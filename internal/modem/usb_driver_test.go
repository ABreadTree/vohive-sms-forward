package modem

import (
	"errors"
	"testing"
)

func TestAutoDetachOnlyOnLinuxAndPropagatesFailure(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		calls := 0
		failed := errors.New("detach unavailable")
		err := enableATDriverDetach(goos, func(on bool) error {
			calls++
			if !on {
				t.Fatal("auto-detach disabled")
			}
			return failed
		})
		if goos == "linux" {
			if calls != 1 || !errors.Is(err, failed) {
				t.Fatalf("linux calls=%d err=%v", calls, err)
			}
		} else if calls != 0 || err != nil {
			t.Fatalf("darwin calls=%d err=%v", calls, err)
		}
	}
}
