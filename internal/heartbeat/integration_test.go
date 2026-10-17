package heartbeat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestManagerTLSHealthyFaultRecoveryIntegration(t *testing.T) {
	var count atomic.Int32
	cfg := validConfig()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e := Envelope{Timestamp: r.Header.Get("X-VoHive-Timestamp"), Nonce: r.Header.Get("X-VoHive-Nonce"), Body: b}
		if r.Header.Get("X-VoHive-Device") != cfg.DeviceID || r.Header.Get("X-VoHive-Signature") != signature(cfg, e) {
			t.Error("invalid auth")
			w.WriteHeader(403)
			return
		}
		var p payload
		if json.Unmarshal(b, &p) != nil || !p.Healthy {
			t.Error("invalid health")
		}
		count.Add(1)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	cfg.Endpoint = server.URL + "/v1/heartbeat"
	checker := &testChecker{}
	m := NewManager(filepath.Join(privateDir(t), "heartbeat.json"), checker, Client{HTTP: server.Client()})
	m.Start(context.Background())
	defer m.Stop()
	if m.Save(cfg) != nil {
		t.Fatal("save")
	}
	waitFor(t, func() bool { return count.Load() == 1 && !m.Status().LastSuccess.IsZero() })
	checker.bad.Store(true)
	if m.SendNow(context.Background()) == nil || count.Load() != 1 {
		t.Fatal("unhealthy heartbeat sent")
	}
	checker.bad.Store(false)
	if m.SendNow(context.Background()) != nil || count.Load() != 2 {
		t.Fatal("healthy heartbeat didn't resume")
	}
}
