package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Draft struct {
	To      string
	Body    string
	Expires time.Time
}

type Reply struct {
	UpdateID int64
	ChatID   int64
	Text     string
}

func (s *Store) GetTelegramReply(updateID int64) (string, error) {
	var reply string
	err := s.db.QueryRow(`SELECT reply FROM telegram_commands WHERE update_id=?`, updateID).Scan(&reply)
	return reply, err
}

func (s *Store) SaveTelegramDraft(updateID, chatID int64, to, body string, expires time.Time) error {
	return s.SaveTelegramDraftReply(updateID, chatID, to, body, expires, "草稿已保存")
}

// SaveTelegramDraftReply commits both the draft and its replay-safe reply.
func (s *Store) SaveTelegramDraftReply(updateID, chatID int64, to, body string, expires time.Time, reply string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO telegram_commands
	    (update_id,chat_id,recipient,body,state,reply) VALUES (?,?,?,?,'draft',?)`, updateID, chatID, to, body, reply)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		_, err = tx.Exec(`INSERT INTO telegram_drafts (chat_id,update_id,recipient,body,expires_at)
		    VALUES (?,?,?,?,?) ON CONFLICT(chat_id) DO UPDATE SET
		    update_id=excluded.update_id,recipient=excluded.recipient,
		    body=excluded.body,expires_at=excluded.expires_at
		    WHERE excluded.update_id > telegram_drafts.update_id`, chatID, updateID, to, body, expires.Unix())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CancelTelegramDraft(updateID, chatID int64, reply string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT OR IGNORE INTO telegram_commands
	    (update_id,chat_id,state,reply) VALUES (?,?,'cancelled',?)`, updateID, chatID, reply)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		if _, err = tx.Exec(`DELETE FROM telegram_drafts WHERE chat_id=? AND update_id<?`, chatID, updateID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveTelegramReply(updateID, chatID int64, reply string) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO telegram_commands
	    (update_id,chat_id,state,reply) VALUES (?,?,'completed',?)`, updateID, chatID, reply)
	return err
}

// BeginTelegramConfirm consumes the draft and records inflight in one
// transaction. After this commits, no replay may send the same SMS again.
func (s *Store) BeginTelegramConfirm(updateID, chatID int64, now time.Time) (Draft, bool, error) {
	return s.BeginTelegramConfirmAt(updateID, chatID, now, 0)
}

// BeginTelegramConfirmAt also checks the Telegram message date. A queued
// /confirm sent before the user received the preview is never authorization.
func (s *Store) BeginTelegramConfirmAt(updateID, chatID int64, now time.Time, commandDate int64) (Draft, bool, error) {
	var d Draft
	tx, err := s.db.Begin()
	if err != nil {
		return d, false, err
	}
	defer tx.Rollback()
	var prior int64
	err = tx.QueryRow(`SELECT update_id FROM telegram_commands WHERE update_id=?`, updateID).Scan(&prior)
	if err == nil {
		return d, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return d, false, err
	}
	var expires, previewSent, previewSentAt int64
	err = tx.QueryRow(`SELECT d.recipient,d.body,d.expires_at,c.reply_sent,c.reply_sent_at
	    FROM telegram_drafts d JOIN telegram_commands c ON c.update_id=d.update_id
	    WHERE d.chat_id=? AND d.update_id<?`, chatID, updateID).
		Scan(&d.To, &d.Body, &expires, &previewSent, &previewSentAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, false, err
	}
	validDraft := err == nil && expires > now.Unix()
	ok := validDraft && previewSent == 1 && (commandDate == 0 || commandDate > previewSentAt)
	if !ok {
		d = Draft{}
	}
	if ok || (err == nil && !validDraft) {
		if _, err = tx.Exec(`DELETE FROM telegram_drafts WHERE chat_id=? AND update_id<?`, chatID, updateID); err != nil {
			return d, false, err
		}
	}
	state, reply := "no_draft", "没有有效草稿；请先发送 /send 手机号 内容。"
	if validDraft && !ok {
		state, reply = "no_preview", "预览尚未送达，或确认命令早于预览；请阅读预览后重新发送 /confirm。"
	}
	if ok {
		state, reply = "inflight", "短信发送结果待核对；不会自动重发。"
		d.Expires = time.Unix(expires, 0)
	}
	if _, err = tx.Exec(`INSERT INTO telegram_commands
	    (update_id,chat_id,recipient,body,state,reply) VALUES (?,?,?,?,?,?)`, updateID, chatID, d.To, d.Body, state, reply); err != nil {
		return d, false, err
	}
	if err = tx.Commit(); err != nil {
		return d, false, err
	}
	return d, ok, nil
}

func (s *Store) FinishTelegramConfirm(updateID int64, state string, parts int, summary string) error {
	if state != "sent" && state != "failed" && state != "uncertain" {
		return fmt.Errorf("invalid confirmation state")
	}
	res, err := s.db.Exec(`UPDATE telegram_commands SET state=?,parts=?,reply=?
	    WHERE update_id=? AND state='inflight'`, state, parts, summary, updateID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) PendingTelegramReplies(limit int) ([]Reply, error) {
	rows, err := s.db.Query(`SELECT update_id,chat_id,reply FROM telegram_commands
	    WHERE reply_sent=0 AND reply<>'' ORDER BY update_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reply
	for rows.Next() {
		var r Reply
		if err := rows.Scan(&r.UpdateID, &r.ChatID, &r.Text); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkTelegramReplySent(updateID int64) error {
	_, err := s.db.Exec(`UPDATE telegram_commands SET reply_sent=1,reply_sent_at=CAST(strftime('%s','now') AS INTEGER) WHERE update_id=?`, updateID)
	return err
}

func (s *Store) LastTelegramUpdateID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='telegram_last_update'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) SetLastTelegramUpdateID(id int64) error {
	_, err := s.db.Exec(`INSERT INTO meta(key,value) VALUES ('telegram_last_update',?)
	    ON CONFLICT(key) DO UPDATE SET value=MAX(CAST(value AS INTEGER),CAST(excluded.value AS INTEGER))`, id)
	return err
}
