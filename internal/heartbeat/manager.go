package heartbeat

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

type HealthChecker interface{ Check(context.Context) Report }
type Sender interface {
	Send(context.Context, Config, Report, Envelope) error
}
type Status struct {
	Config           Config    `json:"config"`
	SecretConfigured bool      `json:"secret_configured"`
	Report           Report    `json:"report"`
	LastAttempt      time.Time `json:"last_attempt"`
	LastSuccess      time.Time `json:"last_success"`
	Error            string    `json:"error,omitempty"`
	ConfigError      string    `json:"config_error,omitempty"`
}
type command struct {
	ctx        context.Context
	send       bool
	generation uint64
	reply      chan commandResult
}
type commandResult struct {
	report Report
	err    error
}

// Manager has one serial worker. Reconfiguration cancels and joins it before
// starting another generation; no old request can outlive a successful Save.
type Manager struct {
	opMu                sync.Mutex
	mu                  sync.RWMutex
	path                string
	checker             HealthChecker
	sender              Sender
	cfg                 Config
	status              Status
	parent              context.Context
	running             context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	generation          uint64
	commands            chan command
	nextCheck, nextSend time.Time // worker-owned
	retryWait           func(context.Context, time.Duration) bool
	cycleContext        func(context.Context, time.Duration) (context.Context, context.CancelFunc)
}

func NewManager(path string, checker HealthChecker, sender Sender) *Manager {
	return &Manager{path: path, checker: checker, sender: sender, cfg: Config{IntervalSeconds: 600}, commands: make(chan command), cycleContext: context.WithTimeout, retryWait: func(ctx context.Context, d time.Duration) bool {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}}
}
func (m *Manager) Start(parent context.Context) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.parent != nil {
		return
	}
	m.parent = parent
	cfg, err := LoadConfig(m.path)
	if err == nil {
		m.cfg = cfg
	} else if !os.IsNotExist(err) {
		m.status.ConfigError = "config_invalid"
	}
	m.startLocked()
}
func (m *Manager) startLocked() {
	ctx, cancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	m.mu.Lock()
	m.running = ctx
	m.cancel = cancel
	m.done = done
	m.generation++
	generation := m.generation
	cfg := m.cfg
	m.mu.Unlock()
	m.nextCheck = time.Time{}
	m.nextSend = time.Time{}
	go func() {
		defer close(done)
		m.step(ctx, cfg, time.Now())
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				m.step(ctx, cfg, now)
			case req := <-m.commands:
				if req.generation != generation {
					req.reply <- commandResult{err: ErrConfig}
					continue
				}
				requestCtx, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(req.ctx, cancel)
				var result commandResult
				if req.ctx.Err() != nil {
					result.err = req.ctx.Err()
				} else if req.send {
					result.err = m.send(requestCtx, cfg)
					result.report = m.Status().Report
				} else {
					result.report = m.check(requestCtx)
				}
				stop()
				cancel()
				req.reply <- result
			}
		}
	}()
}
func (m *Manager) stopLocked() {
	m.mu.RLock()
	cancel, done := m.cancel, m.done
	m.mu.RUnlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (m *Manager) Stop() { m.opMu.Lock(); defer m.opMu.Unlock(); m.stopLocked() }
func (m *Manager) Save(cfg Config) error {
	if cfg.IntervalSeconds == 0 {
		cfg.IntervalSeconds = 600
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	old := m.cfg
	m.mu.RUnlock()
	if cfg.Secret == "" {
		cfg.Secret = old.Secret
	}
	if cfg.Validate() != nil {
		return ErrConfig
	}
	// Persist first; cancellation below joins any request using the old config.
	err := SaveConfig(m.path, cfg)
	if err != nil && !errors.Is(err, ErrDurability) {
		return ErrConfig
	}
	m.stopLocked()
	m.mu.Lock()
	m.cfg = cfg
	m.status.ConfigError = ""
	m.status.Error = ""
	m.status.LastAttempt = time.Time{}
	m.status.LastSuccess = time.Time{}
	if err != nil {
		m.status.ConfigError = "durability_unconfirmed"
	}
	m.mu.Unlock()
	if m.parent != nil {
		m.startLocked()
	}
	return nil
}
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.status
	s.Config = m.cfg
	s.SecretConfigured = m.cfg.Secret != ""
	s.Config.Secret = ""
	s.Report.Checks = cloneChecks(s.Report.Checks)
	s.Report.Healthy = healthy(s.Report, time.Now())
	return s
}
func cloneChecks(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
func (m *Manager) check(ctx context.Context) Report {
	checkCtx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	r := m.checker.Check(checkCtx)
	if checkCtx.Err() != nil {
		r.Healthy = false
	}
	m.mu.Lock()
	m.status.Report = r
	m.mu.Unlock()
	return r
}
func (m *Manager) step(ctx context.Context, cfg Config, now time.Time) {
	if ctx.Err() != nil {
		return
	}
	if cfg.Enabled && !now.Before(m.nextSend) {
		m.send(ctx, cfg)
		m.nextSend = now.Add(time.Duration(cfg.IntervalSeconds) * time.Second)
		m.nextCheck = time.Now().Add(time.Minute)
	} else if !now.Before(m.nextCheck) {
		m.check(ctx)
		m.nextCheck = now.Add(time.Minute)
	}
}
func (m *Manager) send(ctx context.Context, cfg Config) error {
	if !cfg.Enabled {
		return ErrConfig
	}
	ctx, cancel := m.cycleContext(ctx, time.Duration(cfg.IntervalSeconds)*time.Second)
	defer cancel()
	r := m.check(ctx)
	if !healthy(r, time.Now()) {
		m.setError("self_check_failed")
		return ErrUnhealthy
	}
	e, err := NewEnvelope(cfg, r, time.Now())
	if err != nil {
		m.setError("delivery_failed")
		return err
	}
	for i := 0; i < 4; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i > 0 {
			if !m.retryWait(ctx, []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}[i-1]) {
				return ctx.Err()
			}
			r = m.check(ctx)
			if !healthy(r, time.Now()) {
				m.setError("self_check_failed")
				return ErrUnhealthy
			}
		}
		m.mu.Lock()
		m.status.LastAttempt = time.Now()
		m.mu.Unlock()
		err = m.sender.Send(ctx, cfg, r, e)
		if err != nil {
			m.setError("delivery_failed")
		}
		if err == nil {
			m.mu.Lock()
			m.status.LastSuccess = time.Now()
			m.status.Error = ""
			m.mu.Unlock()
			return nil
		}
	}
	m.setError("delivery_failed")
	return ErrSend
}
func (m *Manager) setError(category string) { m.mu.Lock(); m.status.Error = category; m.mu.Unlock() }
func (m *Manager) request(ctx context.Context, send bool) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	m.mu.RLock()
	running, gen := m.running, m.generation
	m.mu.RUnlock()
	if running == nil {
		return Report{}, ErrConfig
	}
	req := command{ctx: ctx, send: send, generation: gen, reply: make(chan commandResult, 1)}
	select {
	case m.commands <- req:
	case <-running.Done():
		return Report{}, ErrConfig
	case <-ctx.Done():
		return Report{}, ctx.Err()
	}
	select {
	case r := <-req.reply:
		return r.report, r.err
	case <-running.Done():
		return Report{}, ErrConfig
	case <-ctx.Done():
		return Report{}, ctx.Err()
	}
}
func (m *Manager) CheckNow(ctx context.Context) (Report, error) { return m.request(ctx, false) }
func (m *Manager) SendNow(ctx context.Context) error            { _, err := m.request(ctx, true); return err }
