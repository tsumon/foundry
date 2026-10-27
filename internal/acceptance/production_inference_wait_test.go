package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Replace only the native storage boundary. Resolver, ledger and inference
// request/response checks remain real; no native credential store is opened.
type inferenceWaitSecrets struct {
	model.Secrets
	read func()
	err  error
}

func (s *inferenceWaitSecrets) Get(ref string) (string, error) {
	if s.read != nil {
		s.read()
	}
	if s.err != nil {
		return "", s.err
	}
	return s.Secrets.Get(ref)
}

func installInferenceWaitSecrets(t *testing.T, s *Service, read func(), err error) *catalogMemorySecrets {
	t.Helper()
	auth, e := s.store.GetModelAuthorization("catalog-proof")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]string{"access_token": "synthetic-catalog-secret", "refresh_token": "unused-synthetic-refresh", "expires_at": auth.ExpiresAt, "resource": "https://api.openai.com/v1"})
	secrets := &catalogMemorySecrets{t: t, ref: auth.SecretRef, value: string(raw)}
	s.inference.resolver = accounts.New(s.store, &inferenceWaitSecrets{Secrets: secrets, read: read, err: err})
	return secrets
}

func TestProductionInferenceCredentialWaitDoesNotSpendNetworkTimeout(t *testing.T) {
	t.Parallel()
	s, db, a, r, c, _, calls, _ := inferenceController(t)
	before, _ := db.GetModelAuthorization("catalog-proof")
	secrets := installInferenceWaitSecrets(t, s, func() { time.Sleep(31 * time.Second) }, nil)
	transport := s.inference.http.Transport
	s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) < 29*time.Second || time.Until(deadline) > 30*time.Second {
			t.Error("HTTP did not get its own bounded window")
		}
		return transport.RoundTrip(req)
	})
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	got, err := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || got.State != "succeeded" || got.OutputMatched == nil || !*got.OutputMatched || calls.Load() != 1 || secrets.reads != 1 {
		t.Fatalf("local credential wait consumed HTTP window: state=%s code=%s calls=%d reads=%d err=%v", got.State, got.Code, calls.Load(), secrets.reads, err)
	}
	after, _ := db.GetModelAuthorization("catalog-proof")
	if domain.AcceptanceDigest(after) != domain.AcceptanceDigest(before) {
		t.Fatal("credential wait changed authorization")
	}
	m, _ := db.GetMission("inference-budget-task")
	if m.Budget.UsedCalls != 4 || m.Budget.MaxCalls != 7 {
		t.Fatal("wrong budget", m.Budget)
	}
	if _, err = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil || calls.Load() != 1 || secrets.reads != 1 {
		t.Fatal("wait enabled replay")
	}
}

func TestProductionInferenceCredentialAccessFailureIsNotRemoteAuth(t *testing.T) {
	s, db, a, r, c, _, calls, _ := inferenceController(t)
	before, _ := db.GetModelAuthorization("catalog-proof")
	installInferenceWaitSecrets(t, s, nil, errors.New("PRIVATE native denial with synthetic-catalog-secret"))
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	got, err := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || got.State != "failed" || got.Certainty != "not_sent" || got.Code != "credential_access_failed" || calls.Load() != 0 || got.InferenceDiagnostic != nil {
		t.Fatalf("local denial misclassified: %s/%s %v", got.State, got.Code, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), "synthetic-catalog-secret") {
		t.Fatal("native error leaked")
	}
	after, _ := db.GetModelAuthorization("catalog-proof")
	if domain.AcceptanceDigest(before) != domain.AcceptanceDigest(after) {
		t.Fatal("local denial poisoned grant")
	}
	if _, err = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil || calls.Load() != 0 {
		t.Fatal("local denial retried")
	}
}

func TestProductionInferenceCredentialWaitStopsOnCancellationExpiryOrAuthorityChange(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "material", "account", "malformed bundle"} {
		t.Run(mode, func(t *testing.T) {
			s, db, a, r, c, _, calls, _ := inferenceController(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := "request_rejected"
			secrets := installInferenceWaitSecrets(t, s, func() {
				switch mode {
				case "cancel":
					cancel()
				case "deadline":
					<-ctx.Done()
				case "material":
					s.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
				case "account":
					a.Enabled = false
					if _, err := db.SaveModelAccount(a, a.Revision); err != nil {
						t.Fatal(err)
					}
				}
			}, nil)
			if mode == "account" || mode == "malformed bundle" {
				want = "auth_invalid"
			}
			if mode == "malformed bundle" {
				secrets.value = `{"access_token":""}`
			}
			o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, time.Second)
				defer stop()
				want = "credential_wait_expired"
			}
			got, err := s.Execute(ctx, a.ID, o.ID, o.Revision, o.MaterialDigest)
			if err != nil || got.State != "failed" || got.Certainty != "not_sent" || got.Code != want || calls.Load() != 0 || got.InferenceDiagnostic != nil {
				t.Fatalf("unsafe wait %s: %s/%s %v", mode, got.State, got.Code, err)
			}
			m, _ := db.GetMission("inference-budget-task")
			if m.Budget.UsedCalls != 4 || m.Budget.MaxCalls != 7 {
				t.Fatal("local failure refunded", m.Budget)
			}
			if _, err = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil || calls.Load() != 0 {
				t.Fatal("automatic replay")
			}
		})
	}
}

func TestProductionInferenceCredentialWaitKeepsNetworkTimeout(t *testing.T) {
	t.Parallel()
	s, db, a, r, c, _, calls, _ := inferenceController(t)
	s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-req.Context().Done()
		return nil, req.Context().Err()
	})
	o := approveCatalog(t, s, proposeInference(t, s, a, r, c))
	start := time.Now()
	got, err := s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
	if err != nil || got.State != "unknown" || got.Certainty != "unknown" || got.Code != "transport_unknown" || calls.Load() != 1 || time.Since(start) > 35*time.Second {
		t.Fatalf("network timeout lost: %s/%s %v", got.State, got.Code, err)
	}
	m, _ := db.GetMission("inference-budget-task")
	if m.Budget.UsedCalls != 4 {
		t.Fatal("timeout refunded")
	}
	if _, err = s.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); err == nil || calls.Load() != 1 {
		t.Fatal("timed out send replayed")
	}
}
