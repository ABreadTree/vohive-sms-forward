package modem

import (
	"context"
	"errors"
	"testing"
)

type fakeDataSwitch struct {
	enabled   bool
	supported bool
	err       error
	sets      int
}

func (f *fakeDataSwitch) DataEnabled() (bool, bool, error) { return f.enabled, f.supported, f.err }
func (f *fakeDataSwitch) SetDataEnabled(v bool) error      { f.sets++; f.enabled = v; return nil }

func TestEnsureDataOffPendingWithoutDongle(t *testing.T) {
	f := &fakeDataSwitch{err: errors.New("dongle not found on USB")}
	state, err := EnsureDataOff(context.Background(), f)
	if err != nil || state != DataGuardPending || f.sets != 0 {
		t.Fatalf("state=%s err=%v sets=%d", state, err, f.sets)
	}
}
func TestEnsureDataOffReportsQueryError(t *testing.T) {
	f := &fakeDataSwitch{err: errors.New("AT query timed out")}
	state, err := EnsureDataOff(context.Background(), f)
	if state != DataGuardError || err == nil || f.sets != 0 {
		t.Fatalf("state=%s err=%v sets=%d", state, err, f.sets)
	}
}
func TestEnsureDataOffNoopWhenOff(t *testing.T) {
	f := &fakeDataSwitch{supported: true}
	state, err := EnsureDataOff(context.Background(), f)
	if state != DataGuardOff || err != nil || f.sets != 0 {
		t.Fatalf("state=%s err=%v sets=%d", state, err, f.sets)
	}
}
func TestEnsureDataOffRechecksAfterSwitch(t *testing.T) {
	f := &fakeDataSwitch{enabled: true, supported: true}
	state, err := EnsureDataOff(context.Background(), f)
	if state != DataGuardOff || err != nil || f.sets != 1 {
		t.Fatalf("state=%s err=%v sets=%d", state, err, f.sets)
	}
}
func TestEnsureDataOffUnsupportedIsError(t *testing.T) {
	f := &fakeDataSwitch{enabled: true}
	state, err := EnsureDataOff(context.Background(), f)
	if state != DataGuardError || err == nil || f.sets != 0 {
		t.Fatalf("state=%s err=%v sets=%d", state, err, f.sets)
	}
}
