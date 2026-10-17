package store

import (
	"context"
	"testing"
)

func TestHealthChecksDatabaseAndCancellation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.Health(ctx) == nil {
		t.Fatal("ignored cancellation")
	}
	s.Close()
	if s.Health(context.Background()) == nil {
		t.Fatal("closed database healthy")
	}
}
