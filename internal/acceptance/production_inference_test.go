package acceptance

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/store"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const inferenceCompletion = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-6.1-sol\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"FOUNDRY_INFERENCE_OK\"}]}],\"usage\":{\"input_tokens\":9,\"output_tokens\":7}}}\n\n"

func inferenceController(t *testing.T) (*Service, *store.Store, domain.ModelAccount, domain.ModelRoute, domain.ModelAcceptanceOperation, *catalogResolver, *atomic.Int32, string) {
	t.Helper()
	catalog, db, a, r, connection, resolver, calls, path := catalogControllerWithPath(t)
	catalog.catalog.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-6.1-sol","display_name":"GPT","visibility":"list"}]}`))}, nil
	})
	o := approveCatalog(t, catalog, proposeCatalog(t, catalog, a, r, connection))
	c, e := catalog.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if e != nil || c.State != "succeeded" {
		t.Fatalf("catalog: %s %v", c.State, e)
	}
	resolver.reads.Store(0)
	s := NewProductionInference(db, ProductionInferenceOptions{Route: r, Resolver: resolver})
	if s.inference.http == nil {
		t.Fatal("inference adapter missing")
	}
	s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, e := io.ReadAll(req.Body)
		if e != nil || req.Method != "POST" || req.URL.String() != "https://api.openai.com/v1/responses" || string(body) != `{"model":"gpt-6.1-sol","input":[{"role":"user","content":"Reply exactly: FOUNDRY_INFERENCE_OK"}],"store":false,"stream":true}` || req.Header.Get("Authorization") != "Bearer synthetic-catalog-secret" {
			t.Error("inference request exceeds authority")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_synthetic"}}, Body: io.NopCloser(strings.NewReader(inferenceCompletion)), Request: req}, nil
	})
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, db, a, r, c, resolver, calls, path
}
func proposeInference(t *testing.T, s *Service, a domain.ModelAccount, r domain.ModelRoute, c domain.ModelAcceptanceOperation) domain.ModelAcceptanceOperation {
	t.Helper()
	o, e := s.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "inference", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID, Model: "gpt-6.1-sol"})
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func TestProductionInferencePreparationAndSingleDispatch(t *testing.T) {
	s, _, a, r, c, resolver, calls, _ := inferenceController(t)
	if !s.ProductionInferenceStatus(a.ID).Enabled || s.ProductionCatalogStatus(a.ID).Enabled || s.ProductionConnectionStatus(a.ID).Enabled || s.SimulationEligible(a.ID) {
		t.Fatal("wrong mode")
	}
	o := proposeInference(t, s, a, r, c)
	if _, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil {
		t.Fatal("pending dispatched")
	}
	if calls.Load() != 0 || resolver.reads.Load() != 0 {
		t.Fatal("preparation effects")
	}
	o = approveCatalog(t, s, o)
	if calls.Load() != 0 || resolver.reads.Load() != 0 {
		t.Fatal("approval effects")
	}
	var wg sync.WaitGroup
	var winners atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if e == nil {
				winners.Add(1)
				if got.State != "succeeded" || got.OutputMatched == nil || !*got.OutputMatched || got.TerminalEvent != "response.completed" || got.InputTokens != 9 || got.OutputTokens != 7 || !got.UsageKnown {
					t.Errorf("bad result: %+v", got)
				}
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 || calls.Load() != 1 || resolver.reads.Load() != 1 {
		t.Fatalf("counts %d/%d/%d", winners.Load(), calls.Load(), resolver.reads.Load())
	}
}

func TestProductionInferenceBoundedFailuresAndMatch(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		body, state, code string
		matched, usage    bool
	}{
		{"success", 200, inferenceCompletion, "succeeded", "", true, true},
		{"mismatch", 200, strings.ReplaceAll(inferenceCompletion, "FOUNDRY_INFERENCE_OK", "different"), "succeeded", "", false, true},
		{"unknown usage", 200, strings.ReplaceAll(inferenceCompletion, `,"usage":{"input_tokens":9,"output_tokens":7}`, ""), "succeeded", "", true, false},
		{"auth", 401, `{}`, "failed", "auth_invalid", false, false},
		{"http plain echo", 401, `synthetic-catalog-secret`, "unknown", "response_incomplete", false, false},
		{"http json echo", 401, `{"error":{"message":"synthetic-catalog-secret","code":"invalid_api_key"}}`, "unknown", "response_incomplete", false, false},
		{"http escaped echo", 403, `{"error":{"message":"synthetic-catalog-\u0073ecret","code":"permission_error"}}`, "unknown", "response_incomplete", false, false},
		{"http duplicate-key echo", 429, `{"error":{"message":"synthetic-catalog-\u0073ecret","message":"discarded","code":"insufficient_quota"}}`, "unknown", "response_incomplete", false, false},
		{"entitlement", 403, `{}`, "failed", "entitlement_denied", false, false},
		{"rate", 429, `{}`, "failed", "rate_limited", false, false},
		{"server", 503, `{}`, "failed", "server_error", false, false},
		{"redirect", 302, `{}`, "unknown", "response_incomplete", false, false},
		{"quota after delta", 200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"FOUNDRY_INFERENCE_OK\"}\n\ndata: {\"type\":\"response.failed\",\"response\":{\"object\":\"response\",\"status\":\"failed\",\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\"}}}\n\n", "failed", "quota_exhausted", false, false},
		{"failed usage", 200, "data: {\"type\":\"response.failed\",\"response\":{\"object\":\"response\",\"status\":\"failed\",\"error\":{\"code\":\"subscription_sharing_usage_limit_exceeded\",\"param\":\"model\"},\"usage\":{\"input_tokens\":9,\"output_tokens\":7}}}\n\n", "failed", "quota_exhausted", false, true},
		{"malformed missing failed response", 200, "data: {\"type\":\"response.failed\"}\n\n", "unknown", "response_incomplete", false, false},
		{"malformed scalar failed response", 200, "data: {\"type\":\"response.failed\",\"response\":42}\n\n", "unknown", "response_incomplete", false, false},
		{"malformed failed with usage", 200, "data: {\"type\":\"response.failed\",\"response\":{\"object\":\"response\",\"status\":\"failed\",\"error\":null,\"usage\":{\"input_tokens\":9,\"output_tokens\":7}}}\n\n", "unknown", "response_incomplete", false, false},
		{"malformed missing error", 200, "data: {\"type\":\"error\"}\n\n", "unknown", "response_incomplete", false, false},
		{"malformed empty error", 200, "data: {\"type\":\"error\",\"error\":{}}\n\n", "unknown", "response_incomplete", false, false},
		{"unfamiliar valid failed response", 200, "data: {\"type\":\"response.failed\",\"response\":{\"object\":\"response\",\"status\":\"failed\",\"error\":{\"code\":\"future_code\"}}}\n\n", "failed", "request_rejected", false, false},
		{"unfamiliar valid error", 200, "data: {\"type\":\"error\",\"code\":\"future_code\"}\n\n", "failed", "request_rejected", false, false},
		{"delta only", 200, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"FOUNDRY_INFERENCE_OK\"}\n\n", "unknown", "response_incomplete", false, false},
		{"model", 200, strings.ReplaceAll(inferenceCompletion, "gpt-6.1-sol", "other"), "unknown", "response_incomplete", false, false},
		{"broken", 200, "data: {\n\n", "unknown", "response_incomplete", false, false},
		{"oversized", 200, strings.Repeat(" ", 1<<20) + inferenceCompletion, "unknown", "response_incomplete", false, false},
		{"echo", 200, strings.ReplaceAll(inferenceCompletion, "FOUNDRY_INFERENCE_OK", "synthetic-catalog-secret"), "unknown", "response_incomplete", false, false},
		{"escaped echo", 200, strings.ReplaceAll(inferenceCompletion, "FOUNDRY_INFERENCE_OK", `synthetic-catalog-\u0073ecret`), "unknown", "response_incomplete", false, false},
		{"tool", 200, strings.ReplaceAll(inferenceCompletion, `"type":"message"`, `"type":"function_call"`), "unknown", "response_incomplete", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, a, r, c, resolver, calls, _ := inferenceController(t)
			s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "Location": []string{"https://untrusted.invalid"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})
			o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
			got, e := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
			if e != nil || got.State != tc.state || got.Code != tc.code || got.UsageKnown != tc.usage {
				t.Fatalf("state=%s code=%s usage=%v error=%v", got.State, got.Code, got.UsageKnown, e)
			}
			if tc.name == "quota after delta" && (got.ProviderCode != "subscription_sharing_usage_limit_exceeded" || got.TerminalEvent != "response.failed") {
				t.Fatal("bounded terminal failure metadata lost")
			}
			if tc.name == "failed usage" && (got.InputTokens != 9 || got.OutputTokens != 7 || got.ProviderParam != "model") {
				t.Fatal("actual failed usage lost")
			}
			if tc.state == "succeeded" && (got.OutputMatched == nil || *got.OutputMatched != tc.matched) {
				t.Fatal("match not distinct")
			}
			if tc.state == "unknown" && (got.Certainty != "unknown" || got.Attempts != 1 || got.InferenceEvidence != (domain.InferenceEvidence{}) || got.InputTokens != 0 || got.OutputTokens != 0) {
				t.Fatalf("unknown retained terminal/provider evidence: %+v", got)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "synthetic-catalog-secret") || strings.Contains(string(raw), "different") {
				t.Fatal("raw output retained")
			}
			if _, e = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e == nil || calls.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatal("request replayed")
			}
		})
	}
}
