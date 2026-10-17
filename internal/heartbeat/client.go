package heartbeat

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var ErrSend = errors.New("heartbeat delivery failed")
var ErrUnhealthy = errors.New("heartbeat self-check not healthy")

type Envelope struct {
	Timestamp, Nonce string
	Body             []byte
}
type payload struct {
	Version         int    `json:"version"`
	DeviceID        string `json:"device_id"`
	Name            string `json:"name"`
	IntervalSeconds int    `json:"interval_seconds"`
	Report
}

func healthy(r Report, now time.Time) bool {
	if !r.Healthy || !r.Fresh(now) {
		return false
	}
	for _, k := range []string{"at", "sim", "registration", "data_off", "archive", "database", "timely"} {
		if !r.Checks[k] {
			return false
		}
	}
	return true
}
func NewEnvelope(c Config, r Report, now time.Time) (Envelope, error) {
	var e Envelope
	if c.Validate() != nil {
		return e, ErrConfig
	}
	if !healthy(r, now) {
		return e, ErrUnhealthy
	}
	n := make([]byte, 16)
	if _, err := rand.Read(n); err != nil {
		return e, ErrSend
	}
	b, err := json.Marshal(payload{Version: 1, DeviceID: c.DeviceID, Name: c.Name, IntervalSeconds: c.IntervalSeconds, Report: r})
	if err != nil || len(b) > 4096 {
		return e, ErrSend
	}
	return Envelope{Timestamp: strconv.FormatInt(now.Unix(), 10), Nonce: hex.EncodeToString(n), Body: b}, nil
}

type Client struct{ HTTP *http.Client }

func (c Client) Send(ctx context.Context, cfg Config, r Report, e Envelope) error {
	if cfg.Validate() != nil {
		return ErrConfig
	}
	if !cfg.Enabled || !healthy(r, time.Now()) {
		return ErrUnhealthy
	}
	ts, err := strconv.ParseInt(e.Timestamp, 10, 64)
	age := time.Now().Unix() - ts
	if err != nil || age < 0 || age > 120 || len(e.Nonce) != 32 || len(e.Body) == 0 || len(e.Body) > 4096 {
		return ErrSend
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(e.Body))
	if err != nil {
		return ErrSend
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range map[string]string{"Device": cfg.DeviceID, "Timestamp": e.Timestamp, "Nonce": e.Nonce, "Signature": signature(cfg, e)} {
		req.Header.Set("X-VoHive-"+k, v)
	}
	hc := http.Client{Timeout: 10 * time.Second}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		return ErrSend
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrSend
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(b) > 4096 {
		return ErrSend
	}
	var ack struct {
		OK bool `json:"ok"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(&ack) != nil || !ack.OK {
		return ErrSend
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return ErrSend
	}
	return nil
}

func signature(cfg Config, e Envelope) string {
	sum := sha256.Sum256(e.Body)
	key, _ := hex.DecodeString(cfg.Secret)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strings.Join([]string{"POST", "/v1/heartbeat", cfg.DeviceID, e.Timestamp, e.Nonce, hex.EncodeToString(sum[:])}, "\n")))

	return hex.EncodeToString(mac.Sum(nil))
}
