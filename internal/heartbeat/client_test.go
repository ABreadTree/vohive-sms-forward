package heartbeat

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func healthyReport() Report {
	return Report{Healthy: true, CheckedAt: time.Now().UTC(), Checks: map[string]bool{"at": true, "sim": true, "registration": true, "data_off": true, "archive": true, "database": true, "timely": true}}
}
func TestTLSHeartbeatSignatureAndAck(t *testing.T) {
	c := validConfig()
	r := healthyReport()
	var received bool
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		b, _ := io.ReadAll(q.Body)
		var body map[string]any
		if json.Unmarshal(b, &body) != nil {
			t.Error("invalid body")
		}
		if body["version"] != float64(1) || body["device_id"] != c.DeviceID || body["healthy"] != true {
			t.Error("wrong schema", body)
		}
		ts := q.Header.Get("X-VoHive-Timestamp")
		nonce := q.Header.Get("X-VoHive-Nonce")
		sum := sha256.Sum256(b)
		key, _ := hex.DecodeString(c.Secret)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(strings.Join([]string{"POST", "/v1/heartbeat", c.DeviceID, ts, nonce, hex.EncodeToString(sum[:])}, "\n")))
		if q.Header.Get("X-VoHive-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("signature mismatch")
		}
		received = true
		io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	c.Endpoint = s.URL + "/v1/heartbeat"
	e, err := NewEnvelope(c, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := (Client{HTTP: s.Client()}).Send(context.Background(), c, r, e); err != nil {
		t.Fatal(err)
	}
	if !received {
		t.Fatal("not sent")
	}
}
func TestClientRejectsRedirectBadAckAndUnhealthy(t *testing.T) {
	for _, mode := range []string{"redirect", "bad_ack", "too_large", "unhealthy", "stale"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch mode {
				case "redirect":
					http.Redirect(w, r, "https://elsewhere.example/", 302)
				case "too_large":
					io.WriteString(w, strings.Repeat(" ", 5000)+`{"ok":true}`)
				default:
					io.WriteString(w, `{"ok":false}`)
				}
			}))
			defer s.Close()
			c := validConfig()
			c.Endpoint = s.URL + "/v1/heartbeat"
			r := healthyReport()
			if mode == "unhealthy" {
				r.Healthy = false
			}
			if mode == "stale" {
				r.CheckedAt = time.Now().Add(-151 * time.Second)
			}
			e, _ := NewEnvelope(c, r, time.Now())
			if err := (Client{HTTP: s.Client()}).Send(context.Background(), c, r, e); err == nil {
				t.Fatal("accepted", mode)
			}
			if (mode == "unhealthy" || mode == "stale") && calls != 0 {
				t.Fatal("sent unhealthy")
			}
		})
	}
}

func TestPublishedSigningVector(t *testing.T) {
	c := validConfig()
	c.DeviceID = "router-1"
	c.Secret = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	e := Envelope{Timestamp: "1792195200", Nonce: "000102030405060708090a0b0c0d0e0f", Body: []byte(`{"version":1,"device_id":"router-1","name":"router","interval_seconds":600,"checked_at":"2026-10-17T00:00:00Z","healthy":true,"checks":{"archive":true,"at":true,"data_off":true,"database":true,"registration":true,"sim":true,"timely":true}}`)}
	if got := signature(c, e); got != "2f912cd50efa01093d15890497175019376a4df0e3de1f3fa538878c7604af9b" {
		t.Fatal(got)
	}
}

func TestClientRequiresTrustedTLS(t *testing.T) {
	var called bool
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; io.WriteString(w, `{"ok":true}`) }))
	defer s.Close()
	cfg := validConfig()
	cfg.Endpoint = s.URL + "/v1/heartbeat"
	r := healthyReport()
	e, _ := NewEnvelope(cfg, r, time.Now())
	if (Client{}).Send(context.Background(), cfg, r, e) == nil || called {
		t.Fatal("untrusted TLS accepted")
	}
}
