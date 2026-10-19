package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"foundry/internal/domain"
	"io"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptanceGatesIndependentOwnedProtocolStages(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	for _, provider := range []string{"openai", "kimi"} {
		t.Run(provider, func(t *testing.T) {
			account := "acceptance-gate-" + provider
			route := account + "-route"
			var lastClaim int64
			run := func(phase, prereq, model string) domain.ModelAcceptanceOperation {
				t.Helper()
				a, e := r.store.GetModelAccount(account)
				if e != nil {
					t.Fatal(e)
				}
				o, e := r.gates.Propose(account, domain.ModelAcceptanceProposal{Phase: phase, RouteID: route, ExpectedAccountRevision: a.Revision, PrerequisiteID: prereq, Model: model})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = r.gates.Execute(context.Background(), account, o.ID, o.Revision, o.MaterialDigest); e == nil {
					t.Fatal("unapproved execution")
				}
				o, e = r.gates.Decide(account, o.ID, o.Revision, o.MaterialDigest, "approve")
				if e != nil {
					t.Fatal(e)
				}
				approved := o
				o, e = r.gates.Execute(context.Background(), account, o.ID, o.Revision, o.MaterialDigest)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = r.gates.Execute(context.Background(), account, approved.ID, approved.Revision, approved.MaterialDigest); e == nil {
					t.Fatal("duplicate execution")
				}
				raw, _ := json.Marshal(o)
				for _, marker := range r.gateProtocol.secretMarkers() {
					if marker != "" && strings.Contains(string(raw), marker) {
						t.Fatal("secret in receipt")
					}
				}
				if o.Attempts != 1 || o.ClaimSequence <= lastClaim || o.Source != "local_simulation" || o.LiveAccess != "blocked" || o.Quota != "unknown" {
					t.Fatalf("unsafe receipt %+v", o)
				}
				lastClaim = o.ClaimSequence
				return o
			}
			r.gateProtocol.mu.Lock()
			beforeDirectory := r.gateProtocol.directories
			beforeHits := 0
			for _, n := range r.gateProtocol.modelHits {
				beforeHits += n
			}
			r.gateProtocol.mu.Unlock()
			conn := run("connection", "", "")
			if conn.State != "succeeded" {
				t.Fatalf("connection %+v", conn)
			}
			r.gateProtocol.mu.Lock()
			dirs := r.gateProtocol.directories
			hits := 0
			for _, n := range r.gateProtocol.modelHits {
				hits += n
			}
			r.gateProtocol.mu.Unlock()
			if dirs != beforeDirectory || hits != beforeHits {
				t.Fatal("connection dispatched next stage")
			}
			cat := run("catalog", conn.ID, "")
			if cat.State != "succeeded" || len(cat.Catalog) == 0 {
				t.Fatalf("catalog %+v", cat)
			}
			r.gateProtocol.mu.Lock()
			dirs = r.gateProtocol.directories
			hits = 0
			for _, n := range r.gateProtocol.modelHits {
				hits += n
			}
			r.gateProtocol.mu.Unlock()
			if dirs != beforeDirectory+1 || hits != beforeHits {
				t.Fatal("catalog dispatched inference")
			}
			infer := run("inference", cat.ID, cat.Catalog[0].ID)
			if infer.State != "succeeded" || !infer.UsageKnown || infer.InputTokens != 11 || infer.OutputTokens != 7 {
				t.Fatalf("inference %+v", infer)
			}
			for _, scenario := range []struct{ model, state, code string }{{"synthetic-quota-exhausted", "failed", "quota_exhausted"}, {"synthetic-permission-denied", "failed", "entitlement_denied"}, {"synthetic-rate-limited", "failed", "rate_limited"}, {"synthetic-sse-outcome-unknown", "unknown", "response_incomplete"}} {
				got := run("inference", cat.ID, scenario.model)
				if got.State != scenario.state || got.Code != scenario.code {
					t.Fatalf("%s: %+v", scenario.model, got)
				}
			}
			r.gateProtocol.mu.Lock()
			dirs = r.gateProtocol.directories
			hits = 0
			for _, n := range r.gateProtocol.modelHits {
				hits += n
			}
			invalid := r.gateProtocol.invalid
			r.gateProtocol.mu.Unlock()
			if dirs != beforeDirectory+1 || hits != beforeHits+5 || invalid != "" {
				t.Fatal("unexpected replay, fallback, or fixture payload")
			}
		})
	}
	db, e := sql.Open("sqlite3", filepath.Join(r.Root, "foundry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	rows, e := db.Query(`SELECT data FROM model_acceptance_operations`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			t.Fatal(e)
		}
		for _, marker := range r.gateProtocol.secretMarkers() {
			if marker != "" && strings.Contains(raw, marker) {
				t.Fatal("token marker persisted in operation")
			}
		}
		for _, marker := range []string{"secret_ref", "handoff_url", "access_token", domain.AcceptancePrompt, "synthetic output"} {
			if strings.Contains(raw, marker) {
				t.Fatal("sensitive material persisted in operation")
			}
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
}

func TestAcceptanceGatesHTTPReviewExecuteAndSafeHistory(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	request := func(method, path string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, r.URL+path, strings.NewReader(string(raw)))
		req.Header.Set("Origin", r.URL)
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		out, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, out
	}
	if status, _ := request("POST", "/api/session", map[string]any{}); status != 200 {
		t.Fatal(status)
	}
	account := "acceptance-gate-kimi"
	path := "/api/model-accounts/" + account + "/acceptance-operations"
	decode := func(status int, raw []byte) domain.ModelAcceptanceOperation {
		t.Helper()
		if status != 200 {
			t.Fatalf("HTTP %d %s", status, raw)
		}
		var envelope struct {
			Operation domain.ModelAcceptanceOperation `json:"operation"`
		}
		if e := json.Unmarshal(raw, &envelope); e != nil {
			t.Fatal(e)
		}
		return envelope.Operation
	}
	status, raw := request("GET", path, nil)
	if status != 200 || !strings.Contains(string(raw), `"simulation_enabled":true`) {
		t.Fatalf("list %d %s", status, raw)
	}
	o := decode(request("POST", path, map[string]any{"phase": "connection", "route_id": account + "-route", "expected_account_revision": 1}))
	if status, _ := request("POST", path+"/"+o.ID+"/execute", map[string]any{"expected_revision": o.Revision, "material_digest": o.MaterialDigest}); status != 409 {
		t.Fatalf("unapproved %d", status)
	}
	if status, _ := request("POST", path+"/"+o.ID+"/decision", map[string]any{"expected_revision": o.Revision, "material_digest": strings.Repeat("0", 64), "decision": "approve"}); status != 409 {
		t.Fatalf("digest %d", status)
	}
	o = decode(request("POST", path+"/"+o.ID+"/decision", map[string]any{"expected_revision": o.Revision, "material_digest": o.MaterialDigest, "decision": "approve"}))
	approved := o
	o = decode(request("POST", path+"/"+o.ID+"/execute", map[string]any{"expected_revision": o.Revision, "material_digest": o.MaterialDigest}))
	if o.State != "succeeded" {
		t.Fatalf("connection %+v", o)
	}
	if status, _ := request("POST", path+"/"+o.ID+"/execute", map[string]any{"expected_revision": approved.Revision, "material_digest": o.MaterialDigest}); status != 409 {
		t.Fatalf("repeat %d", status)
	}
	saved := decode(request("GET", path+"/"+o.ID, nil))
	if saved.Revision != o.Revision || saved.Attempts != 1 || saved.ClaimSequence < 1 || saved.ClaimSequence != o.ClaimSequence {
		t.Fatal("read mutated record")
	}
	if status, body := request("GET", "/api/model-accounts/acceptance-gate-openai/acceptance-operations/"+o.ID, nil); status != 404 || strings.Contains(string(body), o.MaterialDigest) {
		t.Fatalf("cross-account %d", status)
	}
	for _, entry := range []struct {
		path string
		body any
	}{{"/api/model-authorizations/start", map[string]any{"account_id": "acceptance-gate-openai", "expected_account_revision": 1}}, {"/api/model-accounts/acceptance-gate-kimi/credentials", map[string]any{"expected_revision": 3, "operation_id": "bypass", "key": "DO-NOT-IMPORT"}}} {
		if status, _ := request("POST", entry.path, entry.body); status != 403 {
			t.Fatalf("direct bypass %d", status)
		}
	}
	db, e := sql.Open("sqlite3", filepath.Join(r.Root, "foundry.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	rows, e := db.Query(`SELECT data FROM model_acceptance_operations`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if e = rows.Scan(&data); e != nil {
			t.Fatal(e)
		}
		for _, secret := range r.gateProtocol.secretMarkers() {
			if secret != "" && strings.Contains(data, secret) {
				t.Fatal("secret persisted in acceptance operation")
			}
		}
		for _, forbidden := range []string{"secret_ref", "handoff_url", "access_token", domain.AcceptancePrompt, "synthetic output"} {
			if strings.Contains(data, forbidden) {
				t.Fatal("sensitive field persisted")
			}
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	// Safety revocation remains available, but the global legacy SIWC fixture
	// must never receive a token minted by the separate acceptance-gate issuer.
	openai := "acceptance-gate-openai"
	conn, e := r.gates.Propose(openai, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: openai + "-route", ExpectedAccountRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	conn, e = r.gates.Decide(openai, conn.ID, conn.Revision, conn.MaterialDigest, "approve")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.gates.Execute(context.Background(), openai, conn.ID, conn.Revision, conn.MaterialDigest); e != nil {
		t.Fatal(e)
	}
	auth, e := r.store.CurrentModelAuthorization(openai)
	if e != nil {
		t.Fatal(e)
	}
	r.protocol.mu.Lock()
	beforeRevoke := r.protocol.revokeRequests
	r.protocol.mu.Unlock()
	status, raw = request("POST", "/api/model-authorizations/"+auth.Binding.ID+"/revoke", map[string]any{"expected_account_revision": 1})
	if status != 409 || !strings.Contains(string(raw), "live_provider_access_pending_confirmation") || !strings.Contains(string(raw), `"local_state":"revoked"`) || !strings.Contains(string(raw), `"remote_state":"unconfirmed"`) {
		t.Fatalf("local-only safety revoke %d %s", status, raw)
	}
	auth, e = r.store.CurrentModelAuthorization(openai)
	if e != nil || auth.State != "revoked" {
		t.Fatal("safety revoke failed to disable local authorization")
	}
	r.protocol.mu.Lock()
	afterRevoke := r.protocol.revokeRequests
	r.protocol.mu.Unlock()
	if beforeRevoke != afterRevoke {
		t.Fatal("gate token sent to legacy fixture")
	}
	r.gateProtocol.mu.Lock()
	gateRevoke := r.gateProtocol.revokeRequests
	r.gateProtocol.mu.Unlock()
	if gateRevoke != 0 {
		t.Fatal("local-only revoke sent remote request")
	}
}
