package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func notificationPage(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	(&Server{}).Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("page HTTP %d", w.Code)
	}
	b, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func notificationBlock(t *testing.T) string {
	t.Helper()
	s := notificationPage(t)
	start := strings.Index(s, "/* ---------------- notification settings ---------------- */")
	end := strings.Index(s, "/* ---------------- theme ---------------- */")
	if start < 0 || end < start {
		t.Fatal("notification settings script missing")
	}
	return s[start:end]
}
func TestNotificationPageHasPasswordInputsAndSeparateTests(t *testing.T) {
	s := notificationPage(t)
	for _, fragment := range []string{`id="tg-token" type="password"`, `id="bark-key" type="password"`, `id="tg-chat-id"`, `id="bark-server"`, `id="save-bark"`, `id="save-telegram"`, `id="test-bark"`, `id="test-telegram"`, `id="notification-status"`} {
		if !strings.Contains(s, fragment) {
			t.Fatalf("missing %s", fragment)
		}
	}
	block := notificationBlock(t)
	for _, fragment := range []string{`/api/notifications`, `/api/notifications/test`, `"bark"`, `"telegram"`} {
		if !strings.Contains(block, fragment) {
			t.Fatalf("missing notification action %s", fragment)
		}
	}
}
func TestNotificationPageDoesNotPersistCredentials(t *testing.T) {
	block := notificationBlock(t)
	for _, bad := range []string{"localStorage", "sessionStorage", "location.", "URLSearchParams", "?token=", "?key="} {
		if strings.Contains(block, bad) {
			t.Fatalf("credential persistence or URL construction: %s", bad)
		}
	}
}
func TestNotificationPageClearsSecretsAfterSave(t *testing.T) {
	block := notificationBlock(t)
	for _, fragment := range []string{`$("tg-token").value=""`, `$("bark-key").value=""`} {
		if !strings.Contains(block, fragment) {
			t.Fatalf("save does not clear %s", fragment)
		}
	}
}
func TestNotificationPagePreservesSMSOnlyDataLock(t *testing.T) {
	s := notificationPage(t)
	if !strings.Contains(s, `dataSMSOnly=!!j.sms_only`) || !strings.Contains(s, `if(dataBusy || dataSMSOnly`) {
		t.Fatal("SMS-only data lock UI missing")
	}
}

func TestNotificationPageHasIndependentFormsAndActions(t *testing.T) {
	s := notificationPage(t)
	for _, fragment := range []string{`id="bark-form"`, `id="telegram-form"`, `id="bark-status"`, `id="telegram-status"`, `id="save-bark"`, `id="save-telegram"`, `id="test-bark"`, `id="test-telegram"`, `id="bark-note"`, `id="telegram-note"`} {
		if !strings.Contains(s, fragment) {
			t.Fatalf("missing %s", fragment)
		}
	}
	if strings.Contains(s, "四项均须填写") || strings.Contains(s, "再次保存会完整替换原配置") {
		t.Fatal("old full-replacement copy remains")
	}
}
func TestNotificationPageUsesPerChannelEndpoints(t *testing.T) {
	block := notificationBlock(t)
	for _, fragment := range []string{`fetch("/api/notifications/bark"`, `fetch("/api/notifications/telegram"`, `fetch("/api/notifications/test"`} {
		if !strings.Contains(block, fragment) {
			t.Fatalf("missing action %s", fragment)
		}
	}
	if strings.Contains(block, `fetch("/api/notifications",{method:"POST"`) {
		t.Fatal("old full replacement POST remains")
	}
	for _, bad := range []string{"localStorage", "sessionStorage", "location.", "URLSearchParams"} {
		if strings.Contains(block, bad) {
			t.Fatalf("credential persistence or URL: %s", bad)
		}
	}
}

func TestNotificationPageAggregateStatusAfterLocalAction(t *testing.T) {
	block := notificationBlock(t)
	if !strings.Contains(block, `const anyActive=notificationState.bark.active||notificationState.telegram.active;`) {
		t.Fatal("aggregate status depends on absent top-level fields after a local action")
	}
}
