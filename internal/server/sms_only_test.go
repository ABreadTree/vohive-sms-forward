package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
)

func TestSMSOnlyRejectsDataEnable(t *testing.T) {
	s := &Server{smsOnly: true}
	r := httptest.NewRequest(http.MethodPost, "/api/data", strings.NewReader(`{"enabled":true}`))
	w := httptest.NewRecorder()
	s.handleData(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestSMSOnlyDataStateIsExplicit(t *testing.T) {
	s := &Server{smsOnly: true, guardState: modem.DataGuardPending}
	w := httptest.NewRecorder()
	s.handleData(w, httptest.NewRequest(http.MethodGet, "/api/data", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"guard_state":"pending"`) || !strings.Contains(w.Body.String(), `"sms_only":true`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestSMSOnlyPageLocksDataToggle(t *testing.T) {
	s := &Server{smsOnly: true}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dataSMSOnly") || !strings.Contains(w.Body.String(), "b.disabled=dataBusy || dataSMSOnly") {
		t.Fatalf("page lacks SMS-only data lock, status=%d", w.Code)
	}
}
