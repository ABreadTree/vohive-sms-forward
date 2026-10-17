package heartbeat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit opt-in fixture: consumes the installed service's read-only health
// snapshot, then sends to an ephemeral loopback TLS receiver, never to the ECS.
func TestTargetRuntimeHeartbeatSmoke(t *testing.T) {
	if os.Getenv("VOHIVE_TARGET_SMOKE") != "1" {
		t.Skip("target-only opt-in fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:7676/api/heartbeat", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("local health API unavailable")
	}
	defer response.Body.Close()
	var status Status
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status) != nil || !healthy(status.Report, time.Now()) {
		t.Fatal("target health is not ready")
	}
	var calls atomic.Int32
	cfg := validConfig()
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4097))
		env := Envelope{Timestamp: r.Header.Get("X-VoHive-Timestamp"), Nonce: r.Header.Get("X-VoHive-Nonce"), Body: body}
		if r.Header.Get("X-VoHive-Device") != cfg.DeviceID || r.Header.Get("X-VoHive-Signature") != signature(cfg, env) {
			t.Error("signature invalid")
			w.WriteHeader(403)
			return
		}
		calls.Add(1)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer receiver.Close()
	cfg.Endpoint = receiver.URL + "/v1/heartbeat"
	env, err := NewEnvelope(cfg, status.Report, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := Client{HTTP: receiver.Client()}
	if client.Send(ctx, cfg, status.Report, env) != nil || calls.Load() != 1 {
		t.Fatal("target signed TLS send failed")
	}
	status.Report.Healthy = false
	if client.Send(ctx, cfg, status.Report, env) == nil || calls.Load() != 1 {
		t.Fatal("unhealthy report escaped gate")
	}
	t.Log("live modem health -> signed TLS fixture accepted; unhealthy report blocked; production config untouched")
}
