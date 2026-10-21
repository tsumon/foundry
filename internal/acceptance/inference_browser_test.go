package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/httpapi"
	"foundry/internal/model"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Every non-acceptance external boundary is denied, including secret mutation.
type browserSecrets struct{}

func (browserSecrets) Get(string) (string, error) { return "", model.ErrSecretStoreUnavailable }
func (browserSecrets) Set(string, string) error   { return model.ErrSecretStoreUnavailable }
func (browserSecrets) Delete(string) error        { return model.ErrSecretStoreUnavailable }

type browserRuntime struct {
	fixture             acceptance.InferenceBrowserFixture
	url, evidence, stop string
	server              *http.Server
}

func startInferenceBrowser(t *testing.T, scenario, webDir string) browserRuntime {
	t.Helper()
	f := acceptance.NewInferenceBrowserFixture(t, scenario)
	root := t.TempDir()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client := model.NewClient(browserSecrets{})
	e := engine.New(f.Store, client, engine.Options{DataDir: root})
	t.Cleanup(func() { _ = e.Close() })
	base := httpapi.New(f.Store, e, client, browserSecrets{}, httpapi.Options{Acceptance: f.Service, Address: listener.Addr().String(), WebDir: webDir})
	r := browserRuntime{fixture: f, url: "http://" + listener.Addr().String(), evidence: filepath.Join(root, "local-evidence.json"), stop: filepath.Join(root, "stop")}
	var evidenceMu sync.Mutex
	writeEvidence := func() {
		evidenceMu.Lock()
		defer evidenceMu.Unlock()
		reads, sends := f.Counters()
		raw, _ := json.Marshal(map[string]any{"label": "本地模拟", "scenario": scenario, "credential_reads": reads, "synthetic_dispatches": sends})
		if err := os.WriteFile(r.evidence, raw, 0600); err != nil {
			t.Error(err)
		}
	}
	writeEvidence()
	label := `<aside role="status" style="position:fixed;top:0;left:0;right:0;height:60px;box-sizing:border-box;z-index:2147483647;padding:8px 16px;background:#fff3c4;color:#332700;border-bottom:2px solid #a87000;font:14px/20px sans-serif"><strong>本地模拟 · SYNTHETIC INFERENCE FIXTURE</strong> — ` + scenario + `<br>临时数据库、合成凭据与传输；不代表真实订阅验收。设置 → 模型 → production。</aside>`
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != listener.Addr().String() {
			http.Error(w, "local fixture host only", http.StatusForbidden)
			return
		}
		if req.Method != "GET" && req.Method != "HEAD" && req.URL.Path != "/api/session" && !strings.HasPrefix(req.URL.Path, "/api/model-accounts/"+f.Account.ID+"/acceptance-operations") {
			http.Error(w, "local fixture permits acceptance operations only", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(req.URL.Path, "/api/") && !strings.HasPrefix(req.URL.Path, "/assets/") && webDir != "" {
			raw, err := os.ReadFile(filepath.Join(webDir, "index.html"))
			if err != nil {
				http.Error(w, "build frontend first", 503)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(bytes.Replace(raw, []byte("<body>"), []byte(`<body style="padding-top:60px">`+label), 1))
			return
		}
		defer writeEvidence()
		base.ServeHTTP(w, req)
	})
	r.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	t.Cleanup(func() { _ = r.server.Close() })
	go func() { _ = r.server.Serve(listener) }()
	return r
}

func browserRequest(t *testing.T, client *http.Client, r browserRuntime, method, path string, body any) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, r.url+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", r.url)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("local HTTP %s %s: %d %s %v", method, path, resp.StatusCode, raw, err)
	}
	return raw
}

func TestInferenceBrowserHTTPBoundary(t *testing.T) {
	r := startInferenceBrowser(t, "pending", "")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	browserRequest(t, client, r, "POST", "/api/session", map[string]any{})
	base := "/api/model-accounts/" + r.fixture.Account.ID + "/acceptance-operations"
	raw := browserRequest(t, client, r, "GET", base, nil)
	var h struct {
		Enabled    bool                              `json:"production_inference_enabled"`
		Operations []domain.ModelAcceptanceOperation `json:"operations"`
	}
	if err := json.Unmarshal(raw, &h); err != nil || !h.Enabled || len(h.Operations) != 3 {
		t.Fatalf("history %s %v", raw, err)
	}
	if reads, sends := r.fixture.Counters(); reads != 0 || sends != 0 {
		t.Fatal("status dispatched")
	}
	o := r.fixture.Operation
	var result struct {
		Operation domain.ModelAcceptanceOperation `json:"operation"`
	}
	raw = browserRequest(t, client, r, "POST", base+"/"+o.ID+"/decision", map[string]any{"expected_revision": o.Revision, "material_digest": o.MaterialDigest, "decision": "approve"})
	if err := json.Unmarshal(raw, &result); err != nil || result.Operation.State != "approved" {
		t.Fatalf("approve %s", raw)
	}
	if reads, sends := r.fixture.Counters(); reads != 0 || sends != 0 {
		t.Fatal("approval dispatched")
	}
	o = result.Operation
	raw = browserRequest(t, client, r, "POST", base+"/"+o.ID+"/execute", map[string]any{"expected_revision": o.Revision, "material_digest": o.MaterialDigest})
	if err := json.Unmarshal(raw, &result); err != nil || result.Operation.State != "succeeded" || result.Operation.OutputMatched == nil || !*result.Operation.OutputMatched {
		t.Fatalf("execute %s", raw)
	}
	if reads, sends := r.fixture.Counters(); reads != 1 || sends != 1 {
		t.Fatal("wrong dispatch count")
	}
	browserRequest(t, client, r, "GET", base+"/"+o.ID, nil)
	if reads, sends := r.fixture.Counters(); reads != 1 || sends != 1 {
		t.Fatal("receipt read dispatched")
	}
}

// Explicit opt-in only. All state belongs to this test process and is deleted
// by testing cleanup when the stop file is created or SIGINT/SIGTERM arrives.
func TestInferenceBrowserInteractive(t *testing.T) {
	if os.Getenv("FOUNDRY_INFERENCE_BROWSER") != "1" {
		t.Skip("opt-in local browser fixture")
	}
	scenario := os.Getenv("FOUNDRY_INFERENCE_SCENARIO")
	if scenario == "" {
		scenario = "pending"
	}
	webDir := os.Getenv("FOUNDRY_INFERENCE_WEB_DIR")
	if webDir == "" {
		webDir = "../../web/dist"
	}
	webDir, err := filepath.Abs(webDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(webDir, "index.html")); err != nil {
		t.Fatal("run npm run build in web before launching")
	}
	r := startInferenceBrowser(t, scenario, webDir)
	raw, _ := json.Marshal(map[string]any{"label": "本地模拟", "scenario": scenario, "url": r.url, "account": r.fixture.Account.ID, "operation": r.fixture.Operation.ID, "evidence_file": r.evidence, "stop_file": r.stop})
	fmt.Println(string(raw))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := os.Stat(r.stop); err == nil {
				return
			}
		}
	}
}
