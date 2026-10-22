package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A desktop fixture must not briefly serve unguarded bootstrap or silently
// place disposable evidence in OS temporary storage instead of retained data.
func TestDesktopRepositoryFixtureGuardAndRetention(t *testing.T) {
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("workbench"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	token := strings.Repeat("a", 64)
	r, err := startRepositoryTasksDesktop(web, "", parent, token)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if filepath.Dir(r.Root) != parent {
		t.Fatalf("evidence outside retained parent: %s", r.Root)
	}
	for _, path := range []string{"/", "/api/session"} {
		response, err := http.Get(r.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("unguarded %s: %d", path, response.StatusCode)
		}
		method := "GET"
		if path == "/api/session" {
			method = "POST"
		}
		req, _ := http.NewRequest(method, r.URL+path, strings.NewReader("{}"))
		req.Header.Set("X-Foundry-Desktop", token)
		req.Header.Set("Origin", r.URL)
		req.Header.Set("Content-Type", "application/json")
		response, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("authorized %s: %d", path, response.StatusCode)
		}
	}
	r.Close()
	if _, err := os.Stat(filepath.Join(r.Root, "foundry.db")); err != nil {
		t.Fatal("evidence not retained", err)
	}
}
