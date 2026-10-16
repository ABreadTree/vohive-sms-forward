package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type fakeSMS struct {
	calls  int
	result *modem.SendResult
	err    error
}

func (f *fakeSMS) SendSMS(string, string) (*modem.SendResult, error) {
	f.calls++
	return f.result, f.err
}

type fakeSIM struct{}

func (fakeSIM) CurrentID() int64 { return store.UnknownSIM }

func update(id, chat, from int64, kind, text string) notify.Update {
	var u notify.Update
	u.UpdateID = id
	u.Message.Chat.ID = chat
	u.Message.Chat.Type = kind
	u.Message.From.ID = from
	u.Message.Text = text
	return u
}
func newTestProcessor(t *testing.T) (*Processor, *store.Store, *fakeSMS) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sms := &fakeSMS{result: &modem.SendResult{Parts: 1}}
	return NewProcessor(st, sms, fakeSIM{}, 99), st, sms
}

func TestProcessorRejectsOtherChatAndGroup(t *testing.T) {
	p, _, sms := newTestProcessor(t)
	for _, u := range []notify.Update{update(1, 100, 100, "private", "/send 12345 hi"), update(2, 99, 99, "group", "/send 12345 hi"), update(3, 99, 100, "private", "/send 12345 hi")} {
		reply, err := p.Process(context.Background(), u)
		if err != nil || reply != "" {
			t.Fatalf("reply=%q err=%v", reply, err)
		}
	}
	if sms.calls != 0 {
		t.Fatalf("sent %d times", sms.calls)
	}
}
func TestDraftReplaceExpireCancel(t *testing.T) {
	p, st, sms := newTestProcessor(t)
	for _, u := range []notify.Update{update(1, 99, 99, "private", "/send 12345 old"), update(2, 99, 99, "private", "/send 67890 new"), update(3, 99, 99, "private", "/cancel")} {
		if _, err := p.Process(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Process(context.Background(), update(4, 99, 99, "private", "/confirm")); err != nil {
		t.Fatal(err)
	}
	if sms.calls != 0 {
		t.Fatalf("sent %d times", sms.calls)
	}
	if err := st.SaveTelegramDraft(5, 99, "12345", "late", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(context.Background(), update(6, 99, 99, "private", "/confirm")); err != nil {
		t.Fatal(err)
	}
	if sms.calls != 0 {
		t.Fatalf("sent expired draft %d times", sms.calls)
	}
}
func TestConfirmSendsOnce(t *testing.T) {
	p, st, sms := newTestProcessor(t)
	if reply, err := p.Process(context.Background(), update(1, 99, 99, "private", "/send +12345 hello")); err != nil || !strings.Contains(reply, "+12345") {
		t.Fatalf("preview=%q err=%v", reply, err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	u := update(2, 99, 99, "private", "/confirm")
	reply, err := p.Process(context.Background(), u)
	if err != nil || !strings.Contains(reply, "已发送") {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if _, err := p.Process(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if sms.calls != 1 {
		t.Fatalf("sent %d times", sms.calls)
	}
}
func TestPartialSendStaysUncertain(t *testing.T) {
	p, st, sms := newTestProcessor(t)
	sms.result = &modem.SendResult{Parts: 1}
	sms.err = errors.New("private modem error")
	if _, err := p.Process(context.Background(), update(1, 99, 99, "private", "/send 12345 long text")); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	u := update(2, 99, 99, "private", "/confirm")
	reply, err := p.Process(context.Background(), u)
	if err != nil || !strings.Contains(reply, "待核对") || strings.Contains(reply, "private modem error") {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	p.Process(context.Background(), u)
	if sms.calls != 1 {
		t.Fatalf("sent %d times", sms.calls)
	}
}
func TestInflightAfterRestartNeverResends(t *testing.T) {
	p, st, sms := newTestProcessor(t)
	_ = p
	if err := st.SaveTelegramDraft(1, 99, "12345", "hi", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.BeginTelegramConfirm(2, 99, time.Now()); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	restarted := NewProcessor(st, sms, fakeSIM{}, 99)
	reply, err := restarted.Process(context.Background(), update(2, 99, 99, "private", "/confirm"))
	if err != nil || !strings.Contains(reply, "待核对") || sms.calls != 0 {
		t.Fatalf("reply=%q err=%v calls=%d", reply, err, sms.calls)
	}
}
func TestReplyPersistedUntilDelivered(t *testing.T) {
	p, st, _ := newTestProcessor(t)
	if _, err := p.Process(context.Background(), update(1, 99, 99, "private", "/help")); err != nil {
		t.Fatal(err)
	}
	replies, err := st.PendingTelegramReplies(10)
	if err != nil || len(replies) != 1 {
		t.Fatalf("replies=%+v err=%v", replies, err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	replies, err = st.PendingTelegramReplies(10)
	if err != nil || len(replies) != 0 {
		t.Fatalf("replies=%+v err=%v", replies, err)
	}
}

func TestParseCommandPreservesBodySpacing(t *testing.T) {
	c, err := ParseCommand("/send +12345 first  second\nthird")
	if err != nil || c.Body != "first  second\nthird" {
		t.Fatalf("command=%+v err=%v", c, err)
	}
}

func TestStaleQueuedCommandsNeverSend(t *testing.T) {
	p, _, sms := newTestProcessor(t)
	send := update(1, 99, 99, "private", "/send 12345 old")
	send.Message.Date = time.Now().Add(-time.Hour).Unix()
	if _, err := p.Process(context.Background(), send); err != nil {
		t.Fatal(err)
	}
	confirm := update(2, 99, 99, "private", "/confirm")
	confirm.Message.Date = time.Now().Add(-time.Hour).Unix()
	if _, err := p.Process(context.Background(), confirm); err != nil {
		t.Fatal(err)
	}
	if sms.calls != 0 {
		t.Fatalf("stale commands sent %d SMS", sms.calls)
	}
}

func TestConfirmRequiresDeliveredPreview(t *testing.T) {
	p, st, sms := newTestProcessor(t)
	if _, err := p.Process(context.Background(), update(1, 99, 99, "private", "/send 12345 hi")); err != nil {
		t.Fatal(err)
	}
	reply, err := p.Process(context.Background(), update(2, 99, 99, "private", "/confirm"))
	if err != nil || sms.calls != 0 || !strings.Contains(reply, "预览") {
		t.Fatalf("reply=%q calls=%d err=%v", reply, sms.calls, err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	reply, err = p.Process(context.Background(), update(3, 99, 99, "private", "/confirm"))
	if err != nil || sms.calls != 1 || !strings.Contains(reply, "已发送") {
		t.Fatalf("reply=%q calls=%d err=%v", reply, sms.calls, err)
	}
}

func TestPreSendFailureReportsFailed(t *testing.T) {
	p, st, sms := newTestProcessor(t)
	sms.result = nil
	sms.err = errors.New("secret AT setup failure")
	if _, err := p.Process(context.Background(), update(1, 99, 99, "private", "/send 12345 hi")); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	reply, err := p.Process(context.Background(), update(2, 99, 99, "private", "/confirm"))
	if err != nil || !strings.Contains(reply, "发送失败") || strings.Contains(reply, "待核对") || strings.Contains(reply, "secret") {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
}
