package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func TestInventoriesRequireCompleteStableBoundedCount(t *testing.T) {
	for _, kind := range []string{"servers", "volumes"} {
		for _, scenario := range []string{"short", "missing-result", "missing-count", "negative", "too-large", "changed-count", "short-second-page", "repeated-id", "empty-id"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				calls := 0
				c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
					count, size := 2, 1
					if scenario == "changed-count" || scenario == "short-second-page" || scenario == "repeated-id" {
						count, size = 102, 100
						if offset > 0 {
							size = 1
							if scenario == "changed-count" {
								count = 101
							}
						}
					}
					if scenario == "negative" {
						count, size = -1, 0
					}
					if scenario == "too-large" {
						count, size = 100001, 0
					}
					items := make([]map[string]any, size)
					for i := range items {
						id := fmt.Sprint(offset + i)
						if scenario == "repeated-id" && offset > 0 {
							id = "0"
						}
						if scenario == "empty-id" {
							id = ""
							count = 1
						}
						items[i] = map[string]any{"id": id, "name": "maintenance", "project": "p", "status": "ACTIVE", "size": 10, "bootable": false}
					}
					body := map[string]any{"count": count, "result": items}
					if scenario == "missing-result" {
						delete(body, "result")
					}
					if scenario == "missing-count" {
						delete(body, "count")
					}
					_ = json.NewEncoder(w).Encode(body)
				})
				var err error
				if kind == "servers" {
					_, err = c.ListServers(context.Background(), "p")
				} else {
					_, err = c.ListVolumes(context.Background(), "p")
				}
				if err == nil || calls > 2 {
					t.Fatalf("incomplete inventory accepted: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestInventoriesAcceptExplicitZeroWithoutResultAndExactFullPage(t *testing.T) {
	for _, kind := range []string{"servers", "volumes"} {
		for _, count := range []int{0, 100} {
			calls := 0
			c := clientAt(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"count": count}
				if count > 0 {
					items := make([]map[string]any, count)
					for i := range items {
						items[i] = map[string]any{"id": fmt.Sprint(i), "size": 10}
					}
					body["result"] = items
				}
				_ = json.NewEncoder(w).Encode(body)
			})
			var n int
			var err error
			if kind == "servers" {
				var x []Server
				x, err = c.ListServers(context.Background(), "p")
				n = len(x)
			} else {
				var x []Volume
				x, err = c.ListVolumes(context.Background(), "p")
				n = len(x)
			}
			if err != nil || n != count || calls != 1 {
				t.Fatalf("kind=%s n=%d calls=%d error=%v", kind, n, calls, err)
			}
		}
	}
}
