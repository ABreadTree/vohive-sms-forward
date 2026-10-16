package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestTelegramConfirmIsDurableAndSingleUse(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if err := st.SaveTelegramDraft(10, 99, "+12345", "hello", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTelegramReplySent(10); err != nil {
		t.Fatal(err)
	}
	d, ok, err := st.BeginTelegramConfirm(11, 99, now)
	if err != nil || !ok || d.To != "+12345" || d.Body != "hello" {
		t.Fatalf("draft=%+v ok=%v err=%v", d, ok, err)
	}
	_, ok, err = st.BeginTelegramConfirm(11, 99, now)
	if err != nil || ok {
		t.Fatalf("duplicate confirmation ok=%v err=%v", ok, err)
	}
	if err := st.FinishTelegramConfirm(11, "sent", 1, "已发送"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishTelegramConfirm(11, "sent", 1, "已发送"); err == nil {
		t.Fatal("completion accepted twice")
	}
}

func TestTelegramV6MigrationAddsPreviewDeliveryTime(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "vohive.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE telegram_commands (
	    update_id INTEGER PRIMARY KEY,chat_id INTEGER NOT NULL,
	    recipient TEXT NOT NULL DEFAULT '',body TEXT NOT NULL DEFAULT '',
	    state TEXT NOT NULL,parts INTEGER NOT NULL DEFAULT 0,
	    reply TEXT NOT NULL,reply_sent INTEGER NOT NULL DEFAULT 0);
	    INSERT INTO telegram_commands(update_id,chat_id,state,reply,reply_sent)
	    VALUES (1,99,'draft','preview',1);
	    PRAGMA user_version=6;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var sentAt int64
	if err := st.db.QueryRow(`SELECT reply_sent_at FROM telegram_commands WHERE update_id=1`).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if sentAt == 0 {
		t.Fatal("existing sent preview timestamp not migrated")
	}
}

func TestTelegramDraftExpiresAndRepliesPersist(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if err := st.SaveTelegramDraft(10, 99, "12345", "hello", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	_, ok, err := st.BeginTelegramConfirm(11, 99, now)
	if err != nil || ok {
		t.Fatalf("expired draft ok=%v err=%v", ok, err)
	}
	replies, err := st.PendingTelegramReplies(10)
	if err != nil || len(replies) == 0 {
		t.Fatalf("replies=%+v err=%v", replies, err)
	}
	if err := st.MarkTelegramReplySent(11); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTelegramReplySent(10); err != nil {
		t.Fatal(err)
	}
	replies, err = st.PendingTelegramReplies(10)
	if err != nil || len(replies) != 0 {
		t.Fatalf("pending=%+v err=%v", replies, err)
	}
}
