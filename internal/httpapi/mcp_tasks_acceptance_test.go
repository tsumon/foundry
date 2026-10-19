package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"foundry/internal/store"
)

// Deliver a real authenticated POST through the production router, but drop its
// HTTP response after the handler returns (hence after its transaction commits).
// This fault lives only in this test, never in the runtime or MCP fixture.
type lostDecisionResponse struct{ http.ResponseWriter }

func (w lostDecisionResponse) WriteHeader(int)             {}
func (w lostDecisionResponse) Write(p []byte) (int, error) { return len(p), nil }

// Catches request-lifetime cancellation of accepted work, missing durable
// receipts, or a second MCP reservation/dispatch on reconciliation replay.
func TestMCPTaskAcceptanceLostCommittedResponse(t *testing.T) {
	f := newMCPHTTPFixture(t)
	m := f.create()
	base := "/api/missions/" + m.ID
	f.ok("POST", base+"/plan", map[string]any{"revision": m.Revision, "controlled_consent": true})
	m = f.wait(m.ID, "planning", "awaiting_tool_approval")
	proposals, err := f.store.ListMCPProposals(m.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatal(proposals, err)
	}
	p := proposals[0]
	path := base + "/mcp-proposals/" + p.ID + "/decision"
	payload := map[string]any{"expected_revision": m.Revision, "proposal_digest": p.Digest, "decision": "approve", "request_id": "lost-response-acceptance"}
	lossServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.handler.ServeHTTP(lostDecisionResponse{w}, r)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	defer lossServer.Close()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", lossServer.URL+path, strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", f.http.URL)
	req.Header.Set("Content-Type", "application/json")
	req.Host = f.http.Listener.Addr().String()
	req.AddCookie(f.cookie)
	response, err := f.http.Client().Do(req)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("fault did not lose the committed response")
	}
	f.wait(m.ID, "awaiting_approval", "")
	var receipt struct {
		Decision struct {
			RequestID    string `json:"request_id"`
			InvocationID string `json:"invocation_id"`
			GrantID      string `json:"grant_id"`
			Approved     bool   `json:"approved"`
		} `json:"decision"`
	}
	if err = json.Unmarshal([]byte(f.ok("GET", base+"/mcp-decisions/lost-response-acceptance", nil)), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Decision.Approved || receipt.Decision.RequestID != "lost-response-acceptance" || receipt.Decision.InvocationID == "" || receipt.Decision.GrantID == "" {
		t.Fatalf("missing committed receipt %+v", receipt)
	}
	f.ok("POST", path, payload)
	calls, err := f.store.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 || calls[0].ID != receipt.Decision.InvocationID || calls[0].GrantID != receipt.Decision.GrantID || calls[0].Status != "succeeded" {
		t.Fatalf("lost response replay dispatched again: %+v %v", calls, err)
	}
	if _, err := f.store.GetMCPDecision(m.ID, "different-intent"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unexpected replacement decision", err)
	}
}
