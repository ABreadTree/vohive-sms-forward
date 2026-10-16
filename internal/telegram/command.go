package telegram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type Command struct{ Action, To, Body string }

func ParseCommand(text string) (Command, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Command{}, errors.New("空命令")
	}
	switch fields[0] {
	case "/send":
		if len(fields) < 3 {
			return Command{}, errors.New("用法：/send 手机号 内容")
		}
		to := fields[1]
		num := strings.TrimPrefix(to, "+")
		if len(num) < 3 || len(num) > 20 || strings.Trim(num, "0123456789") != "" {
			return Command{}, errors.New("手机号格式不正确")
		}
		// Keep the user's interior whitespace exactly as entered; only strip
		// the command and recipient prefix, then outer padding.
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), fields[0]))
		rest = strings.TrimSpace(strings.TrimPrefix(rest, to))
		body := rest
		units := len(utf16.Encode([]rune(body)))
		if units == 0 || units > 1000 {
			return Command{}, errors.New("短信内容须为 1–1000 个 UTF-16 单位")
		}
		return Command{Action: "send", To: to, Body: body}, nil
	case "/confirm", "/cancel", "/help":
		if len(fields) != 1 {
			return Command{}, errors.New("该命令不接受参数")
		}
		return Command{Action: strings.TrimPrefix(fields[0], "/")}, nil
	default:
		return Command{}, errors.New("未知命令；发送 /help 查看用法")
	}
}

type CommandStore interface {
	GetTelegramReply(int64) (string, error)
	SaveTelegramDraft(int64, int64, string, string, time.Time) error
	SaveTelegramDraftReply(int64, int64, string, string, time.Time, string) error
	BeginTelegramConfirm(int64, int64, time.Time) (store.Draft, bool, error)
	BeginTelegramConfirmAt(int64, int64, time.Time, int64) (store.Draft, bool, error)
	FinishTelegramConfirm(int64, string, int, string) error
	CancelTelegramDraft(int64, int64, string) error
	SaveTelegramReply(int64, int64, string) error
	RecordOutbound(int64, string, string) error
	PendingTelegramReplies(int) ([]store.Reply, error)
	MarkTelegramReplySent(int64) error
}
type SMSSender interface {
	SendSMS(string, string) (*modem.SendResult, error)
}
type CurrentSIM interface{ CurrentID() int64 }

type Processor struct {
	store         CommandStore
	sender        SMSSender
	sim           CurrentSIM
	allowedChatID int64
	canSend       func() bool
}

func NewProcessor(st CommandStore, sender SMSSender, sim CurrentSIM, allowedChatID int64) *Processor {
	return &Processor{store: st, sender: sender, sim: sim, allowedChatID: allowedChatID}
}
func (p *Processor) SetSendGuard(fn func() bool) { p.canSend = fn }

// Process returns an empty reply for every non-authorized chat. The caller
// sends only persisted replies and may retry them without invoking SMS again.
func (p *Processor) Process(ctx context.Context, u notify.Update) (string, error) {
	chat := u.Message.Chat
	if chat.ID != p.allowedChatID || chat.Type != "private" || u.Message.From.ID != p.allowedChatID {
		return "", nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if old, err := p.store.GetTelegramReply(u.UpdateID); err == nil {
		return old, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	cmd, err := ParseCommand(u.Message.Text)
	if err != nil {
		return p.saveReply(u.UpdateID, chat.ID, err.Error())
	}
	if (cmd.Action == "send" || cmd.Action == "confirm") && u.Message.Date > 0 &&
		time.Since(time.Unix(u.Message.Date, 0)) > 2*time.Minute {
		return p.saveReply(u.UpdateID, chat.ID, "这条命令已超过两分钟，短信未下发；请重新发送 /send。")
	}
	switch cmd.Action {
	case "help":
		return p.saveReply(u.UpdateID, chat.ID, "命令：/send 手机号 内容 → 预览；两分钟内 /confirm → 发送；/cancel → 取消。")
	case "send":
		units := len(utf16.Encode([]rune(cmd.Body)))
		parts := (units + 69) / 70
		expires := time.Now().Add(2 * time.Minute)
		if u.Message.Date > 0 {
			expires = time.Unix(u.Message.Date, 0).Add(2 * time.Minute)
		}
		preview := fmt.Sprintf("短信预览\n收件人：%s\n内容：%s\n预计 %d 段（按 UCS-2 每段最多 70 单位估算）\n有效期 2 分钟。发送 /confirm 才会下发；/cancel 可取消。", cmd.To, cmd.Body, parts)
		if err := p.store.SaveTelegramDraftReply(u.UpdateID, chat.ID, cmd.To, cmd.Body, expires, preview); err != nil {
			return "", err
		}
		return preview, nil
	case "cancel":
		reply := "草稿已取消。"
		if err := p.store.CancelTelegramDraft(u.UpdateID, chat.ID, reply); err != nil {
			return "", err
		}
		return reply, nil
	case "confirm":
		if p.canSend != nil && !p.canSend() {
			return p.saveReply(u.UpdateID, chat.ID, "移动数据关闭状态尚未核验，短信未下发；请稍后重新发送 /confirm。")
		}
		draft, ok, err := p.store.BeginTelegramConfirmAt(u.UpdateID, chat.ID, time.Now(), u.Message.Date)
		if err != nil {
			return "", err
		}
		if !ok {
			return p.store.GetTelegramReply(u.UpdateID)
		}
		if err := ctx.Err(); err != nil {
			return p.store.GetTelegramReply(u.UpdateID)
		}
		result, sendErr := p.sender.SendSMS(draft.To, draft.Body)
		parts := 0
		if result != nil {
			parts = result.Parts
		}
		state, reply := "uncertain", "短信发送结果待核对；不会自动重发。"
		if sendErr != nil && result == nil {
			// SendSMS returns nil only before entering its physical send loop.
			state, reply = "failed", "短信发送失败（尚未提交到模组），请检查设备状态后重新操作。"
		} else if sendErr == nil && parts > 0 {
			if err := p.store.RecordOutbound(p.sim.CurrentID(), draft.To, draft.Body); err == nil {
				state, reply = "sent", fmt.Sprintf("已发送至 %s（%d 段）。", draft.To, parts)
			} else {
				reply = "模组已报告成功，但本地归档失败，结果待核对；不会自动重发。"
			}
		} else if parts > 0 {
			reply = fmt.Sprintf("已提交 %d 段，但后续发送结果待核对；不会自动重发。", parts)
		}
		if err := p.store.FinishTelegramConfirm(u.UpdateID, state, parts, reply); err != nil {
			return "", err
		}
		return reply, nil
	}
	return "", nil
}

func (p *Processor) saveReply(updateID, chatID int64, reply string) (string, error) {
	if err := p.store.SaveTelegramReply(updateID, chatID, reply); err != nil {
		return "", err
	}
	return p.store.GetTelegramReply(updateID)
}
