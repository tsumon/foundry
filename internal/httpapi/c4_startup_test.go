package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A nil caller service is not a guard: controlled New must never initialize
// default account/provider/capability paths, and the router must deny them first.
func TestC4ControlledStartupSuppressesServices(t *testing.T) {
	s := New(nil, nil, nil, nil, Options{C4ControlledOnly: true}).(*server)
	if s.accounts != nil || s.modelRouter != nil || s.siwc != nil || s.acceptance != nil || s.capability != nil || s.hooks != nil {
		t.Fatal("external service initialized")
	}
	for _, path := range []string{"/api/model-accounts", "/api/model-routes", "/api/model-authorizations", "/api/capabilities", "/api/catalog/probe", "/api/role-model-policy", "/api/profiles/a/test", "/api/magpie/discover", "/api/hooks", "/api/settings", "/api/missions/x/mcp/call", "/api/missions/x/read-grants", "/api/unknown-future-route"} {
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, "http://127.0.0.1:4317"+path, strings.NewReader(`{}`))
			r.AddCookie(&http.Cookie{Name: sessionName, Value: s.token})
			r.Header.Set("Origin", s.origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "c4_controlled_only") {
				t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
			}
		}
	}
}

func TestC4ControlledRouteAllowlist(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/health", true}, {"GET", "/api/profiles", true}, {"POST", "/api/profiles", false},
		{"GET", "/api/catalog", true}, {"POST", "/api/catalog/projects", false},
		{"POST", "/api/repository-imports/inspections", true}, {"GET", "/api/repository-imports/operations", true},
		{"GET", "/api/missions", true}, {"POST", "/api/missions", true},
		{"POST", "/api/missions/owned/c4", true}, {"POST", "/api/missions/owned/plan", true}, {"POST", "/api/missions/owned/approve", true}, {"POST", "/api/missions/owned/deliver", true}, {"POST", "/api/missions/owned/chat", true},
		{"POST", "/api/missions/owned/mcp-discovery", false}, {"GET", "/api/missions/owned/capabilities", false},
	} {
		if got := c4ControlledRoute(tc.method, tc.path); got != tc.want {
			t.Fatalf("%s %s = %v", tc.method, tc.path, got)
		}
	}
}
