package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ABreadTree/vohive-sms-forward/internal/app"
	"github.com/ABreadTree/vohive-sms-forward/internal/archive"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
	"github.com/ABreadTree/vohive-sms-forward/internal/server"
	"github.com/ABreadTree/vohive-sms-forward/internal/store"
)

type startupSettings struct {
	events *[]string
	err    error
}

func (s startupSettings) StartFromDisk() error {
	*s.events = append(*s.events, "prepare")
	return s.err
}

type startupArchiver struct{ events *[]string }

func (a startupArchiver) Start() { *a.events = append(*a.events, "start") }
func TestStartupUsesDefaultNotificationPath(t *testing.T) {
	dir := t.TempDir()
	if got := notificationConfigPath(dir, ""); got != filepath.Join(dir, "notify.json") {
		t.Fatalf("default path=%s", got)
	}
	if got := notificationConfigPath(dir, "/chosen.json"); got != "/chosen.json" {
		t.Fatalf("override=%s", got)
	}
}
func TestInvalidConfigDoesNotStopWebStartup(t *testing.T) {
	var events []string
	err := startNotificationAndArchive(startupSettings{&events, errors.New("bad config")}, startupArchiver{&events})
	if err == nil || strings.Join(events, ",") != "prepare,start" {
		t.Fatalf("events=%v err=%v", events, err)
	}
	web := server.New(nil, nil, nil, nil, nil, nil, false).Handler()
	w := httptest.NewRecorder()
	web.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `id="smsform"`) {
		t.Fatalf("local SMS UI missing: %d", w.Code)
	}
}
func TestConfiguredStartupPreparesBeforeFirstArchiveSync(t *testing.T) {
	var events []string
	if err := startNotificationAndArchive(startupSettings{&events, nil}, startupArchiver{&events}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "prepare,start" {
		t.Fatalf("events=%v", events)
	}
}

type parkedClient struct{}

func (parkedClient) PushBark(ctx context.Context, _, _ string) error  { <-ctx.Done(); return ctx.Err() }
func (parkedClient) SendTelegram(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() }
func (parkedClient) GetUpdates(ctx context.Context, _ int64) ([]notify.Update, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestRestartRestoresSavedConfig(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := notify.Config{TelegramBotToken: "token", TelegramChatID: 123, BarkServer: "https://api.day.app", BarkDeviceKey: "key"}
	factory := func(notify.Config) app.NotificationClient { return parkedClient{} }
	path := notificationConfigPath(dir, "")
	m1 := app.NewNotifications(path, st, archive.New(nil, st, nil, false, nil), nil, nil, nil, factory)
	if err := m1.SaveAndApply(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveInboundWithDeliveries(store.UnknownSIM, "10086", "2026-10-15 12:00:00", "pending", []string{"bark", "telegram"}); err != nil {
		t.Fatal(err)
	}
	m1.Stop()
	m2 := app.NewNotifications(path, st, archive.New(nil, st, nil, false, nil), nil, nil, nil, factory)
	defer m2.Stop()
	if err := m2.StartFromDisk(); err != nil {
		t.Fatal(err)
	}
	if !m2.Status().Active {
		t.Fatalf("status=%+v", m2.Status())
	}
	jobs, err := st.DueDeliveries(time.Now().Add(time.Hour), 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("pending jobs=%+v err=%v", jobs, err)
	}
}
func TestNotificationAPIRequiresLoopbackEvenWithoutSMSOnly(t *testing.T) {
	if err := validateListenAddr(":7676", false); err == nil {
		t.Fatal("public listener accepted with settings API")
	}
	if err := validateListenAddr("127.0.0.1:7676", false); err != nil {
		t.Fatal(err)
	}
}
