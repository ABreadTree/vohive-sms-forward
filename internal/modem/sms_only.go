package modem

import (
	"context"
	"errors"
	"strings"
	"time"
)

type DataSwitch interface {
	DataEnabled() (enabled, supported bool, err error)
	SetDataEnabled(enabled bool) error
}

type DataGuardState string

const (
	DataGuardOff     DataGuardState = "off"
	DataGuardPending DataGuardState = "pending"
	DataGuardError   DataGuardState = "error"
)

// EnsureDataOff verifies the modem setting, changes it only if necessary and
// verifies it again after a change. Pending never means verified off.
func EnsureDataOff(ctx context.Context, sw DataSwitch) (DataGuardState, error) {
	changed := false
	for {
		if err := ctx.Err(); err != nil {
			return DataGuardPending, err
		}
		enabled, supported, err := sw.DataEnabled()
		if err != nil {
			if absentDongle(err) {
				return DataGuardPending, nil
			}
			return DataGuardError, err
		}
		if !supported {
			return DataGuardError, errors.New("modem does not support data switch query")
		}
		if !enabled {
			return DataGuardOff, nil
		}
		if !changed {
			if err := sw.SetDataEnabled(false); err != nil {
				if absentDongle(err) {
					return DataGuardPending, nil
				}
				return DataGuardError, err
			}
			changed = true
			continue
		}
		// A rebooting modem can temporarily report its old setting. Bound the
		// wait by the caller's context; the main loop retries later if needed.
		select {
		case <-ctx.Done():
			return DataGuardPending, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func absentDongle(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "dongle not found") || strings.Contains(s, "no such device")
}
