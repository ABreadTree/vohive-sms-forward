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

type fakeUpdates struct {
	updates   []notify.Update
	err       error
	getCalls  int
	offsets   []int64
	sendErr   error
	sendCalls int
	block     bool
	started   chan struct{}
}

func (f *fakeUpdates) GetUpdates(ctx context.Context, offset int64) ([]notify.Update, error) {
	f.getCalls++
	f.offsets = append(f.offsets, offset)
	if f.block {
		close(f.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.updates, nil
}
func (f *fakeUpdates) SendTelegram(context.Context, string) error { f.sendCalls++; return f.sendErr }

func pollerFixture(t *testing.T) (*store.Store, *Processor, *fakeUpdates) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := NewProcessor(st, &fakeSMS{}, fakeSIM{}, 99)
	return st, p, &fakeUpdates{}
}
func TestPollerRetriesNetworkFailure(t *testing.T) {
	st, p, client := pollerFixture(t)
	poll := NewPoller(client, p, st)
	client.err = errors.New("offline")
	if err := poll.runOnce(context.Background()); err == nil {
		t.Fatal("wanted network error")
	}
	client.err = nil
	client.updates = []notify.Update{update(1, 99, 99, "private", "/help")}
	if err := poll.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	last, err := st.LastTelegramUpdateID()
	if err != nil || last != 1 || client.sendCalls != 1 {
		t.Fatalf("last=%d send=%d err=%v", last, client.sendCalls, err)
	}
}
func TestPollerReportsWebhookConflict(t *testing.T) {
	st, p, client := pollerFixture(t)
	client.err = &notify.APIError{Platform: "telegram", Status: 409}
	err := NewPoller(client, p, st).runOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "webhook") {
		t.Fatalf("error=%v", err)
	}
}
func TestPollerResumesFromStoredOffset(t *testing.T) {
	st, p, client := pollerFixture(t)
	if err := st.SetLastTelegramUpdateID(20); err != nil {
		t.Fatal(err)
	}
	if err := NewPoller(client, p, st).runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.offsets) != 1 || client.offsets[0] != 21 {
		t.Fatalf("offsets=%v", client.offsets)
	}
}
func TestPollerRetriesPendingReplyWithoutResendingSMS(t *testing.T) {
	st, p, client := pollerFixture(t)
	sms := &fakeSMS{result: &modem.SendResult{Parts: 1}}
	p = NewProcessor(st, sms, fakeSIM{}, 99)
	if _, err := p.Process(context.Background(), update(1, 99, 99, "private", "/send 12345 hi")); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkTelegramReplySent(1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(context.Background(), update(2, 99, 99, "private", "/confirm")); err != nil {
		t.Fatal(err)
	}
	client.sendErr = errors.New("offline")
	poll := NewPoller(client, p, st)
	if err := poll.runOnce(context.Background()); err == nil {
		t.Fatal("wanted reply failure")
	}
	client.sendErr = nil
	if err := poll.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	replies, err := st.PendingTelegramReplies(10)
	if err != nil || len(replies) != 0 || client.sendCalls != 2 || sms.calls != 1 {
		t.Fatalf("replies=%v calls=%d err=%v", replies, client.sendCalls, err)
	}
}
func TestPollerStopCancelsRequest(t *testing.T) {
	st, p, client := pollerFixture(t)
	client.block = true
	client.started = make(chan struct{})
	poll := NewPoller(client, p, st)
	poll.Start(context.Background())
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not start")
	}
	done := make(chan struct{})
	go func() { poll.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel getUpdates")
	}
}

func TestQueuedConfirmOlderThanPreviewDoesNotSend(t *testing.T) {
	st, _, client := pollerFixture(t)
	sms := &fakeSMS{result: &modem.SendResult{Parts: 1}}
	p := NewProcessor(st, sms, fakeSIM{}, 99)
	send := update(1, 99, 99, "private", "/send 12345 hi")
	confirm := update(2, 99, 99, "private", "/confirm")
	stamp := time.Now().Unix()
	send.Message.Date, confirm.Message.Date = stamp, stamp
	client.updates = []notify.Update{send, confirm}
	if err := NewPoller(client, p, st).runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sms.calls != 0 {
		t.Fatalf("queued confirm sent %d SMS before user saw preview", sms.calls)
	}
}
