package notify

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type fakeSender struct {
	barkErr   error
	barkCalls int
	tgCalls   int
	started   chan struct{}
}

func (f *fakeSender) PushBark(ctx context.Context, title, body string) error {
	f.barkCalls++
	if f.started != nil {
		close(f.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return f.barkErr
}
func (f *fakeSender) SendTelegram(context.Context, string) error { f.tgCalls++; return nil }

func pendingStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_, err = st.ArchiveInboundWithDeliveries(store.UnknownSIM, "10086", "2026-10-15 12:00:00", "private body", []string{"bark", "telegram"})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDispatcherRetriesBarkWhileTelegramSucceeds(t *testing.T) {
	st := pendingStore(t)
	sender := &fakeSender{barkErr: errors.New("offline")}
	d := NewDispatcher(st, sender)
	now := time.Now()
	if err := d.runOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if sender.barkCalls != 1 || sender.tgCalls != 1 {
		t.Fatalf("calls bark=%d telegram=%d", sender.barkCalls, sender.tgCalls)
	}
	due, err := st.DueDeliveries(now.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].Channel != "bark" || due[0].Attempts != 1 {
		t.Fatalf("remaining=%+v", due)
	}
}

func TestChannelDispatcherOnlyRunsSelectedWorker(t *testing.T) {
	st := pendingStore(t)
	sender := &fakeSender{}
	d := NewDispatcher(st, sender, "bark")
	if err := d.runOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if sender.barkCalls != 1 || sender.tgCalls != 0 {
		t.Fatalf("calls bark=%d tg=%d", sender.barkCalls, sender.tgCalls)
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Channel != "telegram" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestDispatcherResumesDueJobsAfterRestart(t *testing.T) {
	st := pendingStore(t)
	first := NewDispatcher(st, &fakeSender{barkErr: errors.New("offline")})
	now := time.Now()
	if err := first.runOnce(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	secondSender := &fakeSender{}
	second := NewDispatcher(st, secondSender)
	if err := second.runOnce(context.Background(), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if secondSender.barkCalls != 1 || secondSender.tgCalls != 0 {
		t.Fatalf("restart calls bark=%d tg=%d", secondSender.barkCalls, secondSender.tgCalls)
	}
	due, err := st.DueDeliveries(now.Add(3*time.Hour), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("remaining=%+v err=%v", due, err)
	}
}

func TestDispatcherRedactsTransportError(t *testing.T) {
	st := pendingStore(t)
	sender := &fakeSender{barkErr: errors.New("secret-bark-key and private body")}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	if err := NewDispatcher(st, sender).runOnce(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "secret-bark-key") || strings.Contains(logs.String(), "private body") {
		t.Fatalf("sensitive error appeared in log: %q", logs.String())
	}
}

func TestDispatcherStopCancelsRequest(t *testing.T) {
	st := pendingStore(t)
	sender := &fakeSender{started: make(chan struct{})}
	d := NewDispatcher(st, sender)
	d.Start()
	select {
	case <-sender.started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not start")
	}
	done := make(chan struct{})
	go func() { d.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel HTTP request")
	}
}

type blockingBarkSender struct {
	barkStarted     chan struct{}
	telegramStarted chan struct{}
}

func (f *blockingBarkSender) PushBark(ctx context.Context, _, _ string) error {
	close(f.barkStarted)
	<-ctx.Done()
	return ctx.Err()
}
func (f *blockingBarkSender) SendTelegram(context.Context, string) error {
	close(f.telegramStarted)
	return nil
}

func TestDispatcherTelegramNotBlockedByBark(t *testing.T) {
	st := pendingStore(t)
	sender := &blockingBarkSender{barkStarted: make(chan struct{}), telegramStarted: make(chan struct{})}
	d := NewDispatcher(st, sender)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.runOnce(ctx, time.Now()) }()
	select {
	case <-sender.barkStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Bark did not start")
	}
	select {
	case <-sender.telegramStarted:
	case <-time.After(300 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("Telegram was blocked by Bark")
	}
	cancel()
	<-done
}
