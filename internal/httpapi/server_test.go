package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
)

const testOrigin = "http://127.0.0.1:4317"

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
}

type interleavingSecrets struct {
	mu           sync.Mutex
	values       map[string]string
	aEntered     chan struct{}
	bEntered     chan struct{}
	allowAWrite  chan struct{}
	allowBReturn chan struct{}
}

func newInterleavingSecrets() *interleavingSecrets {
	return &interleavingSecrets{
		values: map[string]string{}, aEntered: make(chan struct{}), bEntered: make(chan struct{}),
		allowAWrite: make(chan struct{}), allowBReturn: make(chan struct{}),
	}
}

func (s *interleavingSecrets) Set(id, value string) error {
	switch value {
	case "key-a":
		close(s.aEntered)
		<-s.allowAWrite
		s.mu.Lock()
		s.values[id] = value
		s.mu.Unlock()
		return nil
	case "key-b":
		close(s.bEntered)
		s.mu.Lock()
		s.values[id] = value
		s.mu.Unlock()
		<-s.allowBReturn
		return nil
	default:
		return errors.New("unexpected test credential")
	}
}

func (s *interleavingSecrets) Get(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	if !ok {
		return "", errors.New("missing")
	}
	return value, nil
}

func (s *interleavingSecrets) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, id)
	return nil
}

func (s *memorySecrets) Set(id, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[id] = value
	return nil
}

func (s *memorySecrets) Get(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	if !ok {
		return "", errors.New("missing")
	}
	return value, nil
}

func (s *memorySecrets) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, id)
	return nil
}

type idleRunner struct{}

func (idleRunner) Resolve(context.Context, string) (string, error) { return "sha256:test", nil }
func (idleRunner) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	return domain.CommandResult{}, errors.New("not used")
}
func (idleRunner) Reconcile(context.Context) error { return nil }

var _ sandbox.Runner = idleRunner{}

type fixture struct {
	dataDir   string
	storePath string
	t         *testing.T
	store     *store.Store
	secrets   *memorySecrets
	handler   http.Handler
	cookie    *http.Cookie
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "foundry.db")
	s, err := store.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	secrets := &memorySecrets{}
	client := model.NewClient(secrets)
	dataDir := t.TempDir()
	e := engine.New(s, client, engine.Options{DataDir: dataDir, Runner: idleRunner{}})
	t.Cleanup(func() { _ = e.Close() })
	f := &fixture{t: t, store: s, storePath: storePath, secrets: secrets, dataDir: dataDir}
	f.handler = New(s, e, client, secrets, Options{Address: "127.0.0.1:4317"})
	return f
}

func (f *fixture) request(method, target, body string, authenticated bool) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, testOrigin+target, strings.NewReader(body))
	req.Host = "127.0.0.1:4317"
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if authenticated && f.cookie != nil {
		req.AddCookie(f.cookie)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *fixture) authenticate() {
	f.t.Helper()
	rr := f.request(http.MethodPost, "/api/session", `{}`, false)
	if rr.Code != http.StatusOK {
		f.t.Fatalf("bootstrap status=%d body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		f.t.Fatalf("cookies=%d", len(cookies))
	}
	f.cookie = cookies[0]
	if !f.cookie.HttpOnly || f.cookie.SameSite != http.SameSiteStrictMode {
		f.t.Fatalf("insecure cookie: %#v", f.cookie)
	}
}

func TestAuthenticationRejectsUnauthenticatedReadsAndStreams(t *testing.T) {
	f := newFixture(t)
	for _, target := range []string{"/api/health", "/api/missions", "/api/events?after=0"} {
		rr := f.request(http.MethodGet, target, "", false)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s status=%d body=%s", target, rr.Code, rr.Body.String())
		}
	}
}

func TestSessionRequiresExactHostOriginAndFetchMetadata(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name, host, origin, fetchSite string
		want                          int
	}{
		{name: "valid", host: "127.0.0.1:4317", origin: testOrigin, fetchSite: "same-origin", want: http.StatusOK},
		{name: "evil origin", host: "127.0.0.1:4317", origin: "https://attacker.example", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "wrong host", host: "localhost:4317", origin: testOrigin, fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "absent host", host: "", origin: testOrigin, fetchSite: "same-origin", want: http.StatusForbidden},
		{name: "cross site metadata", host: "127.0.0.1:4317", origin: testOrigin, fetchSite: "cross-site", want: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testOrigin+"/api/session", strings.NewReader(`{}`))
			req.Host = tc.host
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			f.handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestAuthenticatedReadUsesLaunchCookie(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	rr := f.request(http.MethodGet, "/api/health", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Status       string          `json:"status"`
		Version      string          `json:"version"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" || response.Version != "0.1.0" {
		t.Fatalf("response=%+v", response)
	}
	for _, capability := range []string{"planning", "execution", "docker_verification", "local_patch_delivery", "model_directory_probe", "magpie_discovery", "server_sent_events"} {
		if !response.Capabilities[capability] {
			t.Errorf("capability %q missing: %+v", capability, response.Capabilities)
		}
	}
}

func TestStaleMissionRevisionMapsToConflict(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	_, err := f.store.CreateMission(domain.Mission{ID: "mission-1", State: "draft", Stage: "brief"})
	if err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodPost, "/api/missions/mission-1/plan", `{"revision":0}`, true)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestJSONMutationsRejectMalformedUnknownAndOversizedBodies(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{name: "malformed", body: `{`, want: http.StatusBadRequest},
		{name: "unknown", body: `{"revision":1,"extra":true}`, want: http.StatusBadRequest},
		{name: "oversized", body: `{"revision":1,"padding":"` + strings.Repeat("x", 2<<20) + `"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.request(http.MethodPost, "/api/missions/missing/plan", tc.body, true)
			if rr.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestProfileKeyIsWriteOnlyAndBlankUpdateRetainsIt(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	body := `{"name":"Local","kind":"direct","protocol":"responses","base_url":"http://127.0.0.1:9999/v1","model":"test-model","api_key":"top-secret"}`
	rr := f.request(http.MethodPost, "/api/profiles", body, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "top-secret") || strings.Contains(rr.Body.String(), "api_key") {
		t.Fatalf("credential echoed: %s", rr.Body.String())
	}
	var created struct {
		Profile domain.Profile `json:"profile"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Profile.ID == "" || !created.Profile.HasKey {
		t.Fatalf("profile=%+v", created.Profile)
	}
	if secret, err := f.secrets.Get(created.Profile.ID); err != nil || secret != "top-secret" {
		t.Fatalf("secret=%q err=%v", secret, err)
	}

	update := `{"id":"` + created.Profile.ID + `","name":"Renamed","kind":"direct","protocol":"responses","base_url":"http://127.0.0.1:9999/v1","model":"test-model","api_key":""}`
	rr = f.request(http.MethodPost, "/api/profiles", update, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}
	if secret, err := f.secrets.Get(created.Profile.ID); err != nil || secret != "top-secret" {
		t.Fatalf("retained secret=%q err=%v", secret, err)
	}
}

func TestUnknownAPIRouteDoesNotFallBackToSPA(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	rr := f.request(http.MethodGet, "/api/misspelled", "", true)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d content-type=%q body=%s", rr.Code, rr.Header().Get("Content-Type"), rr.Body.String())
	}
}

func TestInternalErrorsAreSanitized(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	body := `{"title":"Test","goal":"Test goal","repo_path":"/tmp/repo","spec":{"scope":["src"],"acceptance":["true"],"image":"test","profile_id":"test","max_calls":1}}`
	rr := f.request(http.MethodPost, "/api/missions", body, true)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "database is closed") || strings.Contains(rr.Body.String(), "sql:") {
		t.Fatalf("internal detail leaked: %s", rr.Body.String())
	}
}

func TestDownloadRejectsSymlinkedArtifactDirectory(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "changes.patch"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	missionRoot := filepath.Join(t.TempDir(), "mission-download")
	if err := os.MkdirAll(missionRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(missionRoot, "delivery")); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(missionRoot, "delivery", "changes.patch")
	_, err := f.store.CreateMission(domain.Mission{
		ID: "mission-download", State: "delivered", Stage: "deliver",
		Delivery: &domain.Delivery{PatchPath: artifact, ReportPath: filepath.Join(missionRoot, "delivery", "report.md")},
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/missions/mission-download/patch", "", true)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDownloadsServeOnlyStoredDeliveryPair(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	deliveryDir := filepath.Join(f.dataDir, "missions", "mission-artifacts", "delivery")
	if err := os.MkdirAll(deliveryDir, 0700); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(deliveryDir, "changes.patch")
	reportPath := filepath.Join(deliveryDir, "report.md")
	if err := os.WriteFile(patchPath, []byte("patch-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, []byte("# Report"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.CreateMission(domain.Mission{
		ID: "mission-artifacts", State: "delivered", Stage: "deliver",
		Delivery: &domain.Delivery{PatchPath: patchPath, ReportPath: reportPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body, contentType, filename string
	}{
		{path: "/api/missions/mission-artifacts/patch", body: "patch-data", contentType: "text/plain", filename: "changes.patch"},
		{path: "/api/missions/mission-artifacts/report", body: "# Report", contentType: "text/markdown", filename: "report.md"},
	} {
		rr := f.request(http.MethodGet, tc.path, "", true)
		if rr.Code != http.StatusOK || rr.Body.String() != tc.body || !strings.HasPrefix(rr.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(rr.Header().Get("Content-Disposition"), tc.filename) {
			t.Errorf("%s status=%d headers=%v body=%q", tc.path, rr.Code, rr.Header(), rr.Body.String())
		}
	}
}

func TestProfileRejectsCredentialEndpointMigration(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	create := `{"name":"Local","kind":"direct","protocol":"responses","base_url":"http://127.0.0.1:9999/v1","model":"test-model","api_key":"top-secret"}`
	rr := f.request(http.MethodPost, "/api/profiles", create, true)
	var created struct {
		Profile domain.Profile `json:"profile"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	update := `{"id":"` + created.Profile.ID + `","name":"Moved","kind":"direct","protocol":"responses","base_url":"http://127.0.0.1:9998/v1","model":"test-model","api_key":"replacement"}`
	rr = f.request(http.MethodPost, "/api/profiles", update, true)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if secret, err := f.secrets.Get(created.Profile.ID); err != nil || secret != "top-secret" {
		t.Fatalf("credential changed: %q err=%v", secret, err)
	}
}

func TestConcurrentProfileEditsCannotCrossCredentialOrigins(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	const (
		profileID = "concurrent-profile"
		urlA      = "http://127.0.0.1:9001/v1"
		urlB      = "http://127.0.0.1:9002/v1"
	)
	if err := f.store.PutProfile(domain.Profile{ID: profileID, Name: "Initial", Kind: "direct", Protocol: "responses", BaseURL: urlA, Model: "test"}); err != nil {
		t.Fatal(err)
	}
	controlled := newInterleavingSecrets()
	f.handler.(*server).secrets = controlled

	type result struct {
		name   string
		status int
		body   string
	}
	results := make(chan result, 2)
	save := func(name, baseURL, key string) {
		body := `{"id":"` + profileID + `","name":"` + name + `","kind":"direct","protocol":"responses","base_url":"` + baseURL + `","model":"test","api_key":"` + key + `"}`
		rr := f.request(http.MethodPost, "/api/profiles", body, true)
		results <- result{name: name, status: rr.Code, body: rr.Body.String()}
	}
	go save("A", urlA, "key-a")
	select {
	case <-controlled.aEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first edit did not reach credential storage")
	}
	go save("B", urlB, "key-b")

	bReachedCredentialWrite := false
	select {
	case <-controlled.bEntered:
		bReachedCredentialWrite = true
	case <-time.After(250 * time.Millisecond):
	}
	close(controlled.allowAWrite)
	first := <-results
	close(controlled.allowBReturn)
	second := <-results
	byName := map[string]result{first.name: first, second.name: second}
	if bReachedCredentialWrite || byName["A"].status != http.StatusOK || byName["B"].status != http.StatusConflict || !strings.Contains(byName["B"].body, `"code":"credential_origin"`) {
		t.Fatalf("B reached credential write=%v; A=(%d %s) B=(%d %s)", bReachedCredentialWrite, byName["A"].status, byName["A"].body, byName["B"].status, byName["B"].body)
	}
	profile, err := f.store.GetProfile(profileID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := controlled.Get(profileID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.BaseURL != urlA || credential != "key-a" {
		t.Fatalf("cross-origin pair persisted: base_url=%q credential=%q", profile.BaseURL, credential)
	}
}

func TestInvalidProfileIsValidationError(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	rr := f.request(http.MethodPost, "/api/profiles", `{"name":"Bad","kind":"unknown","protocol":"responses","base_url":"http://127.0.0.1:9999/v1","model":"test"}`, true)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), `"code":"validation"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestProbeAndMagpieDiscoveryReadOnlyModelDirectory(t *testing.T) {
	var methods []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer provider.Close()
	f := newFixture(t)
	f.authenticate()
	profile := domain.Profile{ID: "probe-profile", Name: "Probe", Kind: "compatible", Protocol: "chat_completions", BaseURL: provider.URL, Model: "model-a"}
	if err := f.store.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodPost, "/api/profiles/probe-profile/probe", `{}`, true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"inference_tested":false`) {
		t.Fatalf("probe status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPost, "/api/magpie/discover", `{"base_url":"`+provider.URL+`"}`, true)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"available":true`) || !strings.Contains(rr.Body.String(), `inference was not tested`) {
		t.Fatalf("discover status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, method := range methods {
		if method != "GET /v1/models" {
			t.Fatalf("unexpected provider request: %s", method)
		}
	}
}

func TestEventsReplayCommittedChanges(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	if _, err := f.store.CreateMission(domain.Mission{ID: "mission-event", State: "draft", Stage: "brief"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, testOrigin+"/api/events?after=0", nil).WithContext(ctx)
	req.Host = "127.0.0.1:4317"
	req.AddCookie(f.cookie)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "event: change") || !strings.Contains(rr.Body.String(), `"mission_id":"mission-event"`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
