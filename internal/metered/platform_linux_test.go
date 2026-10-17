package metered

import "testing"

func TestLinuxReconcileSkipsPlatformFlagApplication(t *testing.T) {
	// No traffic/store are needed: unsupported platforms must return before
	// looking at the link or attempting a system command, even as root.
	c := &Controller{}
	for i := 0; i < 10; i++ {
		c.reconcileLocked()
	}
	if c.err() != "unsupported_platform" || c.attempt.n != 0 {
		t.Fatalf("error=%q attempts=%d", c.err(), c.attempt.n)
	}
}
