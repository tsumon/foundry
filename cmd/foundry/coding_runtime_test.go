package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionRenewalCodingStartupReconcilesClaimsWithoutCredentialIO(t *testing.T) {
	for _, crash := range []string{"before_intent", "after_intent", "after_commit"} {
		t.Run(crash, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "coding-renewal.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var requests atomic.Int32
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
			defer issuer.Close()
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "synthetic-renewal", Name: "Synthetic renewal", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", BaseURL: issuer.URL + "/v1", Enabled: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			b := domain.ModelAuthorizationBinding{ID: "synthetic-grant", AccountID: a.ID, Issuer: issuer.URL, Subject: "synthetic", ClientID: "oaiapp_synthetic", SpendClass: "subscription_only", Scopes: []string{"openid", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}}
			initial, err := s.BeginModelAuthorization(a.ID, "synthetic-signin", "sign_in", b.ID)
			if err != nil {
				t.Fatal(err)
			}
			first, err := s.CommitModelAuthorization(initial.ID, b, time.Now().Add(time.Minute).Format(time.RFC3339))
			if err != nil {
				t.Fatal(err)
			}
			m, err := s.PrepareSessionRenewal(domain.SessionRenewalMaterial{ID: "synthetic-material", AccountID: a.ID, AccountRevision: a.Revision, Binding: b, Resource: a.BaseURL, ExpiresAt: time.Now().Add(time.Minute).Format(time.RFC3339Nano)})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ApproveSessionRenewal(a.ID, m.ID, m.Revision, time.Now()); err != nil {
				t.Fatal(err)
			}
			policy, err := s.SessionRenewalPolicy(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ClaimSessionRenewal(policy, first.Generation, time.Now()); err != nil {
				t.Fatal(err)
			}
			if crash != "before_intent" {
				op, e := s.BeginModelAuthorization(a.ID, "synthetic-refresh", "refresh", b.ID)
				if e != nil {
					t.Fatal(e)
				}
				if crash == "after_commit" {
					if _, e = s.CommitModelAuthorizationSchedule(op.ID, b, time.Now().Add(time.Hour).Format(time.RFC3339), "", policy.Revision); e != nil {
						t.Fatal(e)
					}
				}
			}
			// Neither construction nor productionModels.Recover may touch this backend,
			// even when a committed replacement has an exact cleanup intent outstanding.
			secrets := &codingForbiddenSecrets{}
			models, engine, err := newCodingRuntime(s, t.TempDir(), secrets)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			defer models.acceptance.Close()
			defer models.siwc.Close()
			simulation, err := accounts.NewSIWCSimulation(models.accounts, accounts.SIWCOptions{AuthorizationOrigin: issuer.URL, TokenOrigin: issuer.URL, OIDCOrigin: issuer.URL, JWKSOrigin: issuer.URL, RevocationOrigin: issuer.URL, Issuer: issuer.URL, FlowTimeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			defer simulation.Close()
			models.siwc = simulation
			if err = models.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			receipt, err := s.SessionRenewalReceipt(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.GetModelAuthorization(b.ID)
			if err != nil {
				t.Fatal(err)
			}
			if receipt == nil || receipt.Stage != "recovery_required" {
				t.Fatalf("startup left claim unresolved: %+v", receipt)
			}
			if crash == "after_commit" {
				if receipt.Certainty != "confirmed" || receipt.NewGeneration != 2 || current.Generation != 2 || current.State != "available" {
					t.Fatalf("lost committed generation: %+v %+v", receipt, current)
				}
				cleanup, e := s.PendingModelAuthorizationCleanup()
				if e != nil || len(cleanup) != 1 {
					t.Fatalf("startup erased exact cleanup: %+v %v", cleanup, e)
				}
			} else if receipt.Certainty != "unknown" || receipt.NewGeneration != 0 || current.State != "refresh_unknown" || current.Generation != 1 {
				t.Fatalf("unknown claim usable: %+v %+v", receipt, current)
			}
			coordinator := newDesktopSessionRenewal(&models, "darwin", true, true, false, time.Now)
			defer coordinator.Close()
			coordinator.Tick(context.Background())
			status, err := coordinator.Status(a.ID)
			if err != nil || status.State != "recovery_required" {
				t.Fatalf("startup scheduler state: %+v %v", status, err)
			}
			if secrets.calls != 0 || requests.Load() != 0 {
				t.Fatalf("startup effects: secrets=%d requests=%d", secrets.calls, requests.Load())
			}
		})
	}
}

func TestCodingDesktopOptionsAreExplicitAndExclusive(t *testing.T) {
	if yes, err := desktopOptions([]string{"--desktop", "--coding-workbench"}); err != nil || !yes {
		t.Fatal(yes, err)
	}
	for _, args := range [][]string{{"--coding-workbench"}, {"--desktop", "--coding-workbench=true"}, {"--desktop", "--coding-workbench", "--local-mcp-test"}} {
		if _, err := desktopOptions(args); err == nil {
			t.Fatal("accepted", args)
		}
	}
}

type codingForbiddenSecrets struct{ calls int }

func (s *codingForbiddenSecrets) Get(string) (string, error) {
	s.calls++
	return "", model.ErrSecretStoreUnavailable
}
func (s *codingForbiddenSecrets) Set(string, string) error {
	s.calls++
	return model.ErrSecretStoreUnavailable
}
func (s *codingForbiddenSecrets) Delete(string) error {
	s.calls++
	return model.ErrSecretStoreUnavailable
}

func TestCodingStartupAndRecoveryDoNotTouchNativeCredentials(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "pending", Name: "Pending", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "unfinished", SecretRef: "foundry-model-account-pending"}, a.Revision); err != nil {
		t.Fatal(err)
	}
	secrets := &codingForbiddenSecrets{}
	models, e, err := newCodingRuntime(s, t.TempDir(), secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	defer models.siwc.Close()
	defer models.acceptance.Close()
	if err = models.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && e.CodingWorkbenchAvailable() {
		t.Fatal("Windows Docker trust unavailable")
	}
	if secrets.calls != 0 {
		t.Fatal("startup accessed native credentials", secrets.calls)
	}
}

func TestCodingRuntimeMissingOptionalV2PreservesStartup(t *testing.T) {
	t.Setenv("FOUNDRY_VM_V2_BUNDLE", filepath.Join(t.TempDir(), "missing"))
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, e, err := newCodingRuntime(s, t.TempDir(), &codingForbiddenSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	defer p.acceptance.Close()
	defer p.siwc.Close()
	for _, row := range e.CodingProfilesV2() {
		if row.Available {
			t.Fatal("missing optional assets available")
		}
	}
}
func TestCodingRuntimeLoadsInstalledV2WithoutBoot(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture installed platform")
	}
	bundle, _ := filepath.EvalSymlinks(t.TempDir())
	data, _ := filepath.EvalSymlinks(t.TempDir())
	os.Chmod(data, 0700)
	raw, err := os.ReadFile("../../internal/vmruntime/testdata/runtime-inventory-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	d, err := vmruntime.ParseRuntimeInventoryV2(raw)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("synthetic installed asset, never executable")
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	d.Supervisor.Size = int64(len(body))
	d.Supervisor.SHA256 = sum(body)
	raw, err = d.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	m := vmruntime.ManifestV2{SchemaVersion: 2, HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2", Runtime: "runtime/foundry-vm-helper", Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest-v2", RuntimeInventory: vmruntime.RuntimeInventoryPathV2}
	m.RuntimeInventoryDigest, _ = d.Digest()
	for p, b := range map[string][]byte{m.Runtime: body, m.Kernel: body, m.RootFS: body, m.Verifier: body, m.RuntimeInventory: raw} {
		name := filepath.Join(bundle, p)
		os.MkdirAll(filepath.Dir(name), 0700)
		os.WriteFile(name, b, 0600)
		m.Assets = append(m.Assets, vmruntime.Asset{Path: p, Size: int64(len(b)), SHA256: sum(b)})
	}
	raw, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(bundle, "manifest.json"), raw, 0600)
	t.Setenv("FOUNDRY_VM_BUNDLE", "")
	t.Setenv("FOUNDRY_VM_V2_BUNDLE", bundle)
	s, err := store.Open(filepath.Join(data, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, e, err := newCodingRuntime(s, data, &codingForbiddenSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	defer p.acceptance.Close()
	defer p.siwc.Close()
	for _, row := range e.CodingProfilesV2() {
		if !row.Available || row.EmbeddedVM == nil {
			t.Fatal("installed resource not composed", row)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(data, "vm-instances"))
	if len(entries) != 0 {
		t.Fatal("startup launched instance")
	}
	if _, ok := e.CodingVMProfile(); ok {
		t.Fatal("v2 replaced v1 profile")
	}
}
