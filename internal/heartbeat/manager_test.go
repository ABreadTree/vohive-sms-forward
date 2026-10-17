package heartbeat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type testChecker struct {
	bad   atomic.Bool
	calls atomic.Int32
}

func (c *testChecker) Check(ctx context.Context) Report {
	c.calls.Add(1)
	r := healthyReport()
	r.Healthy = !c.bad.Load()
	return r
}

type testSender struct {
	calls   atomic.Int32
	fail    atomic.Bool
	block   bool
	started chan struct{}
}

func (s *testSender) Send(ctx context.Context, c Config, r Report, e Envelope) error {
	s.calls.Add(1)
	if s.block {
		select {
		case s.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	if s.fail.Load() {
		return errors.New("private url")
	}
	return nil
}
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestManagerDisabledChecksWithoutSendingAndGatesManualSend(t *testing.T) {
	c := &testChecker{}
	s := &testSender{}
	m := NewManager(filepath.Join(privateDir(t), "heartbeat.json"), c, s)
	m.Start(context.Background())
	defer m.Stop()
	waitFor(t, func() bool { return c.calls.Load() > 0 })
	if s.calls.Load() != 0 {
		t.Fatal("default heartbeat enabled")
	}
	cfg := validConfig()
	if err := m.Save(cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.calls.Load() == 1 })
	c.bad.Store(true)
	if err := m.SendNow(context.Background()); err == nil {
		t.Fatal("unhealthy send accepted")
	}
	if s.calls.Load() != 1 {
		t.Fatal("sent unhealthy")
	}
}
func TestManagerReconfigureCancelsInFlightRequest(t *testing.T) {
	c := &testChecker{}
	s := &testSender{block: true, started: make(chan struct{}, 1)}
	m := NewManager(filepath.Join(privateDir(t), "heartbeat.json"), c, s)
	m.Start(context.Background())
	defer m.Stop()
	if err := m.Save(validConfig()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatal("no immediate send")
	}
	cfg := validConfig()
	cfg.Enabled = false
	done := make(chan error, 1)
	go func() { done <- m.Save(cfg) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reconfigure didn't cancel")
	}
	if m.Status().Config.Enabled {
		t.Fatal("not disabled")
	}
}
func TestScheduledCycleAndRetryLimit(t *testing.T) {
	c := &testChecker{}
	s := &testSender{}
	s.fail.Store(true)
	m := NewManager(filepath.Join(privateDir(t), "heartbeat.json"), c, s)
	m.retryWait = func(ctx context.Context, d time.Duration) bool { return ctx.Err() == nil }
	cfg := validConfig()
	m.cfg = cfg
	m.send(context.Background(), cfg)
	if s.calls.Load() != 4 {
		t.Fatal("want initial + 3 retries", s.calls.Load())
	}
	s.fail.Store(false)
	now := time.Now()
	m.nextCheck = now.Add(time.Hour)
	m.nextSend = now.Add(600 * time.Second)
	m.step(context.Background(), cfg, now.Add(599*time.Second))
	if s.calls.Load() != 4 {
		t.Fatal("early heartbeat")
	}
	m.step(context.Background(), cfg, now.Add(600*time.Second))
	if s.calls.Load() != 5 {
		t.Fatal("missing heartbeat")
	}
}

func TestSendCannotOutliveItsHeartbeatCycle(t *testing.T) {
	c := &testChecker{}
	s := &testSender{block: true, started: make(chan struct{}, 1)}
	m := NewManager(filepath.Join(privateDir(t), "heartbeat.json"), c, s)
	m.cycleContext = func(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		if d != 60*time.Second {
			t.Errorf("wrong cycle deadline %s", d)
		}
		return context.WithTimeout(ctx, 20*time.Millisecond)
	}
	cfg := validConfig()
	cfg.IntervalSeconds = 60
	start := time.Now()
	if m.send(context.Background(), cfg) == nil {
		t.Fatal("expected deadline")
	}
	if s.calls.Load() != 1 || time.Since(start) > time.Second {
		t.Fatal("continued expired cycle")
	}
}

func TestInvalidConfigDoesNotEnableSendingOrExposePrivateError(t *testing.T) {
	p := filepath.Join(privateDir(t), "heartbeat.json")
	if err := os.WriteFile(p, []byte(`{"secret":"private-invalid-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := &testChecker{}
	s := &testSender{}
	m := NewManager(p, c, s)
	m.Start(context.Background())
	defer m.Stop()
	waitFor(t, func() bool { return c.calls.Load() > 0 })
	status := m.Status()
	if status.Config.Enabled || status.ConfigError != "config_invalid" || status.Config.Secret != "" || s.calls.Load() != 0 {
		t.Fatal("unsafe fallback")
	}
}

func TestSaveBlankSecretPreservesExistingAndInvalidSaveDoesNotApply(t *testing.T) {
	p := filepath.Join(privateDir(t), "heartbeat.json")
	m := NewManager(p, &testChecker{}, &testSender{})
	cfg := validConfig()
	if m.Save(cfg) != nil {
		t.Fatal("initial save")
	}
	cfg.Secret = ""
	cfg.Name = "updated"
	if m.Save(cfg) != nil {
		t.Fatal("blank secret save")
	}
	loaded, err := LoadConfig(p)
	if err != nil || loaded.Secret != validConfig().Secret || loaded.Name != "updated" {
		t.Fatal("secret was not preserved")
	}
	before, _ := os.ReadFile(p)
	cfg.IntervalSeconds = 1
	if m.Save(cfg) == nil {
		t.Fatal("invalid save accepted")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) || m.Status().Config.IntervalSeconds != 600 || m.Status().Config.Secret != "" || !m.Status().SecretConfigured {
		t.Fatal("invalid save changed effective config")
	}
}
