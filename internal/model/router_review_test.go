package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func TestRouterCatalogRejectsCredentialEcho(t *testing.T) {
	for _, adapter := range []string{"openai_api_chat", "anthropic_api_messages", "openai_siwc_responses"} {
		for _, field := range []string{"id", "display_name"} {
			t.Run(adapter+"/"+field, func(t *testing.T) {
				item := map[string]string{"id": "safe-model", "display_name": "Safe model"}
				item[field] = "prefix-fake-test-secret-suffix"
				envelope := map[string]any{"data": []any{item}}
				if adapter == "openai_siwc_responses" {
					item["slug"] = item["id"]
					delete(item, "id")
					item["visibility"] = "list"
					envelope = map[string]any{"models": []any{item}}
				}
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(envelope)
				}))
				defer srv.Close()
				s := routedSelection(srv.URL+"/v1", adapter)
				models, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).ProbeSelection(context.Background(), 2, s)
				requireProviderError(t, err, "response_incomplete", "unknown")
				if models != nil || calls != 1 {
					t.Fatalf("unsafe catalog escaped or retried: models=%#v calls=%d", models, calls)
				}
			})
		}
	}
}

func TestRouterUnexpectedStatusesRemainUnknown(t *testing.T) {
	for _, status := range []int{201, 202, 204, 206, 300, 304} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "req_unexpected")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"code":"invalid_api_key","message":"fake-test-secret"}}`)
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", "openai_api_chat")
			result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
			p := requireProviderError(t, err, "response_incomplete", "unknown")
			if p.HTTPStatus != status || p.RequestID != "req_unexpected" || result.Text != "" || result.CredentialGeneration != 7 || calls != 1 {
				t.Fatalf("status evidence/result/count: %#v %#v %d", p, result, calls)
			}
			encoded, _ := json.Marshal(p)
			if strings.Contains(string(encoded), "fake-test-secret") {
				t.Fatal("raw error detail escaped")
			}
		})
	}
}

func TestRouterRequiresAssistantResponseRole(t *testing.T) {
	for _, adapter := range []string{"openai_api_responses", "openai_siwc_responses", "anthropic_api_messages"} {
		for _, role := range []string{"", "user", "system", "assistant"} {
			t.Run(adapter+"/"+role, func(t *testing.T) {
				message := map[string]any{"type": "message", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": "done"}}}
				if role != "" {
					message["role"] = role
				}
				envelope := map[string]any{"object": "response", "status": "completed", "output": []any{message}}
				if adapter == "anthropic_api_messages" {
					envelope = map[string]any{"type": "message", "stop_reason": "end_turn", "content": []any{map[string]string{"type": "text", "text": "done"}}}
					if role != "" {
						envelope["role"] = role
					}
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if adapter == "openai_siwc_responses" {
						w.Header().Set("Content-Type", "text/event-stream")
						raw, _ := json.Marshal(map[string]any{"type": "response.completed", "response": envelope})
						fmt.Fprintf(w, "data: %s\n\n", raw)
					} else {
						json.NewEncoder(w).Encode(envelope)
					}
				}))
				defer srv.Close()
				s := routedSelection(srv.URL+"/v1", adapter)
				result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
				if role == "assistant" {
					if err != nil || result.Text != "done" {
						t.Fatalf("valid assistant failed: %#v %v", result, err)
					}
				} else {
					requireProviderError(t, err, "response_incomplete", "unknown")
					if result.Text != "" {
						t.Fatal("non-assistant content returned")
					}
				}
			})
		}
	}
}

func TestRouterSimulationCapabilityUsesOnlyValidatedPins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origins []string
		want    bool
	}{
		{"production", nil, false},
		{"invalid-only", []string{"http://localhost:1234", "http://127.0.0.1:1234/v1", "https://api.openai.com"}, false},
		{"pinned", []string{"http://127.0.0.1:1234"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &leaseResolver{}
			router := NewRouter(nil, resolver, RouterOptions{SimulationOrigins: tc.origins})
			if router.SimulationEnabled() != tc.want {
				t.Fatal("simulation capability did not reflect validated origins")
			}
			s := routedSelection("https://api.openai.com/v1", "openai_api_chat")
			s.Route.Environment = "live"
			requireProviderError(t, router.PreflightModelSelection(context.Background(), 2, s), "live_provider_access_pending_confirmation", "not_sent")
			if resolver.calls != 0 {
				t.Fatal("capability lookup read credentials")
			}
		})
	}
}

type exactLeaseResolver string

func (secret exactLeaseResolver) ResolveModelCredential(context.Context, domain.RoleModelSelection) (CredentialLease, error) {
	return CredentialLease{Secret: string(secret), Generation: 7}, nil
}

func TestRouterRejectsCredentialEchoInAssistantText(t *testing.T) {
	for _, adapter := range []string{"openai_api_chat", "openai_siwc_responses"} {
		t.Run(adapter, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "req_echo")
				text := "prefix " + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") + " suffix"
				if adapter == "openai_api_chat" {
					json.NewEncoder(w).Encode(map[string]any{"object": "chat.completion", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": text}}}})
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
					body, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": text}}}}}})
					fmt.Fprintf(w, "data: %s\n\n", body)
				}
			}))
			defer srv.Close()
			s := routedSelection(srv.URL+"/v1", adapter)
			result, err := NewRouter(nil, &leaseResolver{}, RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
			p := requireProviderError(t, err, "response_incomplete", "unknown")
			if result.Text != "" || result.UsageKnown || result.ResolvedModel != "" || result.CredentialGeneration != 7 || p.RequestID != "req_echo" || calls != 1 {
				t.Fatalf("unsafe output returned or lost metadata: %#v %#v calls=%d", result, p, calls)
			}
		})
	}
}

func TestRouterRemovesCredentialEchoFromAllowlistedErrorFields(t *testing.T) {
	for _, adapter := range []string{"openai_api_chat", "openai_siwc_responses"} {
		for _, field := range []string{"code", "param"} {
			t.Run(adapter+"/"+field, func(t *testing.T) {
				secret := "invalid_api_key"
				if field == "param" {
					secret = "model"
				}
				errorObject := map[string]string{field: secret}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if adapter == "openai_api_chat" {
						w.WriteHeader(401)
						json.NewEncoder(w).Encode(map[string]any{"error": errorObject})
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
						raw, _ := json.Marshal(map[string]any{"type": "response.failed", "response": map[string]any{"error": errorObject}})
						fmt.Fprintf(w, "data: %s\n\n", raw)
					}
				}))
				defer srv.Close()
				s := routedSelection(srv.URL+"/v1", adapter)
				_, err := NewRouter(nil, exactLeaseResolver(secret), RouterOptions{SimulationOrigins: []string{srv.URL}}).Complete(context.Background(), routedRequest(s))
				p, ok := err.(*ProviderError)
				if !ok {
					t.Fatalf("missing classified error: %v", err)
				}
				if p.ProviderCode != "" || p.ProviderParam != "" {
					t.Fatalf("credential echoed through allowlisted fields: %#v", p)
				}
			})
		}
	}
}
