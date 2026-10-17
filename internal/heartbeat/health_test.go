package heartbeat

import (
	"context"
	"errors"
	"github.com/ABreadTree/vohive-sms-forward/internal/archive"
	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
	"testing"
	"time"
)

func TestCheckerRequiresEveryFreshHealthyComponent(t *testing.T) {
	now := time.Now()
	good := modem.HealthStatus{ATOK: true, SIMReady: true, Registered: true, DataOff: true}
	for _, kind := range []string{"ok", "at", "sim", "registration", "data", "archive_stale", "archive_error", "database", "modem_error", "context"} {
		t.Run(kind, func(t *testing.T) {
			m := good
			a := archive.HealthStatus{LastSuccess: now, LastAttempt: now}
			var dbErr, modemErr error
			switch kind {
			case "at":
				m.ATOK = false
			case "sim":
				m.SIMReady = false
			case "registration":
				m.Registered = false
			case "data":
				m.DataOff = false
			case "archive_stale":
				a.LastSuccess = now.Add(-151 * time.Second)
			case "archive_error":
				a.LastErrorCategory = "sync_failed"
			case "database":
				dbErr = errors.New("private database path")
			case "modem_error":
				modemErr = errors.New("private hardware error")
			}
			c := Checker{Modem: func(context.Context) (modem.HealthStatus, error) { return m, modemErr }, Archive: func() archive.HealthStatus { return a }, Database: func(context.Context) error { return dbErr }, Guard: func() bool { return true }, Now: func() time.Time { return now }}
			ctx := context.Background()
			if kind == "context" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			r := c.Check(ctx)
			if r.Healthy != (kind == "ok") {
				t.Fatalf("%s: %+v", kind, r)
			}
		})
	}
}
func TestReportNeverHealthyWhenStale(t *testing.T) {
	r := Report{Healthy: true, CheckedAt: time.Now().Add(-151 * time.Second)}
	if r.Fresh(time.Now()) {
		t.Fatal("stale report accepted")
	}
}

func TestCheckerRevalidatesArchiveAtCompletion(t *testing.T) {
	now := time.Now()
	a := archive.HealthStatus{LastSuccess: now.Add(-149 * time.Second)}
	c := Checker{Modem: func(context.Context) (modem.HealthStatus, error) {
		return modem.HealthStatus{ATOK: true, SIMReady: true, Registered: true, DataOff: true}, nil
	}, Archive: func() archive.HealthStatus { return a }, Database: func(context.Context) error { now = now.Add(2 * time.Second); return nil }, Guard: func() bool { return true }, Now: func() time.Time { return now }}
	r := c.Check(context.Background())
	if r.Healthy || r.Checks["archive"] {
		t.Fatal("expired archive accepted", r)
	}
	if r.CheckedAt != now {
		t.Fatal("completion timestamp not final")
	}
}
