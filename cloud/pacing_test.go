package cloud

import (
	"context"
	"golang.org/x/time/rate"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPacingRetriesOnlyReadRequests(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			p := newPacedTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				status := 429
				if calls == 2 {
					status = 200
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"0"}}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
			}), rate.NewLimiter(rate.Inf, 1))
			req, _ := http.NewRequestWithContext(context.Background(), method, "https://api.clo.ru/v2/test", nil)
			response, e := p.RoundTrip(req)
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			expected := 1
			if method == http.MethodGet {
				expected = 2
			}
			if calls != expected {
				t.Fatalf("%s dispatched %d times; want %d", method, calls, expected)
			}
		})
	}
}
func TestPacingBudgetIsSharedAndCancellationPreventsDispatch(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	p := newPacedTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	}), rate.NewLimiter(rate.Every(time.Hour), 1))
	req, _ := http.NewRequest(http.MethodGet, "https://api.clo.ru/v2/test", nil)
	response, e := p.RoundTrip(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	if _, e = p.RoundTrip(req); e == nil {
		t.Fatal("shared request budget bypassed")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatal("canceled queued request reached provider")
	}
}
func TestRetryAfterStopsOtherRequestsWithoutRetryingWrite(t *testing.T) {
	calls := 0
	p := newPacedTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	}), rate.NewLimiter(rate.Inf, 1))
	req, _ := http.NewRequest(http.MethodPost, "https://api.clo.ru/v2/test", nil)
	response, e := p.RoundTrip(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://api.clo.ru/v2/test", nil)
	if _, e = p.RoundTrip(req); e == nil {
		t.Fatal("provider cooldown ignored")
	}
	if calls != 1 {
		t.Fatal("write repeated or cooldown request reached provider")
	}
}
