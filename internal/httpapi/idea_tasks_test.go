package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/vmruntime"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestIdeaTasksSessionAndAvailability(t *testing.T) {
	f := newFixture(t)
	if r := f.request("POST", "/api/idea-tasks", `{"title":"Tool","goal":"Build","acceptance":["works"],"profile_id":"test","max_calls":16}`, false); r.Code != 401 {
		t.Fatal(r.Code, r.Body.String())
	}
	f.authenticate()
	r := f.request("GET", "/api/idea-tasks", "", true)
	var v struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &v) != nil || v.Available || v.Reason != "coding_workbench_disabled" {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = f.request("POST", "/api/idea-tasks", `{}`, true); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	missions, err := f.store.ListMissions()
	if err != nil || len(missions) != 0 {
		t.Fatal("unavailable API created mission", err)
	}
}
func TestIdeaCreateExactClosedRequest(t *testing.T) {
	valid := `{"title":"Tool","goal":"Build","acceptance":["works"],"profile_id":"test","max_calls":16}`
	for _, raw := range []string{strings.Replace(valid, `"title"`, `"Title"`, 1), strings.Replace(valid, `"max_calls":16`, `"max_calls":null`, 1), strings.Replace(valid, `"title":"Tool"`, `"title":"Tool","title":"Other"`, 1), strings.Replace(valid, `"title":"Tool"`, `"title":"Tool","repo_path":"/tmp"`, 1), strings.Replace(valid, `"title":"Tool"`, `"title":"Tool","idea_project":{"schema_version":1}`, 1)} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		var q engine.IdeaCreateRequest
		if decodeCodingJSON(httptest.NewRecorder(), r, &q) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(valid))
	var q engine.IdeaCreateRequest
	if err := decodeCodingJSON(httptest.NewRecorder(), r, &q); err != nil {
		t.Fatal(err)
	}
}

func TestIdeaHTTPManagedCreationPreviewAndPinnedFrame(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	router := model.NewRouter(nil, nil, model.RouterOptions{})
	bundle, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instances, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../vmruntime/testdata/runtime-inventory-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := vmruntime.ParseRuntimeInventoryV2(raw)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("inert managed-idea metadata fixture; never executable")
	sum := sha256.Sum256(body)
	inventory.Supervisor.Size = int64(len(body))
	inventory.Supervisor.SHA256 = hex.EncodeToString(sum[:])
	raw, err = inventory.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	host := runtime.GOOS + "/" + runtime.GOARCH
	guest, executable := "linux/arm64", "runtime/foundry-vm-helper"
	if host == "windows/amd64" {
		guest, executable = "linux/amd64", "runtime/qemu-system-x86_64.exe"
	} else if host != "darwin/arm64" {
		t.Skip("runtime metadata supports Mac arm64 and Windows amd64")
	}
	manifest := vmruntime.ManifestV2{SchemaVersion: 2, HostPlatform: host, GuestPlatform: guest, Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2", Runtime: executable, Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest-v2", RuntimeInventory: vmruntime.RuntimeInventoryPathV2}
	manifest.RuntimeInventoryDigest, _ = inventory.Digest()
	for p, b := range map[string][]byte{manifest.Runtime: body, manifest.Kernel: body, manifest.RootFS: body, manifest.Verifier: body, manifest.RuntimeInventory: raw} {
		name := filepath.Join(bundle, p)
		os.MkdirAll(filepath.Dir(name), 0700)
		if err = os.WriteFile(name, b, 0600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		manifest.Assets = append(manifest.Assets, vmruntime.Asset{Path: p, SHA256: hex.EncodeToString(h[:]), Size: int64(len(b))})
	}
	sort.Slice(manifest.Assets, func(i, j int) bool { return manifest.Assets[i].Path < manifest.Assets[j].Path })
	raw, _ = json.Marshal(manifest)
	os.WriteFile(filepath.Join(bundle, "manifest.json"), raw, 0600)
	vm, err := vmruntime.LoadV2(bundle, instances)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := engine.NewCodingVMCapabilityWithV2(router, nil, vm)
	if err != nil {
		t.Fatal(err)
	}
	account, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "idea-account", Name: "metadata only", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.BeginModelCredential(domain.ModelCredentialVersion{AccountID: account.ID, OperationID: "idea-fixture", SecretRef: "foundry-model-account-not-installed"}, account.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishModelCredential("idea-fixture", "available"); err != nil {
		t.Fatal(err)
	}
	route, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "idea-route", AccountID: account.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "idea-model", Name: "metadata only", Kind: "openai", AccountID: account.ID, RouteID: route.ID, BaseURL: account.BaseURL, Protocol: "responses", Model: "declared-model"}); err != nil {
		t.Fatal(err)
	}
	e := engine.New(f.store, router, engine.Options{DataDir: f.dataDir, C4Controlled: cap, CodingWorkbench: true})
	defer e.Close()
	server := f.handler.(*server)
	server.engine = e
	server.codingWorkbench = true
	r := f.request("GET", "/api/idea-tasks", "", true)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"available":true`) {
		t.Fatal(r.Code, r.Body.String())
	}
	r = f.request("POST", "/api/idea-tasks", `{"title":"Fresh browser app","goal":"Build a simple local app","acceptance":["Works offline"],"profile_id":"idea-model","max_calls":12}`, true)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var response struct {
		Mission domain.Mission `json:"mission"`
	}
	if err = json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	m := response.Mission
	if m.IdeaProject == nil || m.Budget.UsedCalls != 0 || len(m.C4Operations) != 0 || len(m.C4Controls) != 0 {
		t.Fatal("creation executed external work", m)
	}
	p := f.request("GET", "/api/missions/"+m.ID+"/app-preview", "", true)
	if p.Code != 200 {
		t.Fatal(p.Code, p.Body.String())
	}
	var preview engine.AppPreview
	if json.Unmarshal(p.Body.Bytes(), &preview) != nil || preview.CandidateDigest != m.CandidateDigest {
		t.Fatal(p.Body.String())
	}
	frame := f.request("GET", "/api/missions/"+m.ID+"/app-preview/frame?digest="+m.CandidateDigest, "", true)
	if frame.Code != 200 || !strings.HasPrefix(frame.Header().Get("Content-Type"), "text/html") || !strings.Contains(frame.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") || !strings.Contains(frame.Body.String(), "<head><meta http-equiv=") {
		t.Fatal(frame.Code, frame.Body.String(), frame.Header())
	}
	if r = f.request("GET", "/api/missions/"+m.ID+"/app-preview/frame?digest="+strings.Repeat("0", 64), "", true); r.Code != 409 {
		t.Fatal("stale frame admitted", r.Code)
	}
	if r = f.request("GET", "/api/missions/"+m.ID+"/source", "", true); r.Code < 400 {
		t.Fatal("undelivered export admitted", r.Code)
	}
	if r = f.request("GET", "/api/missions/"+m.ID+"/app-html", "", true); r.Code < 400 {
		t.Fatal("undelivered standalone html admitted", r.Code)
	}
	if r = f.request("POST", "/api/missions/"+m.ID+"/idea-continue", `{"title":"v2","goal":"Add filter","acceptance":["Filter works"],"profile_id":"idea-model","max_calls":12}`, false); r.Code != 401 {
		t.Fatal("unauthenticated continue admitted", r.Code)
	}
	if r = f.request("POST", "/api/missions/"+m.ID+"/idea-continue", `{"title":"v2","goal":"Add filter","acceptance":["Filter works"],"profile_id":"idea-model","max_calls":12,"extra":1}`, true); r.Code != 400 {
		t.Fatal("non-closed continue JSON admitted", r.Code)
	}
	cont := f.request("POST", "/api/missions/"+m.ID+"/idea-continue", `{"title":"v2","goal":"Add filter","acceptance":["Filter works"],"profile_id":"idea-model","max_calls":12}`, true)
	if cont.Code != 200 {
		t.Fatal("continue failed", cont.Code, cont.Body.String())
	}
	var contResp struct {
		Mission domain.Mission `json:"mission"`
	}
	if err = json.Unmarshal(cont.Body.Bytes(), &contResp); err != nil || contResp.Mission.IdeaProject == nil || contResp.Mission.IdeaProject.ParentMissionID != m.ID {
		t.Fatalf("unexpected continuation response: %v %s", err, cont.Body.String())
	}
	calls, err := f.store.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 0 {
		t.Fatal("metadata/create/preview invoked model", calls, err)
	}
	entries, err := os.ReadDir(instances)
	if err != nil || len(entries) != 0 {
		t.Fatal("metadata/create/preview launched VM", entries, err)
	}
}
