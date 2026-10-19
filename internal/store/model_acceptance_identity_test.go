package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"testing"
	"time"
)

func identityGateStore(t *testing.T) (*Store, domain.ModelAccount, domain.ModelRoute) {
	t.Helper()
	s, _, _ := gateStore(t)
	a, e := s.SaveModelAccount(domain.ModelAccount{ID: "identity", Name: "identity", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "http://127.0.0.1:12345/v1"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateModelRoute(domain.ModelRoute{ID: "identity-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
	if e != nil {
		t.Fatal(e)
	}
	return s, a, r
}

func persistGateAuthorization(t *testing.T, s *Store, a domain.ModelAccount, id, purpose string) {
	t.Helper()
	if _, e := s.BeginModelAuthorization(a.ID, id+"-op", "sign_in", id); e != nil {
		t.Fatal(e)
	}
	scopes := []string{"openid"}
	if purpose == "subscription_only" {
		scopes = []string{"resource.invoke", "chatgpt.tokens.use.direct"}
	}
	if _, e := s.CommitModelAuthorization(id+"-op", domain.ModelAuthorizationBinding{ID: id, AccountID: a.ID, Issuer: "http://127.0.0.1:12345", Subject: "synthetic", ClientID: "synthetic", SpendClass: purpose, Scopes: scopes}, time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); e != nil {
		t.Fatal(e)
	}
	// This store-only fixture writes metadata, not secrets. Complete the exact
	// superseded references so unresolved cleanup cannot mask gate assertions.
	pending, e := s.PendingModelAuthorizationCleanup()
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range pending {
		if e = s.CompleteModelAuthorizationCleanup(op.ID); e != nil {
			t.Fatal(e)
		}
	}
}

// An identity receipt cannot acquire catalog/usage evidence through Finish.
func TestIdentityFinishRejectsModelEvidence(t *testing.T) {
	for _, kind := range []string{"catalog", "usage", "input", "output"} {
		t.Run(kind, func(t *testing.T) {
			s, a, r := identityGateStore(t)
			o, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			persistGateAuthorization(t, s, a, "proof", "identity_only")
			var catalog []domain.ModelAcceptanceCatalogEntry
			var input, output int64
			usage := false
			switch kind {
			case "catalog":
				catalog = []domain.ModelAcceptanceCatalogEntry{{ID: "model", DisplayName: "model"}}
			case "usage":
				usage = true
			case "input":
				input = 1
			case "output":
				output = 1
			}
			if _, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", catalog, input, output, usage, time.Now()); e == nil {
				t.Fatal("identity finish accepted model evidence")
			}
		})
	}
}

func TestIdentitySnapshotCannotUpgradeLegacyMaterialOrDownstreamReceipt(t *testing.T) {
	s, a, r := identityGateStore(t)
	legacy := gateProposal(t, s, a, r)
	identity, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	identity, e = s.DecideModelAcceptance(a.ID, identity.ID, identity.Revision, identity.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	identity, e = s.ClaimModelAcceptance(a.ID, identity.ID, identity.Revision, identity.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	persistGateAuthorization(t, s, a, "proof", "identity_only")
	if _, e = s.DecideModelAcceptance(a.ID, legacy.ID, legacy.Revision, legacy.MaterialDigest, "approve", time.Now()); e == nil {
		t.Fatal("legacy material upgraded to identity authorization")
	}
	identity, e = s.FinishModelAcceptance(a.ID, identity.ID, identity.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	q := domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: identity.ID}
	if _, e = s.ProposeModelAcceptance(a.ID, q, time.Now()); e == nil {
		t.Fatal("identity authorization accepted as connected")
	}
	persistGateAuthorization(t, s, a, "broader", "subscription_only")
	if _, e = s.ProposeModelAcceptance(a.ID, q, time.Now()); e == nil {
		t.Fatal("identity receipt upgraded after broader authorization")
	}
}

func TestIdentityMaterialTamperingRejectedAtTransitions(t *testing.T) {
	for _, action := range []string{"approve", "execute", "finish", "recover"} {
		for _, kind := range []string{"purpose", "scopes", "phase", "route", "attempts", "model", "connection"} {
			t.Run(action+"/"+kind, func(t *testing.T) {
				s, a, r := identityGateStore(t)
				o, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if action != "approve" {
					o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
					if e != nil {
						t.Fatal(e)
					}
				}
				if action == "finish" || action == "recover" {
					o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
					if e != nil {
						t.Fatal(e)
					}
				}
				switch kind {
				case "purpose":
					o.Material.ConnectionPurpose = "subscription_only"
				case "scopes":
					o.Material.RequestedScopes = []string{"openid", "offline_access"}
				case "phase":
					o.Phase = "catalog"
				case "route":
					o.Material.Route.AccountID = "wrong"
				case "attempts":
					o.Material.MaxAttempts = 2
				case "model":
					o.Material.Model = "unapproved-model"
				case "connection":
					o.Material.ConnectionID = "another-connection"
				}
				// Rehash deliberately: shape/purpose relationships are independently checked.
				o.MaterialDigest = domain.AcceptanceDigest(o.Material)
				raw, _ := json.Marshal(o)
				if _, e = s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); e != nil {
					t.Fatal(e)
				}
				switch action {
				case "approve":
					_, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
				case "execute":
					_, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
				case "finish":
					_, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "unknown", "unknown", "transport_unknown", nil, 0, 0, false, time.Now())
				case "recover":
					e = s.RecoverModelAcceptance(time.Now())
				}
				if e == nil {
					t.Fatal("tampered purpose/material admitted")
				}
			})
		}
	}
}

func TestLegacyAcceptanceSnapshotDigestIsUnchanged(t *testing.T) {
	s, a, r := gateStore(t)
	o := gateProposal(t, s, a, r)
	m := o.Material
	historical := struct {
		Account       domain.ModelAccount
		Route         domain.ModelRoute
		Credential    int64
		Authorization *domain.ModelAuthorization
	}{m.Account, m.Route, m.CredentialVersion, m.Authorization}
	if acceptanceSnapshotDigest(m) != domain.AcceptanceDigest(historical) {
		t.Fatal("legacy snapshot digest changed")
	}
	raw, _ := json.Marshal(m)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if fields["connection_purpose"] != nil || fields["requested_scopes"] != nil {
		t.Fatal("legacy JSON gained identity fields")
	}
}

func TestIdentityReauthenticationRequiresFreshProof(t *testing.T) {
	s, a, r := identityGateStore(t)
	persistGateAuthorization(t, s, a, "prior", "identity_only")
	o, e := s.ProposeModelAcceptance(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "identity_only", RouteID: r.ID, ExpectedAccountRevision: a.Revision}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now()); e == nil {
		t.Fatal("old identity proof satisfied fresh connection")
	}
	persistGateAuthorization(t, s, a, "fresh", "identity_only")
	if _, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, time.Now()); e != nil {
		t.Fatal(e)
	}
}
