package cloud

import (
	"context"
	"golang.org/x/time/rate"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// One transport is shared by all fresh credential clients in a CSI controller.
// Requests are paced at HTTP level, including SDK pagination. Only GET may be
// retried; a rejected or uncertain native mutation always returns to its owner.
type pacedTransport struct {
	base  http.RoundTripper
	limit *rate.Limiter
	mu    sync.Mutex
	until time.Time
}

func newPacedTransport(base http.RoundTripper, limit *rate.Limiter) *pacedTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &pacedTransport{base: base, limit: limit}
}
func (p *pacedTransport) wait(ctx context.Context) error {
	for {
		p.mu.Lock()
		until := p.until
		p.mu.Unlock()
		if delay := time.Until(until); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if e := p.limit.Wait(ctx); e != nil {
			return e
		}
		p.mu.Lock()
		paused := time.Now().Before(p.until)
		p.mu.Unlock()
		if !paused {
			return nil
		}
	}
}
func retryDelay(value string, now time.Time) time.Duration {
	if seconds, e := strconv.ParseInt(value, 10, 64); e == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if date, e := http.ParseTime(value); e == nil {
		if date.After(now) {
			return date.Sub(now)
		}
		return 0
	}
	return 5 * time.Second
}
func (p *pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if e := p.wait(req.Context()); e != nil {
			return nil, e
		}
		response, e := p.base.RoundTrip(req)
		if e != nil || response.StatusCode != http.StatusTooManyRequests {
			return response, e
		}
		now := time.Now()
		until := now.Add(retryDelay(response.Header.Get("Retry-After"), now))
		p.mu.Lock()
		if until.After(p.until) {
			p.until = until
		}
		p.mu.Unlock()
		if req.Method != http.MethodGet || attempt >= 3 {
			return response, nil
		}
		if response.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
		}
	}
}
