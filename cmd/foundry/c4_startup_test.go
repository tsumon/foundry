package main

import (
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/store"
)

// Production startup must keep corrupt/new C4 records readable, before any
// recovery writes or Docker/credential/model dependency can be reached.
func TestC4ProductionDiagnosticStartup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "foundry.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw := `{"id":"c4-history","title":"C4","revision":1,"state":"running","spec":{"c4":{"schema_version":999,"mode":"docker"}},"c4_operations":[]}`
	if _, err = db.Exec(`INSERT INTO missions VALUES('c4-history',1,'running','then',?)`, raw); err != nil {
		t.Fatal(err)
	}
	before := diagnosticRows(t, db)
	origin, client, cookie, stop := startDiagnosticProcess(t, directory)
	for _, tc := range []struct {
		method, path string
		want         int
		contains     string
	}{{"GET", "/api/health", 200, `"recovery_error":"c4_unavailable"`}, {"GET", "/api/missions/c4-history/c4", 200, `"read_only":true`}, {"POST", "/api/missions/c4-history/c4", 503, "recovery_read_only"}, {"POST", "/api/missions/c4-history/chat", 503, "recovery_read_only"}} {
		req, _ := http.NewRequest(tc.method, origin+tc.path, strings.NewReader(`{}`))
		req.AddCookie(cookie)
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.want || !strings.Contains(string(body), tc.contains) {
			t.Fatal(tc.path, res.StatusCode, string(body))
		}
	}
	stop()
	if !reflect.DeepEqual(before, diagnosticRows(t, db)) {
		t.Fatal("C4 diagnostic startup rewrote history")
	}
}
