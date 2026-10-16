package store

import (
	"testing"
	"time"
)

func targetFixture(t *testing.T) *Store {
	t.Helper()
	s := openStore(t, t.TempDir())
	if err := s.PrepareNotificationTargets("bark-A", "tg-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveInboundWithDeliveries(UnknownSIM, "10086", "2026-10-15 12:00:00", "body", []string{"bark", "telegram"}); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestPrepareTargetsClearsOnlyChangedChannel(t *testing.T) {
	s := targetFixture(t)
	if err := s.PrepareNotificationTargets("bark-B", "tg-A"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Channel != "telegram" {
		t.Fatalf("wrong pending jobs: %+v", jobs)
	}
}
func TestPrepareTargetsIdempotent(t *testing.T) {
	s := targetFixture(t)
	if err := s.PrepareNotificationTargets("bark-A", "tg-A"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("same target lost jobs: %+v", jobs)
	}
}
func TestPrepareTelegramTargetClearsOldCommandsAndOffset(t *testing.T) {
	s := targetFixture(t)
	if err := s.SaveTelegramDraftReply(10, 77, "123456", "secret sms", time.Now().Add(time.Minute), "preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastTelegramUpdateID(10); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOutbound(UnknownSIM, "123456", "sent sms"); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareNotificationTargets("bark-A", "tg-B"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Channel != "bark" {
		t.Fatalf("wrong pending jobs: %+v", jobs)
	}
	var n int
	for _, table := range []string{"telegram_drafts", "telegram_commands"} {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s count=%d err=%v", table, n, err)
		}
	}
	id, err := s.LastTelegramUpdateID()
	if err != nil || id != 0 {
		t.Fatalf("offset=%d err=%v", id, err)
	}
	msgs, err := s.ListMessages(UnknownSIM, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Direction == "out" && m.Body == "sent sms" {
			found = true
		}
	}
	if !found {
		t.Fatal("sent SMS record lost")
	}
}

func TestPrepareNotificationTargetBarkPreservesTelegramState(t *testing.T) {
	s := targetFixture(t)
	if err := s.SaveTelegramDraftReply(10, 77, "123456", "secret sms", time.Now().Add(time.Minute), "preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLastTelegramUpdateID(10); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareNotificationTarget("bark", "bark-B"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Channel != "telegram" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	id, err := s.LastTelegramUpdateID()
	if err != nil || id != 10 {
		t.Fatalf("offset=%d err=%v", id, err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM telegram_drafts").Scan(&n); err != nil || n != 1 {
		t.Fatalf("drafts=%d err=%v", n, err)
	}
}

func TestPrepareNotificationTargetTelegramPreservesBarkPending(t *testing.T) {
	s := targetFixture(t)
	if err := s.PrepareNotificationTarget("telegram", "tg-B"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Channel != "bark" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}
