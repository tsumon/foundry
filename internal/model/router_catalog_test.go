package model

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRouterSIWCCatalogUsesVisibleSlugsAndServerOrder(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fake-test-secret" {
			t.Error("catalog request mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"slug":"z-model","display_name":"Z model","visibility":"list"},{"slug":"hidden","display_name":"Hidden","visibility":"hidden"},{"slug":"a-model","display_name":"A model","visibility":"list"}]}`)
	}))
	defer srv.Close()
	resolver := &leaseResolver{}
	router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: []string{srv.URL}})
	s := routedSelection(srv.URL+"/v1", "openai_siwc_responses")
	if calls != 0 || resolver.calls != 0 {
		t.Fatal("constructor probed")
	}
	models, err := router.ProbeSelection(context.Background(), 2, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models, []CatalogModel{{ID: "z-model", DisplayName: "Z model"}, {ID: "a-model", DisplayName: "A model"}}) {
		t.Fatalf("catalog %#v", models)
	}
	s.Account.ID = "other"
	_, err = router.ProbeSelection(context.Background(), 2, s)
	requireProviderError(t, err, "request_rejected", "not_sent")
	if calls != 1 || resolver.calls != 1 {
		t.Fatal("invalid selection accessed cached credentials/network")
	}
}

func TestRouterCatalogRejectsWrongShapeAndContent(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"wrong"}]}`, `{"models":null}`, `{"models":[{"slug":"bad\nslug","display_name":"Bad","visibility":"list"}]}`, `{"models":[{"slug":"ok","display_name":"<script>x</script>","visibility":"list"}]}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", "openai_siwc_responses")
			_, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).ProbeSelection(context.Background(), 2, s)
			requireProviderError(t, err, "response_incomplete", "unknown")
		})
	}
}

func TestRouterCatalogProductionGate(t *testing.T) {
	s := routedSelection("https://api.openai.com/v1", "openai_siwc_responses")
	s.Route.Environment = "live"
	resolver := &leaseResolver{}
	_, err := NewRouter(nil, resolver, RouterOptions{}).ProbeSelection(context.Background(), 2, s)
	requireProviderError(t, err, "live_provider_access_pending_confirmation", "not_sent")
	if resolver.calls != 0 {
		t.Fatal("live catalog read secret")
	}
}
