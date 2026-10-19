package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/store"
)

const acceptanceKey = "SYNTHETIC-R5A-NOT-A-REAL-KEY-"

// The only external-write seam: keep a memory ledger across actual Store reopen.
// afterSet runs after the external value exists but before Service can commit it.
type accountAcceptanceSecrets struct {
	mu         sync.Mutex
	values     map[string]string
	sets, gets int
	deletes    []string
	afterSet   func() error
	deleteErr  error
}

func (s *accountAcceptanceSecrets) Set(ref, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets++
	s.values[ref] = value
	if s.afterSet != nil {
		return s.afterSet()
	}
	return nil
}
func (s *accountAcceptanceSecrets) Get(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	return s.values[ref], nil
}
func (s *accountAcceptanceSecrets) Delete(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, ref)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.values, ref)
	return nil
}

type accountHTTPAcceptance struct {
	*mcpHTTPFixture // Reuse the authenticated real-HTTP request helpers only.
	ledger          *accountAcceptanceSecrets
	provider        *httptest.Server
	providerCalls   atomic.Int64
	dropNext        atomic.Bool
	dropped         chan int
}

func newAccountHTTPAcceptance(t *testing.T) *accountHTTPAcceptance {
	t.Helper()
	f := &accountHTTPAcceptance{ledger: &accountAcceptanceSecrets{values: map[string]string{}}, dropped: make(chan int, 1)}
	f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.providerCalls.Add(1)
		http.Error(w, "unexpected synthetic provider request", 500)
	}))
	t.Cleanup(f.provider.Close)
	dbPath := filepath.Join(t.TempDir(), "accounts.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := &fixture{t: t, store: s, dataDir: t.TempDir()}
	client := model.NewClient(f.ledger)
	e := engine.New(s, client, engine.Options{DataDir: base.dataDir, Runner: idleRunner{}, MCPSecrets: f.ledger})
	t.Cleanup(func() { _ = e.Close() })
	h := httptest.NewUnstartedServer(nil)
	base.handler = New(s, e, client, f.ledger, Options{Address: h.Listener.Addr().String(), Accounts: accounts.New(s, f.ledger)})
	h.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/credentials") && f.dropNext.CompareAndSwap(true, false) {
			// Let the real handler finish/commit, then close the socket without
			// sending its successful response. No production failure flag exists.
			recorder := httptest.NewRecorder()
			base.handler.ServeHTTP(recorder, r)
			f.dropped <- recorder.Code
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		base.handler.ServeHTTP(w, r)
	})
	h.Start()
	t.Cleanup(h.Close)
	f.mcpHTTPFixture = &mcpHTTPFixture{fixture: base, http: h, engine: e, dbPath: dbPath}
	response, body := f.send("POST", "/api/session", `{}`, false)
	if response.StatusCode != 200 || len(response.Cookies()) != 1 {
		t.Fatalf("session: %d %s", response.StatusCode, body)
	}
	f.cookie = response.Cookies()[0]
	return f
}

func decodeAccountAcceptance[T any](t *testing.T, raw string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func (f *accountHTTPAcceptance) ok(method, path string, body any) string {
	f.t.Helper()
	raw := f.mcpHTTPFixture.ok(method, path, body)
	if strings.Contains(raw, acceptanceKey) || strings.Contains(raw, "foundry-model-account-") || strings.Contains(raw, "secret_ref") {
		f.t.Fatal("private data in public JSON")
	}
	return raw
}
func (f *accountHTTPAcceptance) account() domain.ModelAccount {
	f.t.Helper()
	raw := f.ok("POST", "/api/model-accounts", map[string]any{"name": "SYNTHETIC frozen name", "provider": "custom", "base_url": f.provider.URL, "auth_method": "api_key", "billing_mode": "subscription"})
	return decodeAccountAcceptance[struct {
		Account domain.ModelAccount `json:"account"`
	}](f.t, raw).Account
}
func (f *accountHTTPAcceptance) credential(a domain.ModelAccount, op string) domain.ModelCredentialVersion {
	f.t.Helper()
	raw := f.ok("POST", "/api/model-accounts/"+a.ID+"/credentials", map[string]any{"expected_revision": a.Revision, "operation_id": op, "key": acceptanceKey + op})
	return decodeAccountAcceptance[struct {
		Operation domain.ModelCredentialVersion `json:"operation"`
	}](f.t, raw).Operation
}
func (f *accountHTTPAcceptance) current(id string) domain.ModelAccount {
	f.t.Helper()
	raw := f.ok("GET", "/api/model-accounts/"+id, nil)
	return decodeAccountAcceptance[struct {
		Account domain.ModelAccount `json:"account"`
	}](f.t, raw).Account
}
func (f *accountHTTPAcceptance) edit(id, name string, enabled bool) {
	f.t.Helper()
	a := f.current(id)
	f.ok("PUT", "/api/model-accounts/"+id, map[string]any{"name": name, "enabled": enabled, "expected_revision": a.Revision})
}
func (f *accountHTTPAcceptance) profile(accountID string) domain.Profile {
	f.t.Helper()
	raw := f.ok("POST", "/api/profiles", map[string]any{"name": "SYNTHETIC profile", "account_id": accountID, "kind": "compatible", "protocol": "chat_completions", "base_url": f.provider.URL, "model": "synthetic"})
	return decodeAccountAcceptance[struct {
		Profile domain.Profile `json:"profile"`
	}](f.t, raw).Profile
}
func (f *accountHTTPAcceptance) mission(profileID, repo string) domain.Mission {
	f.t.Helper()
	f.ok("PUT", "/api/settings", domain.Settings{DefaultProfileID: profileID})
	raw := f.ok("POST", "/api/missions", engine.CreateRequest{Title: "SYNTHETIC acceptance task", Goal: "Keep frozen history", RepoPath: repo, Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "synthetic:local", MaxCalls: 4}})
	id := decodeAccountAcceptance[struct {
		Mission struct {
			ID string `json:"id"`
		} `json:"mission"`
	}](f.t, raw).Mission.ID
	m, err := f.store.GetMission(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return m
}
func accountAcceptanceRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src/main.txt"), []byte("synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "synthetic base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if raw, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("temporary git fixture: %v %s", err, raw)
		}
	}
	return repo
}
func accountAcceptanceMissionBytes(t *testing.T, path, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT data FROM missions WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, acceptanceKey) || strings.Contains(raw, "foundry-model-account-") || strings.Contains(raw, "secret_ref") {
		t.Fatal("private data in stored mission JSON")
	}
	return raw
}
func (f *accountHTTPAcceptance) assertNoDisclosureOrCalls() {
	f.t.Helper()
	for _, route := range []string{"/api/model-accounts", "/api/profiles", "/api/missions"} {
		f.ok("GET", route, nil)
	}
	all, err := f.store.ListModelAccounts()
	if err != nil {
		f.t.Fatal(err)
	}
	for _, a := range all {
		f.ok("GET", "/api/model-accounts/"+a.ID+"/credentials", nil)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(f.dbPath + suffix)
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			f.t.Fatal(err)
		}
		if strings.Contains(string(raw), acceptanceKey) {
			f.t.Fatal("secret value in SQLite main/WAL")
		}
	}
	events, err := f.store.Events(0, 1000)
	if err != nil {
		f.t.Fatal(err)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		f.t.Fatal(err)
	}
	if strings.Contains(string(raw), acceptanceKey) || strings.Contains(string(raw), "foundry-model-account-") {
		f.t.Fatal("private data in events")
	}
	f.ledger.mu.Lock()
	defer f.ledger.mu.Unlock()
	if f.ledger.gets != 0 || f.providerCalls.Load() != 0 {
		f.t.Fatalf("hidden secret reads=%d provider calls=%d", f.ledger.gets, f.providerCalls.Load())
	}
}

// Catches follow-current reinterpretation, treating disable/revoke as snapshot
// corruption, and a denied action consuming budget or issuing hidden I/O.
func TestModelAccountHTTPFrozenLifecycleAcceptance(t *testing.T) {
	f := newAccountHTTPAcceptance(t)
	a := f.account()
	f.credential(a, "v1")
	p := f.profile(a.ID)
	repo := accountAcceptanceRepo(t)
	m := f.mission(p.ID, repo)
	before := accountAcceptanceMissionBytes(t, f.dbPath, m.ID)
	eventsBefore, err := f.store.Events(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	f.credential(f.current(a.ID), "v2")
	f.edit(a.ID, "SYNTHETIC live renamed", true)
	if err := f.store.CheckModelCall(m.ID, domain.Worker); err != nil {
		t.Fatalf("rotation/rename invalidated available V1: %v", err)
	}
	response, raw := f.send("POST", "/api/missions/"+m.ID+"/plan", fmt.Sprintf(`{"revision":%d}`, m.Revision), true)
	if response.StatusCode != 409 || !strings.Contains(raw, "r5a_account_inference_disabled") {
		t.Fatalf("production planning: %d %s", response.StatusCode, raw)
	}
	response, raw = f.send("POST", "/api/profiles/"+p.ID+"/probe", `{}`, true)
	if response.StatusCode != 409 || !strings.Contains(raw, "r5a_account_inference_disabled") {
		t.Fatalf("production probe: %d %s", response.StatusCode, raw)
	}
	f.edit(a.ID, "SYNTHETIC live renamed", false)
	if !errors.Is(f.store.CheckModelCall(m.ID, domain.Worker), store.ErrModelAccountUnavailable) {
		t.Fatal("disabled account remained available")
	}
	response, raw = f.send("POST", "/api/missions/"+m.ID+"/plan", fmt.Sprintf(`{"revision":%d}`, m.Revision), true)
	if response.StatusCode != 409 || !strings.Contains(raw, "model_account_unavailable") {
		t.Fatalf("disabled planning: %d %s", response.StatusCode, raw)
	}
	f.edit(a.ID, "SYNTHETIC live renamed", true)
	f.ok("POST", "/api/model-accounts/"+a.ID+"/credentials/1/revoke", map[string]any{"expected_revision": f.current(a.ID).Revision})
	if !errors.Is(f.store.CheckModelCall(m.ID, domain.Worker), store.ErrModelAccountUnavailable) {
		t.Fatal("revoked frozen V1 followed live V2")
	}
	response, raw = f.send("POST", "/api/missions/"+m.ID+"/plan", fmt.Sprintf(`{"revision":%d}`, m.Revision), true)
	if response.StatusCode != 409 || !strings.Contains(raw, "model_account_unavailable") {
		t.Fatalf("revoked planning: %d %s", response.StatusCode, raw)
	}
	if got := f.current(a.ID); got.CurrentCredentialVersion != 2 || !got.Enabled {
		t.Fatalf("revoking historical V1 damaged current account: %+v", got)
	}
	raw = f.ok("GET", "/api/missions/"+m.ID, nil)
	view := decodeAccountAcceptance[struct {
		Mission SafeMissionView `json:"mission"`
	}](t, raw).Mission
	if view.ModelSnapshotStatus != "valid" || view.ModelSnapshot == nil || len(view.ModelSnapshot.Roles) != 7 {
		t.Fatal("historical snapshot became corrupt/unreadable")
	}
	for role, selection := range view.ModelSnapshot.Roles {
		if selection.Profile.CredentialVersion != 1 || selection.Account == nil || selection.Account.Name != "SYNTHETIC frozen name" || selection.Account.Revision != 3 || selection.Account.IdentityStatus != "unverified" || selection.Account.UsageStatus != "unknown" || selection.Account.InferenceEnabled {
			t.Fatalf("role %s reinterpreted frozen history", role)
		}
	}
	if strings.Contains(raw, "base_url") || strings.Contains(raw, "SYNTHETIC live renamed") {
		t.Fatal("history exposed endpoint or live metadata")
	}
	if accountAcceptanceMissionBytes(t, f.dbPath, m.ID) != before {
		t.Fatal("metadata change or denied action mutated persisted mission/budget")
	}
	eventsAfter, err := f.store.Events(0, 1000)
	if err != nil || !reflect.DeepEqual(eventsBefore, eventsAfter) {
		t.Fatalf("denied action mutated event log: %v", err)
	}
	next := f.mission(p.ID, repo)
	if next.Profiles[domain.Worker].CredentialVersion != 2 || f.store.CheckModelCall(next.ID, domain.Worker) != nil {
		t.Fatal("new mission failed to freeze still-available V2")
	}
	f.assertNoDisclosureOrCalls()
}

// Catches returning ambiguous success as failure without durable lookup, or
// terminal replay allocating/writing another credential after response loss.
func TestModelAccountHTTPLostCommittedResponseAcceptance(t *testing.T) {
	f := newAccountHTTPAcceptance(t)
	a := f.account()
	path := "/api/model-accounts/" + a.ID + "/credentials"
	body := `{"expected_revision":1,"operation_id":"lost-response","key":"` + acceptanceKey + `lost"}`
	f.dropNext.Store(true)
	req, err := http.NewRequest("POST", f.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", f.http.URL)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(f.cookie)
	response, err := f.http.Client().Do(req)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("injected socket loss was not observed by caller")
	}
	select {
	case code := <-f.dropped:
		if code != 200 {
			t.Fatalf("lost response was not a committed success: %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("transport failed before reaching the committed-response fault")
	}
	t.Log("fault observed: committed 200 discarded; caller received transport error")
	raw := f.ok("GET", "/api/model-accounts/"+a.ID+"/operations/lost-response", nil)
	op := decodeAccountAcceptance[struct {
		Operation domain.ModelCredentialVersion `json:"operation"`
	}](t, raw).Operation
	if op.Version != 1 || op.State != "available" || op.OperationID != "lost-response" {
		t.Fatalf("original operation not recoverable: %+v", op)
	}
	before := f.current(a.ID)
	response, replay := f.send("POST", path, strings.Replace(body, acceptanceKey+"lost", acceptanceKey+"replacement", 1), true)
	if response.StatusCode != 200 || replay != raw {
		t.Fatal("explicit backend replay did not return original result")
	}
	if after := f.current(a.ID); !reflect.DeepEqual(before, after) || after.Revision != 3 {
		t.Fatal("terminal replay advanced account")
	}
	versions, err := f.store.ListModelCredentials(a.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("replay allocated extra version: %v", err)
	}
	f.ledger.mu.Lock()
	if f.ledger.sets != 1 || f.ledger.values[versions[0].SecretRef] != acceptanceKey+"lost" {
		t.Error("lost response replay overwrote/reissued external write")
	}
	f.ledger.mu.Unlock()
	f.assertNoDisclosureOrCalls()
}

// Unlike recorder-only account creation tests, rejected requests target a real
// existing credential route with valid CAS, proving they cannot reserve intent.
func TestModelAccountHTTPCredentialDenialBeforeWriteAcceptance(t *testing.T) {
	f := newAccountHTTPAcceptance(t)
	a := f.account()
	path := "/api/model-accounts/" + a.ID + "/credentials"
	body := `{"expected_revision":1,"operation_id":"denied","key":"` + acceptanceKey + `denied"}`
	for _, tc := range []struct {
		name, origin, content, body string
		auth                        bool
		want                        int
	}{
		{"cookie", f.http.URL, "application/json", body, false, 401},
		{"origin", "https://attacker.invalid", "application/json", body, true, 403},
		{"content", f.http.URL, "text/plain", body, true, 403},
		{"duplicate", f.http.URL, "application/json", strings.Replace(body, `"operation_id":"denied"`, `"operation_id":"denied","operation_id":"other"`, 1), true, 400},
		{"unknown", f.http.URL, "application/json", strings.TrimSuffix(body, "}") + `,"secret_ref":"caller-ref"}`, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", f.http.URL+path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", tc.content)
			if tc.auth {
				req.AddCookie(f.cookie)
			}
			response, err := f.http.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.want || strings.Contains(string(raw), acceptanceKey) {
				t.Fatalf("denial status=%d want=%d or unsafe response", response.StatusCode, tc.want)
			}
			if got := f.current(a.ID); !reflect.DeepEqual(got, a) {
				t.Fatal("denied request advanced account")
			}
			versions, err := f.store.ListModelCredentials(a.ID)
			if err != nil || len(versions) != 0 {
				t.Fatalf("denial reserved intent: %v", err)
			}
		})
	}
	f.ledger.mu.Lock()
	writes := f.ledger.sets
	f.ledger.mu.Unlock()
	if writes != 0 {
		t.Fatal("denied request wrote external secret")
	}
	f.assertNoDisclosureOrCalls()
}

// Catches startup claiming success after failed cleanup, rewriting old current
// or mission bytes, and replaying the interrupted HTTP write after actual reopen.
func TestModelAccountHTTPInterruptedWriteReopenAcceptance(t *testing.T) {
	f := newAccountHTTPAcceptance(t)
	a := f.account()
	f.credential(a, "stable")
	repo := accountAcceptanceRepo(t)
	m := f.mission(f.profile(a.ID).ID, repo)
	legacy := f.mission(f.profile("").ID, repo)
	before := map[string]string{m.ID: accountAcceptanceMissionBytes(t, f.dbPath, m.ID), legacy.ID: accountAcceptanceMissionBytes(t, f.dbPath, legacy.ID)}
	eventsBefore, err := f.store.Events(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	stable, err := f.store.GetModelCredential(a.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.ledger.mu.Lock()
	f.ledger.afterSet = f.store.Close
	f.ledger.mu.Unlock()
	response, raw := f.send("POST", "/api/model-accounts/"+a.ID+"/credentials", `{"expected_revision":3,"operation_id":"crashed","key":"`+acceptanceKey+`crashed"}`, true)
	if response.StatusCode != 503 || strings.Contains(raw, acceptanceKey) {
		t.Fatalf("after-Set close fault: %d unsafe=%v", response.StatusCode, strings.Contains(raw, acceptanceKey))
	}
	t.Log("fault observed: SQLite closed after external Set; HTTP returned safe 503")
	f.http.Close()
	if err := f.engine.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f.store = s
	pending, err := s.GetModelCredentialOperation("crashed")
	if err != nil || pending.State != "pending" || pending.Version != 2 {
		t.Fatalf("durable interrupted intent: %+v %v", pending, err)
	}
	f.ledger.mu.Lock()
	f.ledger.afterSet = nil
	f.ledger.deleteErr = errors.New(acceptanceKey + "unsafe-cleanup-error " + pending.SecretRef)
	if f.ledger.values[pending.SecretRef] != acceptanceKey+"crashed" {
		t.Error("fault did not occur after real external memory write")
	}
	f.ledger.mu.Unlock()
	service := accounts.New(s, f.ledger)
	if err := service.Recover(context.Background()); !errors.Is(err, accounts.ErrUnavailable) {
		t.Fatalf("failed cleanup did not block readiness safely: %v", err)
	}
	interrupted, err := s.GetModelCredentialOperation("crashed")
	if err != nil || interrupted.State != "interrupted" || !interrupted.CleanupPending {
		t.Fatalf("cleanup intent lost: %+v %v", interrupted, err)
	}
	accountBeforeRetry, err := s.GetModelAccount(a.ID)
	if err != nil || accountBeforeRetry.CurrentCredentialVersion != 1 || accountBeforeRetry.Revision != 5 {
		t.Fatalf("old current advanced: %+v %v", accountBeforeRetry, err)
	}
	// The failed-cleanup intent must survive a second process/store lifetime too.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f.store = s
	service = accounts.New(s, f.ledger)
	f.ledger.mu.Lock()
	f.ledger.deleteErr = nil
	f.ledger.mu.Unlock()
	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished, err := s.GetModelCredentialOperation("crashed")
	if err != nil || finished.State != "interrupted" || finished.CleanupPending || finished.Version != 2 {
		t.Fatalf("cleanup retry: %+v %v", finished, err)
	}
	afterRetry, err := s.GetModelAccount(a.ID)
	if err != nil || !reflect.DeepEqual(accountBeforeRetry, afterRetry) {
		t.Fatal("cleanup retry duplicated state transition")
	}
	client := model.NewClient(f.ledger)
	e := engine.New(s, client, engine.Options{DataDir: f.dataDir, Runner: idleRunner{}, MCPSecrets: f.ledger})
	defer e.Close()
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, expected := range before {
		if accountAcceptanceMissionBytes(t, f.dbPath, id) != expected {
			t.Fatal("reopen rewrote frozen or legacy history")
		}
	}
	eventsAfter, err := s.Events(0, 1000)
	if err != nil || !reflect.DeepEqual(eventsBefore, eventsAfter) {
		t.Fatalf("recovery rewrote mission events: %v", err)
	}
	// Open a new authenticated launch session only after both recovery gates pass.
	h := httptest.NewUnstartedServer(nil)
	h.Config.Handler = New(s, e, client, f.ledger, Options{Address: h.Listener.Addr().String(), Accounts: service})
	h.Start()
	defer h.Close()
	f.http = h
	response, raw = f.send("POST", "/api/session", `{}`, false)
	if response.StatusCode != 200 || len(response.Cookies()) != 1 {
		t.Fatal("new launch session failed")
	}
	f.cookie = response.Cookies()[0]
	raw = f.ok("GET", "/api/model-accounts/"+a.ID+"/operations/crashed", nil)
	response, replay := f.send("POST", "/api/model-accounts/"+a.ID+"/credentials", `{"expected_revision":3,"operation_id":"crashed","key":"`+acceptanceKey+`replay"}`, true)
	if response.StatusCode != 200 || replay != raw {
		t.Fatal("interrupted operation replay changed original result")
	}
	versions, err := s.ListModelCredentials(a.ID)
	if err != nil || len(versions) != 2 || !reflect.DeepEqual(f.current(a.ID), accountBeforeRetry) {
		t.Fatal("interrupted replay allocated or advanced account state")
	}
	if err := s.CheckModelCall(m.ID, domain.Worker); err != nil {
		t.Fatalf("recovery made stable V1 unavailable: %v", err)
	}
	f.ledger.mu.Lock()
	if f.ledger.sets != 2 || f.ledger.values[stable.SecretRef] != acceptanceKey+"stable" || len(f.ledger.values) != 1 || !reflect.DeepEqual(f.ledger.deletes, []string{pending.SecretRef, pending.SecretRef}) {
		t.Error("recovery replayed write, touched available secret, or repeated successful cleanup")
	}
	f.ledger.mu.Unlock()
	f.assertNoDisclosureOrCalls()
}
