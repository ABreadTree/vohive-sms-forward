package modem

import (
	"context"
	"testing"
	"time"
)

func TestRegistrationHealthParsing(t *testing.T) {
	for _, tc := range []struct {
		response string
		want     bool
	}{
		{"+CEREG: 0,1\r\nOK\r\n", true}, {"+CEREG: 2,5,\"ABCD\",\"1234\",7\r\nOK\r\n", true},
		{"+CREG: 1\r\nOK\r\n", true}, {"+CEREG: 0,2\r\nOK\r\n", false}, {"OK\r\n", false},
		{"+CEREG: 0,11\r\nOK\r\n", false}, {"+CEREG: 0,1junk\r\nOK\r\n", false},
	} {
		if got := registeredResponse(tc.response); got != tc.want {
			t.Errorf("%q got %v", tc.response, got)
		}
	}
}
func TestHealthHonorsContextWhileATBusy(t *testing.T) {
	m := New()
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Health(ctx)
	if err == nil || time.Since(start) > time.Second {
		t.Fatal("busy health should cancel promptly", err)
	}
}
