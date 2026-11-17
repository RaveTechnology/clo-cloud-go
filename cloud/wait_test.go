package cloud

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitTracksAsyncStates(t *testing.T) {
	states := []string{"BUILDING", "STARTING", "ACTIVE"}
	i := 0
	err := Wait(context.Background(), time.Millisecond, func(context.Context) (string, error) { s := states[i]; i++; return s, nil }, func(s string) bool { return s == "ACTIVE" })
	if err != nil || i != 3 {
		t.Fatalf("polls=%d err=%v", i, err)
	}
}
func TestWaitStopsAtTerminalError(t *testing.T) {
	calls := 0
	err := Wait(context.Background(), time.Millisecond, func(context.Context) (string, error) { calls++; return "ERROR", nil }, func(string) bool { return false })
	if err == nil || calls != 1 {
		t.Fatalf("polls=%d err=%v", calls, err)
	}
}
func TestWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := Wait(ctx, time.Millisecond, func(context.Context) (string, error) { calls++; return "BUILDING", nil }, func(string) bool { return false })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("polls=%d err=%v", calls, err)
	}
}
