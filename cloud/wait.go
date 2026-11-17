package cloud

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func Wait(ctx context.Context, interval time.Duration, fetch func(context.Context) (string, error), ready func(string) bool) error {
	if interval <= 0 {
		return fmt.Errorf("poll interval must be positive")
	}
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		status, e := fetch(ctx)
		if e != nil {
			return e
		}
		status = strings.ToUpper(strings.ReplaceAll(status, "-", "_"))
		if ready(status) {
			return nil
		}
		switch status {
		case "ERROR", "UNKNOWN", "BLOCKED":
			return fmt.Errorf("CLO resource entered terminal state %s", status)
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
