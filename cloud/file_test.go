package cloud

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStorageReadsRotatedCredentialAndRedactsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(path, []byte("first-secret\n"), 0600); e != nil {
		t.Fatal(e)
	}
	var seen []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Authorization"), "second-secret") {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"message":"echo second-secret"}}`)
			return
		}
		fmt.Fprint(w, `{"result":{"id":"project","status":"ACTIVE"}}`)
	}))
	defer s.Close()
	storage, e := newFileStorage(path, func(token string) (StorageAPI, error) { return newClient(token, s.URL, s.Client()) })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = storage.GetProject(context.Background(), "project"); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(path, []byte(`"Bearer second-secret"`), 0600)
	_, e = storage.GetProject(context.Background(), "project")
	if e == nil || strings.Contains(e.Error(), "second-secret") {
		t.Fatal("rotated credential leaked")
	}
	if len(seen) != 2 || seen[0] != "Bearer first-secret" || seen[1] != "Bearer second-secret" {
		t.Fatal("credential file not read fresh")
	}
}
