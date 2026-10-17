// Package heartbeat implements local self-checks and authenticated healthy heartbeats.
package heartbeat

import (
	"context"
	"github.com/ABreadTree/vohive-sms-forward/internal/archive"
	"github.com/ABreadTree/vohive-sms-forward/internal/modem"
	"time"
)

const Freshness = 150 * time.Second
const CheckTimeout = 45 * time.Second

type Report struct {
	CheckedAt time.Time       `json:"checked_at"`
	Healthy   bool            `json:"healthy"`
	Checks    map[string]bool `json:"checks"`
}

func (r Report) Fresh(now time.Time) bool {
	age := now.Sub(r.CheckedAt)
	return !r.CheckedAt.IsZero() && age >= 0 && age <= Freshness
}

type Checker struct {
	Modem    func(context.Context) (modem.HealthStatus, error)
	Archive  func() archive.HealthStatus
	Database func(context.Context) error
	Guard    func() bool
	Now      func() time.Time
}

func (c Checker) Check(ctx context.Context) Report {
	now := c.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	h, err := c.Modem(ctx)
	dbOK := c.Database(ctx) == nil
	a := c.Archive()
	completed := now()
	age := completed.Sub(a.LastSuccess)
	r := Report{CheckedAt: completed, Checks: map[string]bool{
		"at": err == nil && h.ATOK, "sim": err == nil && h.SIMReady, "registration": err == nil && h.Registered,
		"data_off": err == nil && h.DataOff && c.Guard(), "archive": !a.LastSuccess.IsZero() && age >= 0 && age <= Freshness && a.LastErrorCategory == "",
		"database": dbOK, "timely": ctx.Err() == nil && now().Sub(start) <= CheckTimeout,
	}}
	r.Healthy = true
	for _, v := range r.Checks {
		r.Healthy = r.Healthy && v
	}
	return r
}
