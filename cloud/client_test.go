package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func clientAt(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, err := newClient("unit-secret-token", s.URL, s.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCreatePrivateServerPreservesBootstrapWithoutPublicAddress(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v2/projects/project/servers" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("Authorization") != "Bearer unit-secret-token" {
			t.Error("credential not supplied")
		}
		if b["user_data"] != "Ym9vdHN0cmFw" {
			t.Error("bootstrap changed or encoded twice")
		}
		a, ok := b["addresses"].([]any)
		if !ok || len(a) != 0 {
			t.Errorf("private node needs explicit empty addresses: %#v", b["addresses"])
		}
		if _, ok := b["keypairs"]; ok {
			t.Error("keypairs conflicts with user_data")
		}
		if _, ok := b["recipe"]; ok {
			t.Error("recipe conflicts with user_data")
		}
		f := b["flavor"].(map[string]any)
		if f["vcpus"] != float64(2) || f["ram"] != float64(4) {
			t.Errorf("wrong flavor %#v", f)
		}
		d := b["storages"].([]any)[0].(map[string]any)
		if d["size"] != float64(20) || d["bootable"] != true || d["storage_type"] != "volume" {
			t.Errorf("wrong storage %#v", d)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		fmt.Fprint(w, `{"result":{"id":"created-server"}}`)
	})
	id, err := c.CreateServer(context.Background(), "project", CreateRequest{Name: "node", ImageID: "image", VCPUs: 2, RAMGB: 4, BootGB: 20, UserDataBase64: "Ym9vdHN0cmFw"})
	if err != nil || id != "created-server" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestCreatePublicServerUsesOnlyReservedAddress(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		a := b["addresses"].([]any)
		if len(a) != 1 {
			t.Fatalf("addresses=%#v", a)
		}
		v := a[0].(map[string]any)
		if v["address_id"] != "reserved" || v["external"] != true || v["version"] != float64(4) {
			t.Errorf("wrong reserved binding %#v", v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		fmt.Fprint(w, `{"result":{"id":"s"}}`)
	})
	_, err := c.CreateServer(context.Background(), "p", CreateRequest{Name: "n", ImageID: "i", VCPUs: 2, RAMGB: 4, BootGB: 20, PublicIPID: "reserved", UserDataBase64: "data"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeleteServerPreservesAddressesAndUnlistedDisks(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/v2/servers/server" {
			t.Error("wrong target")
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		v, ok := b["delete_volumes"].([]any)
		if !ok || len(v) != 1 || v[0] != "boot-only" {
			t.Errorf("unsafe volume deletion %#v", b)
		}
		a, ok := b["delete_addresses"].([]any)
		if !ok || len(a) != 0 || b["clear_fstab"] != false {
			t.Errorf("unsafe options %#v", b)
		}
		w.WriteHeader(204)
	})
	if err := c.DeleteServer(context.Background(), "server", []string{"boot-only"}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRejectsUnspecifiedBootOwnership(t *testing.T) {
	called := false
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) })
	if err := c.DeleteServer(context.Background(), "s", nil); err == nil {
		t.Fatal("empty boot ownership accepted")
	}
	if called {
		t.Fatal("unsafe request reached provider")
	}
}

func TestWritesAreNotRetriedAndCredentialIsRedacted(t *testing.T) {
	calls := 0
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		fmt.Fprint(w, `{"message":"unit-secret-token"}`)
	})
	_, err := c.CreateServer(context.Background(), "p", CreateRequest{})
	if err == nil || strings.Contains(err.Error(), "unit-secret-token") {
		t.Fatalf("unsafe error %v", err)
	}
	if calls != 1 {
		t.Fatalf("write retried %d times", calls)
	}
}

func TestNotFoundIsClassified(t *testing.T) {
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"message":"Not found"}`)
	})
	_, err := c.GetServer(context.Background(), "gone")
	if !IsNotFound(err) {
		t.Fatalf("expected not found: %v", err)
	}
}

func TestListServersReadsEveryPage(t *testing.T) {
	pages := 0
	c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit != 100 {
			t.Fatalf("page size %d", limit)
		}
		n := 100
		if off == 100 {
			n = 1
		} else if off != 0 {
			t.Fatalf("wrong offset %d", off)
		}
		items := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, map[string]any{"id": fmt.Sprint(off + i), "name": "node", "project": "p", "status": "ACTIVE", "created_in": "2026-11-12T00:00:00Z", "guest_agent": true, "rescue_mode": "NO", "switch_status": "ON"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 101, "result": items})
	})
	got, err := c.ListServers(context.Background(), "p")
	if err != nil || len(got) != 101 || pages != 2 {
		t.Fatalf("servers=%d pages=%d err=%v", len(got), pages, err)
	}
}
