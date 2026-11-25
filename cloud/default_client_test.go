package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Exercise New, the constructor used by the machine driver, rather than the
// separately configured file-backed storage client or a test-only client.
func defaultClientTransport(t *testing.T, f roundTripFunc) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func defaultClientResponse(r *http.Request, status int, retry, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{
		"Content-Type": {"application/json"}, "Retry-After": {retry},
	}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestDefaultClientRetriesThrottledVolumeRead(t *testing.T) {
	calls := 0
	defaultClientTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/volumes/volume/detail" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		calls++
		if calls == 1 {
			return defaultClientResponse(r, 429, "0", `{"message":"limited"}`), nil
		}
		return defaultClientResponse(r, 200, "0", `{"result":{"id":"volume","status":"IN-USE","bootable":true,"attached_to":{"id":"server"}}}`), nil
	})
	c, err := New("unit-token")
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.GetVolume(context.Background(), "volume")
	if err != nil || v.ID != "volume" || !v.Bootable || calls != 2 {
		t.Fatalf("transient volume read failed: volume=%+v calls=%d err=%v", v, calls, err)
	}
}

func TestDefaultClientsShareProviderCooldown(t *testing.T) {
	calls := 0
	defaultClientTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return defaultClientResponse(r, 429, "1", `{"message":"limited"}`), nil
	})
	first, _ := New("first-unit-token")
	if err := first.Power(context.Background(), "server", "start"); err == nil || calls != 1 {
		t.Fatalf("write must return its first rejection: calls=%d err=%v", calls, err)
	}
	second, _ := New("second-unit-token")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := second.GetVolume(ctx, "volume")
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("new client bypassed the provider cooldown: calls=%d err=%v", calls, err)
	}
}

func TestDefaultClientNeverRetriesRejectedMutations(t *testing.T) {
	for _, action := range []string{"start", "delete"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			defaultClientTransport(t, func(r *http.Request) (*http.Response, error) {
				calls++
				return defaultClientResponse(r, 429, "0", `{"message":"limited"}`), nil
			})
			c, _ := New("unit-token")
			var err error
			if action == "delete" {
				err = c.DeleteServer(context.Background(), "server", []string{"boot"})
			} else {
				err = c.Power(context.Background(), "server", action)
			}
			if err == nil || calls != 1 {
				t.Fatalf("mutation replayed: calls=%d err=%v", calls, err)
			}
		})
	}
}
