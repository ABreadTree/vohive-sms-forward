package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/archive"
	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type testClient struct {
	mu                     sync.Mutex
	bark, telegram         int
	barkText, telegramText string
	began                  chan struct{}
	ended                  chan struct{}
	block                  bool
}

func (c *testClient) PushBark(ctx context.Context, title, body string) error {
	c.mu.Lock()
	c.bark++
	c.barkText = title + "\n" + body
	c.mu.Unlock()
	if c.block {
		select {
		case c.began <- struct{}{}:
		default:
		}
		<-ctx.Done()
		close(c.ended)
		return ctx.Err()
	}
	return nil
}
func (c *testClient) SendTelegram(ctx context.Context, text string) error {
	c.mu.Lock()
	c.telegram++
	c.telegramText = text
	c.mu.Unlock()
	return nil
}
func (c *testClient) GetUpdates(ctx context.Context, _ int64) ([]notify.Update, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type testSMS struct{ calls int }

func (s *testSMS) SendSMS(string, string) (*modem.SendResult, error) { s.calls++; return nil, nil }
func config() notify.Config {
	return notify.Config{TelegramBotToken: "token", TelegramChatID: 123, BarkServer: "https://api.day.app", BarkDeviceKey: "key"}
}
func newTestManager(t *testing.T, path string, st *store.Store, client *testClient, sms *testSMS) *NotificationManager {
	t.Helper()
	a := archive.New(nil, st, nil, false, nil)
	return NewNotifications(path, st, a, sms, nil, nil, func(notify.Config) NotificationClient { return client })
}
func TestStartFromDiskEnablesBeforeArchiveStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notify.json")
	if _, err := notify.SaveConfig(path, config()); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := newTestManager(t, path, st, &testClient{}, &testSMS{})
	defer m.Stop()
	if err := m.StartFromDisk(); err != nil {
		t.Fatal(err)
	}
	if status := m.Status(); !status.Active || !status.Configured {
		t.Fatalf("status=%+v", status)
	}
	b, tg := notify.TargetFingerprints(config())
	if err := st.PrepareNotificationTargets(b, tg); err != nil {
		t.Fatal(err)
	}
}

func TestStartBarkOnlyAndTelegramOnly(t *testing.T) {
	for _, channel := range []string{"bark", "telegram"} {
		t.Run(channel, func(t *testing.T) {
			dir := t.TempDir()
			st, err := store.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			cfg := notify.Config{}
			if channel == "bark" {
				cfg.BarkServer, cfg.BarkDeviceKey = "https://api.day.app", "key"
			} else {
				cfg.TelegramBotToken, cfg.TelegramChatID = "token", 123
			}
			path := filepath.Join(dir, "notify.json")
			if _, err := notify.SaveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			m := newTestManager(t, path, st, &testClient{}, &testSMS{})
			defer m.Stop()
			if err := m.StartFromDisk(); err != nil {
				t.Fatal(err)
			}
			s := m.Status()
			if channel == "bark" && (!s.Bark.Active || s.Telegram.Active) {
				t.Fatalf("status=%+v", s)
			}
			if channel == "telegram" && (!s.Telegram.Active || s.Bark.Active) {
				t.Fatalf("status=%+v", s)
			}
		})
	}
}

func TestInvalidBarkDoesNotBlockValidTelegram(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config()
	cfg.BarkDeviceKey = "key/extra"
	path := filepath.Join(dir, "notify.json")
	if _, err := notify.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, path, st, &testClient{}, &testSMS{})
	defer m.Stop()
	_ = m.StartFromDisk()
	if s := m.Status(); !s.Telegram.Active || s.Bark.Active || s.Bark.Error != "config_invalid" {
		t.Fatalf("status=%+v", s)
	}
}

func TestSaveBarkPreservesTelegramPollerAndState(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, &testClient{}, &testSMS{})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	oldPoller := m.poller
	if err := st.SaveTelegramDraftReply(10, 77, "123456", "secret", time.Now().Add(time.Minute), "preview"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLastTelegramUpdateID(10); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveBark("https://api.day.app", "new-key"); err != nil {
		t.Fatal(err)
	}
	if m.poller != oldPoller || !m.Status().Telegram.Active {
		t.Fatalf("Telegram restarted: %+v", m.Status())
	}
	id, err := st.LastTelegramUpdateID()
	if err != nil || id != 10 {
		t.Fatalf("offset=%d err=%v", id, err)
	}
}

func TestSaveTelegramPreservesBarkPending(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := &testClient{began: make(chan struct{}, 1), ended: make(chan struct{}), block: true}
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, c, &testSMS{})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveInboundWithDeliveries(store.UnknownSIM, "10086", "2026-10-15 12:00:00", "body", []string{"bark"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.began:
	case <-time.After(7 * time.Second):
		t.Fatal("Bark worker did not start")
	}
	oldBark := m.barkDispatcher
	if err := m.SaveTelegram("new-token", 123); err != nil {
		t.Fatal(err)
	}
	if m.barkDispatcher != oldBark || !m.Status().Bark.Active {
		t.Fatalf("Bark restarted: %+v", m.Status())
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 1 || jobs[0].Channel != "bark" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func TestSaveFailureKeepsUnchangedChannelActive(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, &testClient{}, &testSMS{})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	oldPoller := m.poller
	if err := m.SaveBark("https://api.day.app", "bad/key"); err == nil {
		t.Fatal("bad Bark key saved")
	}
	if m.poller != oldPoller || !m.Status().Telegram.Active {
		t.Fatalf("Telegram changed: %+v", m.Status())
	}
}

func TestChannelTestNeverSendsSMS(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sms := &testSMS{}
	c := &testClient{}
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, c, sms)
	defer m.Stop()
	if err := m.SaveBark("https://api.day.app", "key"); err != nil {
		t.Fatal(err)
	}
	if err := m.TestChannel(context.Background(), "bark"); err != nil {
		t.Fatal(err)
	}
	if err := m.TestChannel(context.Background(), "telegram"); err == nil {
		t.Fatal("unconfigured Telegram test accepted")
	}
	if sms.calls != 0 || c.bark != 1 || c.telegram != 0 {
		t.Fatalf("sms=%d bark=%d tg=%d", sms.calls, c.bark, c.telegram)
	}
}

func TestBlockedBarkTestDoesNotBlockTelegramActions(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := &testClient{began: make(chan struct{}, 1), ended: make(chan struct{}), block: true}
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, c, &testSMS{})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	barkDone := make(chan error, 1)
	go func() { barkDone <- m.TestChannel(ctx, "bark") }()
	select {
	case <-c.began:
	case <-time.After(time.Second):
		t.Fatal("Bark test did not start")
	}
	tgDone := make(chan error, 1)
	go func() {
		if err := m.TestChannel(context.Background(), "telegram"); err != nil {
			tgDone <- err
			return
		}
		tgDone <- m.SaveTelegram("new-token", 123)
	}()
	select {
	case err := <-tgDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-barkDone
		t.Fatal("Telegram test/save blocked behind Bark network request")
	}
	cancel()
	<-barkDone
}

func TestBarkRotationWaitsForBarkTest(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := &testClient{began: make(chan struct{}, 1), ended: make(chan struct{}), block: true}
	newClient := &testClient{}
	m := NewNotifications(filepath.Join(dir, "notify.json"), st, archive.New(nil, st, nil, false, nil), &testSMS{}, nil, nil, func(c notify.Config) NotificationClient {
		if c.BarkDeviceKey == "key" {
			return old
		}
		return newClient
	})
	defer m.Stop()
	if err := m.SaveBark("https://api.day.app", "key"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	barkDone := make(chan error, 1)
	go func() { barkDone <- m.TestChannel(ctx, "bark") }()
	select {
	case <-old.began:
	case <-time.After(time.Second):
		t.Fatal("Bark test did not start")
	}
	rotationDone := make(chan error, 1)
	go func() { rotationDone <- m.SaveBark("https://api.day.app", "new-key") }()
	select {
	case err := <-rotationDone:
		t.Fatalf("rotation overtook active Bark test: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	<-barkDone
	select {
	case err := <-rotationDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("rotation did not finish after Bark test")
	}
}
func TestSaveAndApplyStartsBothWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(dir, "notify.json")
	c := &testClient{}
	m := newTestManager(t, path, st, c, &testSMS{})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	if !m.Status().Active {
		t.Fatalf("status=%+v", m.Status())
	}
	if _, err := notify.LoadConfig(path); err != nil {
		t.Fatal(err)
	}
}
func TestSaveFailureKeepsOldRuntimeAndFingerprints(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(dir, "notify.json")
	c := &testClient{}
	m := newTestManager(t, path, st, c, &testSMS{})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	next := config()
	next.BarkDeviceKey = "other"
	if err := m.SaveAndApply(next); err == nil {
		t.Fatal("unsafe save accepted")
	}
	if !m.Status().Active {
		t.Fatalf("old runtime stopped: %+v", m.Status())
	}
	got, err := notify.LoadConfig(path)
	if err != nil || got.BarkDeviceKey != "key" {
		t.Fatalf("old config changed %+v %v", got, err)
	}
}
func TestPrepareFailureDisablesNotificationsUntilRetry(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, &testClient{}, &testSMS{})
	defer m.Stop()
	st.Close()
	if err := m.SaveAndApply(config()); err == nil {
		t.Fatal("closed DB accepted")
	}
	if s := m.Status(); s.Active || s.Error != "prepare_failed" {
		t.Fatalf("status=%+v", s)
	}
}
func TestRotationWaitsForOldRequestsAndClearsPending(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := &testClient{began: make(chan struct{}, 1), ended: make(chan struct{}), block: true}
	newClient := &testClient{}
	m := NewNotifications(filepath.Join(dir, "notify.json"), st, archive.New(nil, st, nil, false, nil), &testSMS{}, nil, nil, func(c notify.Config) NotificationClient {
		if c.BarkDeviceKey == "key" {
			return old
		}
		return newClient
	})
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveInboundWithDeliveries(store.UnknownSIM, "10086", "2026-10-15 12:00:00", "secret", []string{"bark", "telegram"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.began:
	case <-time.After(7 * time.Second):
		t.Fatal("old request never started")
	}
	next := config()
	next.BarkDeviceKey = "new-key"
	if err := m.SaveAndApply(next); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.ended:
	default:
		t.Fatal("old request still active after rotation")
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Channel == "bark" {
			t.Fatal("old Bark job survived rotation")
		}
	}
}
func TestTestChannelIsPushOnly(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := &testClient{}
	sms := &testSMS{}
	m := newTestManager(t, filepath.Join(dir, "notify.json"), st, c, sms)
	defer m.Stop()
	if err := m.SaveAndApply(config()); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"bark", "telegram"} {
		if err := m.TestChannel(context.Background(), ch); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	b, tg, bt, tt := c.bark, c.telegram, c.barkText, c.telegramText
	c.mu.Unlock()
	if b != 1 || tg != 1 || !strings.Contains(bt, "VoHive Mac 测试通知 · bark") || !strings.Contains(tt, "VoHive Mac 测试通知 · telegram") {
		t.Fatalf("bark=%d telegram=%d texts=%q %q", b, tg, bt, tt)
	}
	if sms.calls != 0 {
		t.Fatalf("sent SMS %d", sms.calls)
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	msgs, err := st.ListMessages(store.UnknownSIM, 10)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("messages=%+v err=%v", msgs, err)
	}
}
func TestInvalidDiskConfigKeepsLocalRuntimeUnconfigured(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(dir, "notify.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(t, path, st, &testClient{}, &testSMS{})
	defer m.Stop()
	if err := m.StartFromDisk(); err == nil {
		t.Fatal("invalid config not reported")
	}
	if s := m.Status(); s.Active || s.Error != "config_invalid" {
		t.Fatalf("status=%+v", s)
	}
}
