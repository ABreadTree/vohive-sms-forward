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

func TestSMSOnlyRejectsNetworkEndpointsWithoutWorkers(t *testing.T) {
	s := &Server{smsOnly: true}
	for _, path := range []string{"/api/traffic", "/api/metered"} {
		for _, method := range []string{"GET", "POST"} {
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{"enabled":true}`)))
			if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "sms_only") {
				t.Errorf("%s %s: %d %s", method, path, w.Code, w.Body.String())
			}
		}
	}
}

func TestSMSOnlyDisallowsManualDataReboots(t *testing.T) {
	s := &Server{smsOnly: true}
	for _, body := range []string{`{"enabled":false}`, `{"enabled":true}`, `broken`} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/data", strings.NewReader(body)))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
}

func TestSMSOnlyPageModeIsServerSupplied(t *testing.T) {
	for _, path := range []string{"/", "/index.html"} {
		w := httptest.NewRecorder()
		(&Server{smsOnly: true}).Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `<body data-sms-only="true">`) {
			t.Errorf("%s missing SMS-only bootstrap: %d", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), `id="smsform"`) || !strings.Contains(w.Body.String(), `id="bark-form"`) {
			t.Error("SMS/notification forms missing")
		}
	}
}
