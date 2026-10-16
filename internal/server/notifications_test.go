package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ABreadTree/vohive-sms-forward/internal/app"
	"github.com/ABreadTree/vohive-sms-forward/internal/notify"
)

type fakeNotificationSettings struct {
	status    app.NotificationStatus
	saved     []notify.Config
	barkSaves []string
	tgSaves   []string
	tested    []string
}

func (f *fakeNotificationSettings) Status() app.NotificationStatus { return f.status }
func (f *fakeNotificationSettings) SaveAndApply(c notify.Config) error {
	f.saved = append(f.saved, c)
	f.status = app.NotificationStatus{Configured: true, Active: true}
	return nil
}
func (f *fakeNotificationSettings) SaveBark(server, key string) error {
	f.barkSaves = append(f.barkSaves, server+"|"+key)
	f.status.Bark = app.ChannelStatus{Configured: true, Active: true}
	return nil
}
func (f *fakeNotificationSettings) SaveTelegram(token string, chatID int64) error {
	f.tgSaves = append(f.tgSaves, token)
	f.status.Telegram = app.ChannelStatus{Configured: true, Active: true}
	return nil
}
func (f *fakeNotificationSettings) TestChannel(_ context.Context, ch string) error {
	f.tested = append(f.tested, ch)
	return nil
}
func notificationRequest(h http.Handler, method, path, body, host, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func notificationHandler(f *fakeNotificationSettings) http.Handler {
	s := &Server{}
	s.SetNotificationSettings(f)
	return s.Handler()
}

const goodConfig = `{"telegram_bot_token":"secret-token","telegram_chat_id":123,"bark_server":"https://api.day.app","bark_device_key":"secret-key"}`
const localHost = "127.0.0.1:7676"
const localOrigin = "http://127.0.0.1:7676"

func TestNotificationStatusNeverReturnsSecrets(t *testing.T) {
	f := &fakeNotificationSettings{status: app.NotificationStatus{Configured: true, Active: true, Error: "secret-token secret-key"}}
	h := notificationHandler(f)
	for _, w := range []*httptest.ResponseRecorder{notificationRequest(h, "GET", "/api/notifications", "", localHost, ""), notificationRequest(h, "POST", "/api/notifications/telegram", `{"telegram_bot_token":"secret-token","telegram_chat_id":123}`, localHost, localOrigin)} {
		if strings.Contains(w.Body.String(), "secret-token") || strings.Contains(w.Body.String(), "secret-key") {
			t.Fatalf("secret leaked: %s", w.Body.String())
		}
	}
	if len(f.tgSaves) != 1 || f.tgSaves[0] != "secret-token" {
		t.Fatalf("save not performed: count=%d", len(f.tgSaves))
	}
}
func TestNotificationPOSTRejectsCrossOriginAndHost(t *testing.T) {
	f := &fakeNotificationSettings{}
	h := notificationHandler(f)
	for _, tc := range []struct{ host, origin string }{{"evil.example", localOrigin}, {localHost, ""}, {localHost, "https://evil.example"}} {
		w := notificationRequest(h, "POST", "/api/notifications/bark", `{"bark_server":"https://api.day.app","bark_device_key":"key"}`, tc.host, tc.origin)
		if w.Code != 403 {
			t.Fatalf("host=%q origin=%q code=%d", tc.host, tc.origin, w.Code)
		}
	}
	if len(f.barkSaves) != 0 {
		t.Fatal("cross-origin request saved config")
	}
}
func TestNotificationPOSTRejectsOversizeUnknownAndTrailingJSON(t *testing.T) {
	f := &fakeNotificationSettings{}
	h := notificationHandler(f)
	base := `{"telegram_bot_token":"x","telegram_chat_id":1}`
	for _, body := range []string{base + strings.Repeat(" ", 4097), `{"telegram_bot_token":"x","telegram_chat_id":1,"extra":true}`, base + ` {}`} {
		w := notificationRequest(h, "POST", "/api/notifications/telegram", body, localHost, localOrigin)
		if w.Code != 400 {
			t.Fatalf("code=%d body=%q", w.Code, body[:min(len(body), 80)])
		}
	}
	if len(f.tgSaves) != 0 {
		t.Fatal("invalid request saved config")
	}
}
func TestNotificationPOSTRequiresChannelFields(t *testing.T) {
	f := &fakeNotificationSettings{}
	h := notificationHandler(f)
	for _, tc := range []struct{ path, body string }{{"/api/notifications/bark", `{}`}, {"/api/notifications/telegram", `{"telegram_bot_token":"x"}`}} {
		w := notificationRequest(h, "POST", tc.path, tc.body, localHost, localOrigin)
		if w.Code != 400 {
			t.Fatalf("code=%d body=%s", w.Code, tc.body)
		}
	}
	if len(f.barkSaves)+len(f.tgSaves) != 0 {
		t.Fatal("incomplete config saved")
	}
}
func TestNotificationTestRoutesOneChannelOnly(t *testing.T) {
	f := &fakeNotificationSettings{status: app.NotificationStatus{Configured: true, Active: true, Bark: app.ChannelStatus{Configured: true, Active: true}, Telegram: app.ChannelStatus{Configured: true, Active: true}}}
	h := notificationHandler(f)
	for _, ch := range []string{"bark", "telegram"} {
		w := notificationRequest(h, "POST", "/api/notifications/test", `{"channel":"`+ch+`"}`, localHost, localOrigin)
		if w.Code != 200 {
			t.Fatalf("%s code=%d %s", ch, w.Code, w.Body.String())
		}
		var got map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !got["ok"] {
			t.Fatalf("%s response=%s", ch, w.Body.String())
		}
	}
	if len(f.tested) != 2 || f.tested[0] != "bark" || f.tested[1] != "telegram" || len(f.saved) != 0 {
		t.Fatalf("tested=%v saved=%v", f.tested, f.saved)
	}
}
func TestNotificationTestRejectsUnknownChannel(t *testing.T) {
	f := &fakeNotificationSettings{status: app.NotificationStatus{Configured: true, Active: true}}
	h := notificationHandler(f)
	w := notificationRequest(h, "POST", "/api/notifications/test", `{"channel":"sms"}`, localHost, localOrigin)
	if w.Code != 400 || len(f.tested) != 0 {
		t.Fatalf("code=%d tested=%v", w.Code, f.tested)
	}
}
func TestNotificationAPINoStore(t *testing.T) {
	h := notificationHandler(&fakeNotificationSettings{})
	for _, tc := range []struct{ method, path, body, host, origin string }{{"GET", "/api/notifications", "", localHost, ""}, {"POST", "/api/notifications/bark", `{"bark_server":"https://api.day.app","bark_device_key":"key"}`, localHost, localOrigin}, {"POST", "/api/notifications/telegram", `{"telegram_bot_token":"token","telegram_chat_id":123}`, localHost, localOrigin}, {"POST", "/api/notifications/test", `{"channel":"bad"}`, localHost, localOrigin}} {
		w := notificationRequest(h, tc.method, tc.path, tc.body, tc.host, tc.origin)
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s %s cache=%q", tc.method, tc.path, got)
		}
	}
}

func TestSaveBarkEndpointDoesNotRequireTelegram(t *testing.T) {
	f := &fakeNotificationSettings{}
	w := notificationRequest(notificationHandler(f), "POST", "/api/notifications/bark", `{"bark_server":"https://api.day.app","bark_device_key":"pure-key"}`, localHost, localOrigin)
	if w.Code != 200 || len(f.barkSaves) != 1 || len(f.tgSaves) != 0 {
		t.Fatalf("code=%d bark=%v tg=%v", w.Code, f.barkSaves, f.tgSaves)
	}
}
func TestSaveTelegramEndpointDoesNotRequireBark(t *testing.T) {
	f := &fakeNotificationSettings{}
	w := notificationRequest(notificationHandler(f), "POST", "/api/notifications/telegram", `{"telegram_bot_token":"token","telegram_chat_id":123}`, localHost, localOrigin)
	if w.Code != 200 || len(f.tgSaves) != 1 || len(f.barkSaves) != 0 {
		t.Fatalf("code=%d bark=%v tg=%v", w.Code, f.barkSaves, f.tgSaves)
	}
}
func TestOldFullReplacementPOSTRejected(t *testing.T) {
	f := &fakeNotificationSettings{}
	w := notificationRequest(notificationHandler(f), "POST", "/api/notifications", goodConfig, localHost, localOrigin)
	if w.Code != 405 || len(f.saved) != 0 {
		t.Fatalf("code=%d saved=%v", w.Code, f.saved)
	}
}
func TestIndependentEndpointsRejectOriginUnknownAndOversize(t *testing.T) {
	f := &fakeNotificationSettings{}
	h := notificationHandler(f)
	for _, tc := range []struct {
		path, body, host, origin string
		want                     int
	}{
		{"/api/notifications/bark", `{"bark_server":"https://api.day.app","bark_device_key":"key"}`, localHost, "", 403},
		{"/api/notifications/telegram", `{"telegram_bot_token":"token","telegram_chat_id":123}`, "evil.example", localOrigin, 403},
		{"/api/notifications/bark", `{"bark_server":"https://api.day.app","bark_device_key":"key","extra":true}`, localHost, localOrigin, 400},
		{"/api/notifications/telegram", `{"telegram_bot_token":"token","telegram_chat_id":123}` + strings.Repeat(" ", 4097), localHost, localOrigin, 400},
	} {
		w := notificationRequest(h, "POST", tc.path, tc.body, tc.host, tc.origin)
		if w.Code != tc.want {
			t.Fatalf("path=%s code=%d want=%d", tc.path, w.Code, tc.want)
		}
	}
	if len(f.barkSaves)+len(f.tgSaves) != 0 {
		t.Fatal("invalid request saved")
	}
}
func TestStatusRedactsBothChannels(t *testing.T) {
	f := &fakeNotificationSettings{status: app.NotificationStatus{Bark: app.ChannelStatus{Configured: true, Error: "secret-key"}, Telegram: app.ChannelStatus{Configured: true, Error: "secret-token"}}}
	w := notificationRequest(notificationHandler(f), "GET", "/api/notifications", "", localHost, "")
	if strings.Contains(w.Body.String(), "secret-") {
		t.Fatalf("leaked status=%s", w.Body.String())
	}
}
func TestOnlySelectedActiveChannelCanBeTested(t *testing.T) {
	f := &fakeNotificationSettings{status: app.NotificationStatus{Bark: app.ChannelStatus{Configured: true, Active: true}}}
	h := notificationHandler(f)
	w := notificationRequest(h, "POST", "/api/notifications/test", `{"channel":"telegram"}`, localHost, localOrigin)
	if w.Code != 503 || len(f.tested) != 0 {
		t.Fatalf("inactive Telegram code=%d tested=%v", w.Code, f.tested)
	}
	w = notificationRequest(h, "POST", "/api/notifications/test", `{"channel":"bark"}`, localHost, localOrigin)
	if w.Code != 200 || len(f.tested) != 1 || f.tested[0] != "bark" {
		t.Fatalf("Bark code=%d tested=%v", w.Code, f.tested)
	}
}
