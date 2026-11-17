package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStorageRequestContracts(t *testing.T) {
	cases := []struct {
		name, method, path string
		body               map[string]any
		code               int
		call               func(*Client) error
	}{
		{"create", "POST", "/v2/projects/p/volumes", map[string]any{"name": "csi-test", "size": float64(10), "autorename": false}, 201, func(c *Client) error {
			id, err := c.CreateVolume(context.Background(), "p", CreateVolumeRequest{Name: "csi-test", SizeGB: 10})
			if err == nil && id != "v" {
				return fmt.Errorf("unexpected created ID %q", id)
			}
			return err
		}},
		{"attach", "POST", "/v2/volumes/v/attach", map[string]any{"server_id": "s"}, 202, func(c *Client) error { return c.AttachVolume(context.Background(), "v", "s") }},
		{"hot detach", "POST", "/v2/volumes/v/detach", map[string]any{"force": true, "clear_fstab": false}, 202, func(c *Client) error { return c.DetachVolume(context.Background(), "v", DetachOptions{Hot: true}) }},
		{"stopped detach", "POST", "/v2/volumes/v/detach", map[string]any{"force": false, "clear_fstab": false}, 202, func(c *Client) error { return c.DetachVolume(context.Background(), "v", DetachOptions{}) }},
		{"extend", "POST", "/v2/volumes/v/extend", map[string]any{"new_size": float64(12), "reboot_server": false}, 202, func(c *Client) error { return c.ExtendVolume(context.Background(), "v", 12) }},
		{"delete", "DELETE", "/v2/volumes/v", map[string]any{"force": false, "clear_fstab": false}, 204, func(c *Client) error { return c.DeleteVolume(context.Background(), "v") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("target %s %s", r.Method, r.URL.Path)
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, tc.body) {
					t.Errorf("unsafe body: got %#v want %#v", got, tc.body)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.code)
				if tc.code == 201 {
					fmt.Fprint(w, `{"result":{"id":"v"}}`)
				} else if tc.code != 204 {
					fmt.Fprint(w, `{}`)
				}
			})
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestStorageDoesNotRetryWrites(t *testing.T) {
	ops := map[string]func(*Client) error{
		"create": func(c *Client) error {
			_, e := c.CreateVolume(context.Background(), "p", CreateVolumeRequest{Name: "n", SizeGB: 10})
			return e
		},
		"attach": func(c *Client) error { return c.AttachVolume(context.Background(), "v", "s") },
		"detach": func(c *Client) error { return c.DetachVolume(context.Background(), "v", DetachOptions{Hot: true}) },
		"extend": func(c *Client) error { return c.ExtendVolume(context.Background(), "v", 12) },
		"delete": func(c *Client) error { return c.DeleteVolume(context.Background(), "v") },
	}
	for name, op := range ops {
		for _, status := range []int{422, 500} {
			t.Run(fmt.Sprintf("%s/%d", name, status), func(t *testing.T) {
				calls := 0
				c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					fmt.Fprint(w, `{"message":"reconcile before retry"}`)
				})
				err := op(c)
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != status || calls != 1 {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestStoragePaginationAndCancellation(t *testing.T) {
	calls := 0
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v2/projects/p/volumes" || r.URL.Query().Get("limit") != "100" {
			t.Errorf("unexpected list request %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 0:
			if r.URL.Query().Get("offset") != "0" {
				t.Error("first offset")
			}
			page := make([]map[string]any, 100)
			for i := range page {
				page[i] = map[string]any{"id": fmt.Sprintf("v%d", i), "name": "data", "size": 10, "status": "AVAILABLE", "bootable": false, "created_in": "2026-11-13T00:00:00Z", "description": ""}
			}
			page[0] = map[string]any{"id": "a", "name": "first", "size": 10, "status": "IN_USE", "bootable": true, "created_in": "2026-11-13T00:00:00Z", "description": "", "attached_to_server": map[string]any{"id": "s", "device": "/dev/sdb"}}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 101, "result": page})
		case 1:
			if r.URL.Query().Get("offset") != "100" {
				t.Error("second offset")
			}
			fmt.Fprint(w, `{"count":101,"result":[{"id":"b","name":"second","size":12,"status":"AVAILABLE","bootable":false,"created_in":"2026-11-13T00:00:00Z","description":""}]}`)
		default:
			t.Error("unexpected additional page")
		}
		calls++
	})
	got, err := c.ListVolumes(context.Background(), "p")
	if err != nil || len(got) != 101 || got[0].ID != "a" || got[100].ID != "b" || got[0].SizeGB != 10 || !got[0].Bootable || got[0].AttachedServerID != "s" {
		t.Fatalf("volumes=%+v err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ListVolumes(ctx, "p")
	if !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("cancel calls=%d err=%v", calls, err)
	}
	t.Run("duplicate pages", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		n := 0
		d := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
			n++
			w.Header().Set("Content-Type", "application/json")
			page := make([]map[string]any, 100)
			for i := range page {
				page[i] = map[string]any{"id": fmt.Sprintf("v%d", i), "name": "data", "size": 10, "status": "AVAILABLE", "bootable": false}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 300, "result": page})
		})
		if _, e := d.ListVolumes(ctx, "p"); e == nil || n > 2 {
			t.Fatalf("nonprogress list accepted: calls=%d err=%v", n, e)
		}
	})
}

func TestStorageInFlightCancellationSingleSend(t *testing.T) {
	var calls atomic.Int32
	var once sync.Once
	entered := make(chan struct{})
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(entered) })
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.AttachVolume(ctx, "v", "s") }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("request failed before reaching provider: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached provider")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("sent %d requests", calls.Load())
	}
}

func TestStorageRedactsToken(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(422)
		fmt.Fprint(w, `{"message":"unit-secret-token"}`)
	})
	err := c.AttachVolume(context.Background(), "v", "s")
	if err == nil || strings.Contains(err.Error(), "unit-secret-token") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("unsafe error: %v", err)
	}
	d := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		fmt.Fprint(w, `{"message":"Object not found"}`)
	})
	if err := d.DeleteVolume(context.Background(), "v"); !IsNotFound(err) {
		t.Fatalf("404 not typed: %v", err)
	}
}

func TestStoragePreservesSwitchStatus(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "servers") {
			fmt.Fprint(w, `{"result":{"id":"s","name":"n","project":"p","status":"STOPPED","switch_status":"OFF"}}`)
		} else {
			fmt.Fprint(w, `{"result":{"id":"v","name":"data","status":"IN_USE","size":12,"bootable":false,"attached_to_server":{"id":"s","device":"/dev/sdc"}}}`)
		}
	})
	s, err := c.GetServer(context.Background(), "s")
	if err != nil || s.SwitchStatus != "OFF" {
		t.Fatalf("server=%+v err=%v", s, err)
	}
	v, err := c.GetVolume(context.Background(), "v")
	if err != nil || v.SizeGB != 12 || v.Device != "/dev/sdc" {
		t.Fatalf("volume=%+v err=%v", v, err)
	}
}

func TestStorageRejectsInvalidRequests(t *testing.T) {
	calls := 0
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	cases := []func() error{
		func() error {
			_, e := c.CreateVolume(context.Background(), "", CreateVolumeRequest{Name: "n", SizeGB: 10})
			return e
		},
		func() error {
			_, e := c.CreateVolume(context.Background(), "p", CreateVolumeRequest{Name: " ", SizeGB: 10})
			return e
		},
		func() error {
			_, e := c.CreateVolume(context.Background(), "p", CreateVolumeRequest{Name: "n", SizeGB: 0})
			return e
		},
		func() error { return c.AttachVolume(context.Background(), "", "s") },
		func() error { return c.AttachVolume(context.Background(), "v", "") },
		func() error { return c.DetachVolume(context.Background(), "", DetachOptions{}) },
		func() error { return c.ExtendVolume(context.Background(), "v", 0) },
		func() error { return c.DeleteVolume(context.Background(), "") },
	}
	for i, call := range cases {
		if err := call(); err == nil {
			t.Errorf("invalid request %d accepted", i)
		}
	}
	if calls != 0 {
		t.Fatalf("%d invalid requests reached provider", calls)
	}
}
