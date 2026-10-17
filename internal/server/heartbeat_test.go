package server

import (
	"context"
	"github.com/ABreadTree/vohive-sms-forward/internal/heartbeat"
	"net/http"
	"strings"
	"testing"
)

type fakeHeartbeat struct {
	saved heartbeat.Config
	sent  int
	err   error
}

func (f *fakeHeartbeat) Status() heartbeat.Status {
	return heartbeat.Status{Config: heartbeat.Config{Secret: "super-private", IntervalSeconds: 600}, ConfigError: "private error"}
}
func (f *fakeHeartbeat) Save(c heartbeat.Config) error { f.saved = c; return f.err }
func (f *fakeHeartbeat) CheckNow(context.Context) (heartbeat.Report, error) {
	return heartbeat.Report{}, f.err
}
func (f *fakeHeartbeat) SendNow(context.Context) error { f.sent++; return f.err }
func hbHandler(f *fakeHeartbeat) http.Handler {
	s := &Server{}
	s.SetHeartbeatSettings(f)
	return s.Handler()
}
func TestHeartbeatAPISecretsAndOrigin(t *testing.T) {
	f := &fakeHeartbeat{}
	h := hbHandler(f)
	r := notificationRequest(h, "GET", "/api/heartbeat", "", localHost, "")
	if r.Code != 200 || strings.Contains(r.Body.String(), "super-private") || strings.Contains(r.Body.String(), "private error") {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, p := range []string{"config", "check", "send"} {
		r := notificationRequest(h, "POST", "/api/heartbeat/"+p, `{}`, localHost, "https://evil.example")
		if r.Code != 403 {
			t.Fatal(p, r.Code)
		}
	}
}
func TestHeartbeatAPIStrictJSONAndSave(t *testing.T) {
	f := &fakeHeartbeat{}
	h := hbHandler(f)
	for _, body := range []string{`{"extra":1}`, `{} {}`, strings.Repeat(" ", 5000)} {
		r := notificationRequest(h, "POST", "/api/heartbeat/config", body, localHost, localOrigin)
		if r.Code != 400 {
			t.Fatal(r.Code)
		}
	}
	body := `{"enabled":false,"device_id":"router-1","name":"router","endpoint":"https://ecs.example/v1/heartbeat","secret":"","interval_seconds":600}`
	r := notificationRequest(h, "POST", "/api/heartbeat/config", body, localHost, localOrigin)
	if r.Code != 200 || f.saved.DeviceID != "router-1" {
		t.Fatal(r.Code)
	}
	r = notificationRequest(h, "POST", "/api/heartbeat/send", `{}`, localHost, localOrigin)
	if r.Code != 200 || f.sent != 1 {
		t.Fatal("send", r.Code)
	}
}
