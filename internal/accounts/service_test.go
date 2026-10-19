package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/store"
)

// Memory Secrets models only the external Keychain boundary, including writes
// that have an effect before returning an error. It never contacts the OS.
type memorySecrets struct {
	mu                  sync.Mutex
	values              map[string]string
	sets, gets, deletes int
	setErr, deleteErr   error
	afterSet            func()
}

func (m *memorySecrets) Set(id, value string) error {
	m.mu.Lock()
	m.sets++
	m.values[id] = value
	err, hook := m.setErr, m.afterSet
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}
func (m *memorySecrets) Get(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	return m.values[id], nil
}
func (m *memorySecrets) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes++
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.values, id)
	return nil
}
func setupAccounts(t *testing.T) (*Service, *store.Store, *memorySecrets, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accounts.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	secrets := &memorySecrets{values: map[string]string{}}
	return New(db, secrets), db, secrets, path
}
func newAccount(t *testing.T, s *Service) domain.ModelAccount {
	t.Helper()
	a, err := s.Save(domain.ModelAccount{Name: "Synthetic account", Provider: "openai", BaseURL: "https://example.invalid/v1", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAccountCASAndImmutableIdentity(t *testing.T) {
	service, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, service)
	if a.ID == "" || a.Revision != 1 || a.CreatedAt == "" || a.UpdatedAt == "" {
		t.Fatalf("bad created metadata: %+v", a)
	}
	edited := a
	edited.Name = "Renamed"
	edited.Enabled = false
	saved, err := service.Save(edited, 1)
	if err != nil || saved.Revision != 2 || saved.Name != "Renamed" || saved.Enabled {
		t.Fatalf("edit: %+v %v", saved, err)
	}
	if _, err := service.Save(a, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	for _, change := range []func(*domain.ModelAccount){func(a *domain.ModelAccount) { a.Provider = "kimi" }, func(a *domain.ModelAccount) { a.BaseURL = "https://other.invalid" }, func(a *domain.ModelAccount) { a.AuthMethod = "oauth" }, func(a *domain.ModelAccount) { a.BillingMode = "subscription" }} {
		bad := saved
		change(&bad)
		if _, err := service.Save(bad, 2); err == nil {
			t.Fatal("immutable identity accepted")
		}
		actual, err := db.GetModelAccount(a.ID)
		if err != nil || actual != saved {
			t.Fatalf("failed update changed state: %+v %v", actual, err)
		}
	}
	if secrets.sets != 0 || secrets.gets != 0 || secrets.deletes != 0 {
		t.Fatal("account metadata touched secrets")
	}
}

func TestCredentialReplayMonotonicVersionsAndFailedWrite(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, s)
	ctx := context.Background()
	first, err := s.AddCredential(ctx, a.ID, a.Revision, "first-write", "SYNTHETIC-ONLY-ONE")
	if err != nil || first.Version != 1 || first.State != "available" {
		t.Fatalf("first version: %+v %v", first, err)
	}
	again, err := s.AddCredential(ctx, a.ID, a.Revision, "first-write", "SYNTHETIC-ONLY-TWO")
	if err != nil || again != first || secrets.values[first.SecretRef] != "SYNTHETIC-ONLY-ONE" || secrets.sets != 1 {
		t.Fatalf("replay changed original: %+v %v sets=%d", again, err, secrets.sets)
	}
	current, _ := db.GetModelAccount(a.ID)
	if current.Revision != 3 || current.CurrentCredentialVersion != 1 {
		t.Fatalf("publication revision: %+v", current)
	}
	secrets.setErr = errors.New("SYNTHETIC-SECRET-ERROR " + first.SecretRef)
	if _, err := s.AddCredential(ctx, a.ID, current.Revision, "second-write", "SYNTHETIC-ONLY-TWO"); err == nil || strings.Contains(err.Error(), "SYNTHETIC") || strings.Contains(err.Error(), first.SecretRef) {
		t.Fatalf("unsafe write error: %v", err)
	}
	current, _ = db.GetModelAccount(a.ID)
	if current.CurrentCredentialVersion != 1 || current.Revision != 5 {
		t.Fatalf("failed write changed current: %+v", current)
	}
	failed, err := db.GetModelCredentialOperation("second-write")
	if err != nil || failed.Version != 2 || failed.State != "failed" || failed.CleanupPending {
		t.Fatalf("failed intent: %+v %v", failed, err)
	}
	if _, ok := secrets.values[failed.SecretRef]; ok {
		t.Fatal("partially written secret survived cleanup")
	}
	replay, err := s.AddCredential(ctx, a.ID, 1, "second-write", "")
	if err != nil || replay.State != "failed" || secrets.sets != 2 {
		t.Fatalf("failed replay: %+v %v", replay, err)
	}
	secrets.setErr = nil
	third, err := s.AddCredential(ctx, a.ID, current.Revision, "third-write", "SYNTHETIC-ONLY-THREE")
	if err != nil || third.Version != 3 || third.State != "available" {
		t.Fatalf("version reused: %+v %v", third, err)
	}
	if secrets.gets != 0 {
		t.Fatal("lifecycle read secrets")
	}
	other := newAccount(t, s)
	if _, err := s.AddCredential(ctx, other.ID, other.Revision, "first-write", "SYNTHETIC-OTHER"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cross-account replay: %v", err)
	}
	b, _ := json.Marshal([]domain.ModelCredentialVersion{first, failed, third})
	if strings.Contains(string(b), "SYNTHETIC") || strings.Contains(string(b), "foundry-model-account-") {
		t.Fatalf("unsafe JSON: %s", b)
	}
}

func TestAccountAndCredentialValidationHasNoSideEffects(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, s)
	for _, change := range []func(*domain.ModelAccount){
		func(a *domain.ModelAccount) { a.Name = "" }, func(a *domain.ModelAccount) { a.Name = strings.Repeat("x", 201) },
		func(a *domain.ModelAccount) { a.Provider = "other" }, func(a *domain.ModelAccount) { a.AuthMethod = "oauth" }, func(a *domain.ModelAccount) { a.BillingMode = "free" },
		func(a *domain.ModelAccount) { a.BaseURL = "http://example.invalid" }, func(a *domain.ModelAccount) { a.BaseURL = "https://name:password@example.invalid" },
		func(a *domain.ModelAccount) { a.BaseURL = "https://example.invalid/?token=bad" }, func(a *domain.ModelAccount) { a.BaseURL = "https://example.invalid/#fragment" },
		func(a *domain.ModelAccount) { a.BaseURL = "https://example.invalid/?" }, func(a *domain.ModelAccount) { a.BaseURL = "https://example.invalid/#" },
	} {
		bad := a
		bad.ID = ""
		change(&bad)
		if _, err := s.Save(bad, 0); err == nil {
			t.Fatal("invalid account accepted")
		}
	}
	supplied := a
	supplied.ID = "caller-created"
	if _, err := s.Save(supplied, 0); err == nil {
		t.Fatal("caller creation identity accepted")
	}
	for _, input := range []struct{ op, key string }{{"", "key"}, {strings.Repeat("a", 257), "key"}, {"has space", "key"}, {"op", ""}, {"op", strings.Repeat("x", 8193)}, {"op", "key\nsecret"}, {"op", "key\x7fsecret"}, {"op", "key\u0085secret"}} {
		if _, err := s.AddCredential(context.Background(), a.ID, 1, input.op, input.key); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
	actual, _ := db.GetModelAccount(a.ID)
	if actual != a {
		t.Fatal("invalid write changed account")
	}
	versions, _ := db.ListModelCredentials(a.ID)
	if len(versions) != 0 || secrets.sets != 0 {
		t.Fatal("invalid write reserved or wrote credential")
	}
	for _, provider := range []string{"openai", "anthropic", "zhipu", "kimi", "custom"} {
		for _, billing := range []string{"subscription", "api_credits", "metered_api", "unknown"} {
			candidate := a
			candidate.ID = ""
			candidate.Provider = provider
			candidate.BillingMode = billing
			candidate.BaseURL = "http://127.0.0.1:9999/v1"
			candidate.Name = strings.Repeat("界", 200)
			if _, err := s.Save(candidate, 0); err != nil {
				t.Fatalf("allowed provider/billing rejected: %s/%s %v", provider, billing, err)
			}
		}
	}
}

func TestConcurrentCredentialOperationsHaveOneOwner(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, s)
	// Separate Service instances demonstrate durable ownership, not just one mutex.
	services := []*Service{s, New(db, secrets)}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := services[i%2].AddCredential(context.Background(), a.ID, 1, "one-operation", "SYNTHETIC-ONLY")
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, ErrInProgress) {
			t.Fatalf("same-operation race: %v", err)
		}
	}
	if secrets.sets != 1 {
		t.Fatalf("duplicate external writes: %d", secrets.sets)
	}
	current, _ := db.GetModelAccount(a.ID)
	errorsOut := make(chan error, 2)
	for _, op := range []string{"different-a", "different-b"} {
		wg.Add(1)
		go func(op string) {
			defer wg.Done()
			_, err := s.AddCredential(context.Background(), a.ID, current.Revision, op, "SYNTHETIC-NEXT")
			errorsOut <- err
		}(op)
	}
	wg.Wait()
	close(errorsOut)
	conflicts := 0
	for err := range errorsOut {
		if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 || secrets.sets != 2 {
		t.Fatalf("CAS race conflicts=%d writes=%d", conflicts, secrets.sets)
	}
}
