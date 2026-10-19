package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagnosticModePreservesSessionHostOriginAndAuthenticationBoundaries(t *testing.T) {
	f := newFixture(t)
	f.handler.(*server).recoveryReadOnly = true
	f.authenticate()
	for _, tc := range []struct {
		name, method, path, host, origin, contentType string
		auth                                          bool
		want                                          int
	}{
		{"no read session", "GET", "/api/health", "127.0.0.1:4317", "", "", false, 401},
		{"no mutation session", "POST", "/api/profiles", "127.0.0.1:4317", testOrigin, "application/json", false, 401},
		{"foreign read origin", "GET", "/api/missions", "127.0.0.1:4317", "https://outside.invalid", "", true, 403},
		{"wrong host", "GET", "/api/missions", "outside.invalid", "", "", true, 403},
		{"foreign mutation origin", "POST", "/api/profiles", "127.0.0.1:4317", "https://outside.invalid", "application/json", true, 403},
		{"non JSON mutation", "POST", "/api/profiles", "127.0.0.1:4317", testOrigin, "text/plain", true, 403},
		{"foreign session origin", "POST", "/api/session", "127.0.0.1:4317", "https://outside.invalid", "application/json", false, 403},
		{"wrong session host", "POST", "/api/session", "outside.invalid", testOrigin, "application/json", false, 403},
		{"non JSON session", "POST", "/api/session", "127.0.0.1:4317", testOrigin, "text/plain", false, 403},
		{"local session allowed", "POST", "/api/session", "127.0.0.1:4317", testOrigin, "application/json", false, 200},
		{"authenticated mutation denied", "POST", "/api/missions/missing/chat", "127.0.0.1:4317", testOrigin, "application/json", true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, testOrigin+tc.path, strings.NewReader(`{}`))
			req.Host = tc.host
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", tc.contentType)
			if tc.auth {
				req.AddCookie(f.cookie)
			}
			rr := httptest.NewRecorder()
			f.handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if tc.want != 200 && len(rr.Result().Cookies()) != 0 {
				t.Fatal("failed request issued session")
			}
		})
	}
}
