package desktophost

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Removing the outer gate would expose both SPA files and session bootstrap.
func TestCapabilityGuardsEveryRoute(t *testing.T) {
	token := strings.Repeat("a", 64)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := Protect(token, next)
	for _, path := range []string{"/", "/assets/app.js", "/api/session", "/api/missions"} {
		for _, value := range []string{"", "wrong", token} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("X-Foundry-Desktop", value)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 403
			if value == token {
				want = 204
			}
			if w.Code != want {
				t.Fatalf("%s: got %d want %d", path, w.Code, want)
			}
		}
	}
}

// EOF must stop the controller; malformed and overlong capabilities never start it.
func TestHandshakeLifetime(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() { io.WriteString(writer, strings.Repeat("b", 64)+"\n") }()
	session, err := Open(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	writer.Close()
	select {
	case <-session.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("parent EOF did not cancel")
	}
	for _, s := range []string{"bad\n", strings.Repeat("x", 2000) + "\n", strings.Repeat("z", 64) + "\n"} {
		if _, err := Open(context.Background(), strings.NewReader(s)); err == nil {
			t.Fatal("invalid capability accepted")
		}
	}
}
