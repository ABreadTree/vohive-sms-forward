package modem

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// HealthStatus deliberately excludes SIM/phone/device identifiers.
type HealthStatus struct{ ATOK, SIMReady, Registered, DataOff bool }

func registeredResponse(response string) bool {
	for _, prefix := range []string{"+CEREG:", "+CGREG:", "+CREG:"} {
		v := firstField(response, prefix)
		if v == "" {
			continue
		}
		fields := strings.Split(v, ",")
		stat := fields[0]
		if len(fields) > 1 {
			stat = fields[1]
		}
		n, err := strconv.Atoi(strings.TrimSpace(stat))
		return err == nil && (n == 1 || n == 5)
	}
	return false
}

// Health serializes read-only AT queries, with cancellable lock acquisition.
// Each command has a short timeout; cancellation never leaves a probe goroutine.
func (m *Modem) Health(ctx context.Context) (HealthStatus, error) {
	var h HealthStatus
	for !m.mu.TryLock() {
		select {
		case <-ctx.Done():
			return h, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer m.mu.Unlock()
	query := func(command string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		timeout := 3 * time.Second
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
			timeout = time.Until(deadline)
		}
		response, err := m.cmdLocked(command, timeout)
		if err != nil || ctx.Err() != nil || !strings.Contains(response, "OK\r\n") {
			return "", errors.New("modem health query failed")
		}
		return response, nil
	}
	if _, err := query("AT"); err != nil {
		return h, err
	}
	h.ATOK = true
	r, err := query("AT+CPIN?")
	if err != nil {
		return h, err
	}
	h.SIMReady = firstField(r, "+CPIN:") == "READY"
	for _, cmd := range []string{"AT+CEREG?", "AT+CGREG?", "AT+CREG?"} {
		r, err = query(cmd)
		if ctx.Err() != nil {
			return h, ctx.Err()
		}
		if err == nil && registeredResponse(r) {
			h.Registered = true
			break
		}
	}
	r, err = query(`AT+QCFG="qcautoconnect"`)
	if err != nil {
		return h, err
	}
	f := strings.Split(firstField(r, "+QCFG:"), ",")
	h.DataOff = len(f) == 2 && strings.TrimSpace(f[0]) == `"qcautoconnect"` && strings.TrimSpace(f[1]) == "0"
	return h, ctx.Err()
}
