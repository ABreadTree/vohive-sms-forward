package archive

import (
	"context"
	"database/sql"
	"errors"
	"github.com/ABreadTree/vohive-sms-forward/internal/pdu"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
	"path/filepath"
	"testing"
)

type failingHealthStorage struct{ fail bool }

func (f *failingHealthStorage) ListStorage(s string) ([]pdu.Record, error) {
	if f.fail && s == "ME" {
		return nil, errors.New("private modem")
	}
	return nil, nil
}
func (f *failingHealthStorage) DeleteMessages(string, []int) error { return nil }
func TestArchiveHealthRetainsSuccessButMarksCurrentPartialFailure(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := &failingHealthStorage{}
	a := newArchiver(f, st, fakeSIM{}, false, nil)
	if !a.Health().LastSuccess.IsZero() {
		t.Fatal("false initial success")
	}
	if a.SyncIfStale(0) != nil {
		t.Fatal("sync failed")
	}
	good := a.Health()
	f.fail = true
	if a.SyncIfStale(0) == nil {
		t.Fatal("expected failure")
	}
	bad := a.Health()
	if bad.LastSuccess != good.LastSuccess || bad.LastErrorCategory != "sync_failed" || bad.LastAttempt.Before(good.LastAttempt) {
		t.Fatal(bad)
	}
}

func TestArchiveWriteFailureIsUnhealthyEvenIfReadsWork(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "vohive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER reject_archive BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'test read-only equivalent'); END;`); err != nil {
		t.Fatal(err)
	}
	a := newArchiver(fakeStorage{records: []pdu.Record{{Index: 1, Sender: "123", Timestamp: "2026-10-17 13:00:00", Text: "fixture", ConcatRef: -1}}}, st, fakeSIM{}, false, nil)
	if st.Health(context.Background()) != nil {
		t.Fatal("fixture must allow reads")
	}
	if a.SyncIfStale(0) == nil || a.Health().LastErrorCategory != "sync_failed" {
		t.Fatal("write failure recorded as success")
	}
}
