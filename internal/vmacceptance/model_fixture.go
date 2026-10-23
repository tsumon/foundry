package vmacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"io"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
)

const fixtureModel = "SYNTHETIC-LOCAL-PROTOCOL"
const fixtureCredential = "SYNTHETIC-NOT-A-PROVIDER-CREDENTIAL"
const fixtureAccount = "native-fixture-account"
const initialSource = "package triple\nfunc Triple(n int) int { return n * 3 }\n"
const workerSource = "package triple\nfunc Triple(n int) int { if n < 0 { return 0 }; return n + n + n }\n"
const repairedSource = "package triple\nfunc Triple(n int) int { return n + n + n }\n"

var fixtureRoles = []domain.Role{domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor}

// This lease exists only inside the explicit acceptance executable. It never
// accesses a credential store and cannot select any real provider endpoint.
type fixtureLease struct {
	base  string
	reads atomic.Int64
}

func (l *fixtureLease) ResolveModelCredential(ctx context.Context, s domain.RoleModelSelection) (model.CredentialLease, error) {
	if err := ctx.Err(); err != nil {
		return model.CredentialLease{}, err
	}
	if s.Account == nil || s.Route == nil || s.Account.ID != fixtureAccount || s.Profile.AccountID != fixtureAccount || s.Profile.Model != fixtureModel || s.Profile.BaseURL != l.base || s.Account.BaseURL != l.base || s.Route.BaseURL != l.base || s.Route.Environment != "simulation" || s.Route.Adapter != "openai_api_responses" {
		return model.CredentialLease{}, errors.New("synthetic_lease_selection_denied")
	}
	l.reads.Add(1)
	return model.CredentialLease{Secret: fixtureCredential, Generation: 1}, nil
}

type localFixture struct {
	mu      sync.Mutex
	pending *model.Request
	calls   []model.Request
	failed  bool
}

func newLocalFixture() *localFixture { return &localFixture{calls: []model.Request{}} }
func (f *localFixture) authorize(q model.Request) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed || len(f.calls) >= len(fixtureRoles) || q.Role != fixtureRoles[len(f.calls)] || q.InvocationID == "" || q.Profile.Model != fixtureModel {
		return errors.New("fixture_admission_denied")
	}
	if f.pending != nil && !reflect.DeepEqual(*f.pending, q) {
		return errors.New("fixture_claim_changed")
	}
	f.pending = &q
	return nil
}
func (f *localFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reject := func() { f.failed = true; http.Error(w, "synthetic_fixture_rejected", http.StatusBadRequest) }
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer "+fixtureCredential || f.pending == nil || f.failed {
		reject()
		return
	}
	var body struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Input        []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"input"`
		Max   int   `json:"max_output_tokens"`
		Store *bool `json:"store"`
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF {
		reject()
		return
	}
	q := *f.pending
	if body.Model != q.Profile.Model || body.Instructions != q.System || len(body.Input) != 1 || body.Input[0].Role != "user" || body.Input[0].Content != q.Prompt || body.Max != q.MaxOutputTokens || body.Store == nil || *body.Store {
		reject()
		return
	}
	reply, err := fixtureReply(q)
	if err != nil {
		reject()
		return
	}
	f.calls = append(f.calls, q)
	f.pending = nil
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", fmt.Sprintf("synthetic-local-%d", len(f.calls)))
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "response", "id": "synthetic-local-response", "model": fixtureModel, "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": reply}}}}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}})
}
func fixtureReply(q model.Request) (string, error) {
	var value any
	switch q.Role {
	case domain.Researcher:
		value = map[string]any{"summary": "SYNTHETIC local fixture observation", "findings": []any{}, "unknowns": []any{}}
	case domain.Planner:
		value = map[string]any{"summary": "Preserve Triple behavior with addition only", "steps": []any{map[string]any{"id": "addition", "title": "Use addition", "description": "Replace multiplication with addition preserving signed inputs", "depends_on": []string{}, "files": []string{"triple.go"}}}}
	case domain.Worker, domain.Debugger:
		content := workerSource
		if q.Role == domain.Debugger {
			content = repairedSource
		}
		value = map[string]any{"summary": "SYNTHETIC scoped implementation", "edits": []any{map[string]string{"path": "triple.go", "content": content}}}
	case domain.QA, domain.Auditor:
		var prompt struct {
			Extra domain.CodingReviewContract `json:"extra"`
		}
		if json.Unmarshal([]byte(q.Prompt), &prompt) != nil || prompt.Extra.MissionID == "" || !sha256Hex.MatchString(prompt.Extra.BindingDigest) || len(prompt.Extra.Required) == 0 {
			return "", errors.New("fixture_review_binding_missing")
		}
		checks := []domain.CodingReviewCheck{}
		yes := true
		for _, ref := range prompt.Extra.Required {
			checks = append(checks, domain.CodingReviewCheck{ID: ref.ID, Passed: &yes, Summary: "SYNTHETIC review of controller supplied evidence", VerificationID: ref.VerificationID, AcceptanceIndex: ref.AcceptanceIndex, ReferenceDigest: ref.ReferenceDigest, Paths: []string{}})
		}
		value = map[string]any{"passed": true, "summary": "SYNTHETIC local review; native test facts remain controller-owned", "findings": []any{}, "binding_digest": prompt.Extra.BindingDigest, "checks": checks}
	default:
		return "", errors.New("fixture_role_denied")
	}
	b, err := json.Marshal(value)
	return string(b), err
}
