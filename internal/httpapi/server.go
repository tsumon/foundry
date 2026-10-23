package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/acceptance"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/capability"
	"foundry/internal/catalog"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/hooks"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
)

const (
	version      = "0.1.0"
	maxBodyBytes = 1 << 20
	sessionName  = "foundry_session"
)

type Options struct {
	CodingWorkbench  bool
	C4ControlledOnly bool
	RecoveryError    string
	Acceptance       *acceptance.Service
	RecoveryReadOnly bool
	Accounts         *accounts.Service
	ModelRouter      *model.Router
	SIWC             *accounts.SIWCService
	Address          string
	WebDir           string
	Hooks            *hooks.Service
}

type server struct {
	codingWorkbench  bool
	c4ControlledOnly bool
	recoveryError    string
	acceptance       *acceptance.Service
	recoveryReadOnly bool
	accounts         *accounts.Service
	store            *store.Store
	catalog          *catalog.Service
	capability       *capability.Service
	hooks            *hooks.Service
	engine           *engine.Engine
	client           *model.Client
	modelRouter      *model.Router
	siwc             *accounts.SIWCService
	secrets          model.Secrets
	host             string
	origin           string
	webDir           string
	token            string
	profileMu        sync.Mutex
}

func New(s *store.Store, e *engine.Engine, client *model.Client, secrets model.Secrets, opts Options) http.Handler {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		panic("httpapi: secure session token unavailable")
	}
	host := opts.Address
	if host == "" {
		host = "127.0.0.1:4317"
	}
	webDir := opts.WebDir
	if webDir != "" {
		webDir, _ = filepath.Abs(webDir)
	}
	observer := opts.Hooks
	accountService := opts.Accounts
	if accountService == nil && !opts.C4ControlledOnly {
		accountService = accounts.New(s, secrets)
	}
	modelRouter := opts.ModelRouter
	if modelRouter == nil && !opts.C4ControlledOnly {
		modelRouter = model.NewRouter(client, accountService, model.RouterOptions{})
	}
	siwc := opts.SIWC
	if siwc == nil && !opts.C4ControlledOnly {
		siwc = accounts.NewSIWCService(accountService)
	}
	if observer == nil && !opts.C4ControlledOnly {
		observer = hooks.New(s)
	}
	gates := opts.Acceptance
	if gates == nil && !opts.C4ControlledOnly {
		gates = acceptance.New(s)
	}
	if opts.RecoveryError == "" {
		opts.RecoveryError = "invalid_mission_model_snapshot"
	}
	var capabilities *capability.Service
	if opts.C4ControlledOnly {
		accountService, modelRouter, siwc, observer, gates = nil, nil, nil, nil, nil
		client, secrets = nil, nil
	} else {
		capabilities = capability.New(s, secrets)
	}
	return &server{
		codingWorkbench:  opts.CodingWorkbench,
		c4ControlledOnly: opts.C4ControlledOnly,
		recoveryError:    opts.RecoveryError,
		acceptance:       gates,
		recoveryReadOnly: opts.RecoveryReadOnly,
		accounts:         accountService,
		store:            s, catalog: catalog.New(s), capability: capabilities, hooks: observer, engine: e, client: client, modelRouter: modelRouter, siwc: siwc, secrets: secrets,
		host: host, origin: "http://" + host, webDir: webDir,
		token: hex.EncodeToString(tokenBytes),
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
		s.serveAPI(w, r)
		return
	}
	s.serveStatic(w, r)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'")
}

func (s *server) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Host != s.host {
		writeError(w, http.StatusForbidden, "forbidden", "request host is not allowed")
		return
	}
	if r.URL.Path == "/api/session" {
		s.session(w, r)
		return
	}
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "launch session required")
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if origin := r.Header.Get("Origin"); origin != "" && origin != s.origin {
			writeError(w, http.StatusForbidden, "forbidden", "request origin is not allowed")
			return
		}
	} else if !s.validMutation(r) {
		writeError(w, http.StatusForbidden, "forbidden", "same-origin JSON request required")
		return
	}

	// Session creation above is the sole permitted POST. Every business
	// mutation (including model/probe/tool calls and future routes) fails closed.
	if s.recoveryReadOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusServiceUnavailable, "recovery_read_only", "recovery limited: diagnostic read-only; business mutations and model/tool calls are disabled")
		return
	}
	if s.c4ControlledOnly && !c4ControlledRoute(r.Method, r.URL.Path) {
		writeError(w, http.StatusForbidden, "c4_controlled_only", "isolated C4 runtime does not expose this capability")
		return
	}
	s.dispatchAPI(w, r)
}

// Explicit local-only allowlist: new routes do not inherit external authority.
func c4ControlledRoute(method, path string) bool {
	read := method == http.MethodGet || method == http.MethodHead
	if read {
		switch path {
		case "/api/health", "/api/profiles", "/api/catalog", "/api/catalog/events", "/api/events", "/api/repository-imports/operations":
			return true
		}
	}
	if path == "/api/missions" || path == "/api/repository-imports/inspections" {
		return read || method == http.MethodPost
	}
	if !strings.HasPrefix(path, "/api/missions/") {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, "/api/missions/"), "/")
	if len(p) == 0 || p[0] == "" {
		return false
	}
	if len(p) == 1 {
		return read
	}
	if len(p) != 2 {
		return false
	}
	if read {
		switch p[1] {
		case "c4", "plan-revisions", "local-operations", "recovery-diagnostic", "patch", "report", "invocations":
			return true
		}
		return false
	}
	if method != http.MethodPost {
		return false
	}
	switch p[1] {
	case "c4", "plan", "plan-revisions", "chat", "approve", "pause", "deliver":
		return true
	}
	return false
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if !s.validMutation(r) {
		writeError(w, http.StatusForbidden, "forbidden", "same-origin JSON request required")
		return
	}
	var request struct{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionName, Value: s.token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionName)
	if err != nil || len(cookie.Value) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.token)) == 1
}

func (s *server) validMutation(r *http.Request) bool {
	if r.Header.Get("Origin") != s.origin {
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func (s *server) dispatchAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/repository-imports/operations":
		s.repositoryImportOperations(w, r)
	case r.URL.Path == "/api/repository-imports/inspections":
		s.repositoryInspections(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/mcp-local-test":
		s.localMCPTest(w)
	case r.Method == http.MethodGet && r.URL.Path == "/api/health":
		if s.recoveryReadOnly {
			writeJSON(w, http.StatusOK, map[string]any{
				"status": "recovery_limited", "version": version, "read_only": true,
				"recovery_error":     s.recoveryError,
				"c4_controlled_only": s.c4ControlledOnly,
				"model_runtime":      s.modelRuntimeView(),
				"capabilities": map[string]bool{
					"c4_simulation": false, "c4_real_execution": false, "c4_controlled_simulation": false,
					"model_snapshots": true, "server_sent_events": true,
					"account_management": false, "credential_versions": false,
					"provider_login": false, "account_inference": false, "automatic_paid_fallback": false,
					"planning": false, "execution": false, "docker_verification": false,
					"local_patch_delivery": false, "model_directory_probe": false,
					"magpie_discovery": false, "catalog": true, "package_build": false, "deployment": false,
					"database_migration": false, "mcp_discovery": false, "tool_execution": false,
					"mcp_tool_call": false, "skill_execution": false, "git_write": false,
					"task_local_mcp": false, "controlled_read_creation": false, "controlled_builtin_tools": false,
					"skill_binding": false, "observation_hooks": false, "hook_scripts": false,
					"hook_webhooks": false, "git_hooks": false,
				},
			})
			return
		}
		control := s.engine.C4ControlledStatus(domain.Mission{})
		localTest, err := s.engine.LocalMCPTestSelection()
		if err != nil {
			writeMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "version": version,
			"c4_controlled_only": s.c4ControlledOnly,
			"c4_control":         control,
			"model_runtime":      s.modelRuntimeView(),
			"capabilities": map[string]bool{
				"c4_simulation": s.engine.C4SimulationEnabled(), "c4_real_execution": c4RealExecution(control.SourceKind, control.Expired, s.recoveryReadOnly),
				"c4_controlled_simulation": control.SourceKind == "synthetic_c4_control" && !control.Expired,
				"account_management":       !s.c4ControlledOnly, "credential_versions": !s.c4ControlledOnly, "model_snapshots": true,
				"provider_login": false, "account_inference": false, "automatic_paid_fallback": false,
				"planning": true, "execution": true, "docker_verification": !s.c4ControlledOnly || control.SourceKind == "docker_c4_control" && !control.Expired,
				"local_patch_delivery": true, "model_directory_probe": !s.c4ControlledOnly && !s.codingWorkbench,
				"magpie_discovery": !s.c4ControlledOnly && !s.codingWorkbench, "server_sent_events": true,
				"catalog": true, "package_build": false, "deployment": false,
				"database_migration": false,
				"mcp_discovery":      !s.c4ControlledOnly, "tool_execution": false, "mcp_tool_call": false, "skill_execution": false, "git_write": false,
				"task_local_mcp":           localTest.Enabled,
				"controlled_read_creation": s.engine.ControlledReadCreationEnabled(), "controlled_builtin_tools": !s.c4ControlledOnly, "skill_binding": !s.c4ControlledOnly,
				"observation_hooks": !s.c4ControlledOnly, "hook_scripts": false, "hook_webhooks": false, "git_hooks": false,
			},
		})
	case r.URL.Path == "/api/coding-tasks":
		s.codingTasks(w, r)
	case r.URL.Path == "/api/model-accounts" || strings.HasPrefix(r.URL.Path, "/api/model-accounts/"):
		s.modelAccountsRoute(w, r)
	case r.URL.Path == "/api/model-routes" || strings.HasPrefix(r.URL.Path, "/api/model-routes/"):
		s.modelRoutesRoute(w, r)
	case r.URL.Path == "/api/model-authorizations" || strings.HasPrefix(r.URL.Path, "/api/model-authorizations/"):
		s.modelAuthorizationsRoute(w, r)
	case r.URL.Path == "/api/hooks" || strings.HasPrefix(r.URL.Path, "/api/hooks/"):
		s.hooksRoute(w, r)
	case r.URL.Path == "/api/capabilities" || strings.HasPrefix(r.URL.Path, "/api/capabilities/"):
		s.capabilitiesRoute(w, r)
	case r.URL.Path == "/api/catalog" || strings.HasPrefix(r.URL.Path, "/api/catalog/"):
		s.catalogRoute(w, r)
	case r.URL.Path == "/api/role-model-policy" || strings.HasPrefix(r.URL.Path, "/api/role-model-policy/"):
		s.rolePolicyRoute(w, r)
	case r.URL.Path == "/api/missions" && r.Method == http.MethodGet:
		s.listMissions(w)
	case r.URL.Path == "/api/missions" && r.Method == http.MethodPost:
		s.createMission(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/missions/"):
		s.missionRoute(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/events":
		s.events(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/profiles":
		s.listProfiles(w)
	case r.Method == http.MethodPost && r.URL.Path == "/api/profiles":
		s.saveProfile(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/profiles/"):
		s.profileRoute(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/api/settings":
		s.saveSettings(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/magpie/discover":
		s.discoverMagpie(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON with known fields")
}

func writeMappedError(w http.ResponseWriter, err error) {
	var importErr domain.ImportError
	if errors.As(err, &importErr) {
		status := 500
		switch importErr {
		case domain.ImportInvalidRequest, domain.ImportInvalidRecord, domain.ImportLocation:
			status = 400
		case domain.ImportSourceChanged, domain.ImportIntentConflict, domain.ImportIncomplete, domain.ImportInvalidBinding:
			status = 409
		case domain.ImportTimeout:
			status = 408
		}
		writeError(w, status, string(importErr), "repository import request refused")
		return
	}
	switch {
	case errors.Is(err, engine.ErrC4Unavailable):
		writeError(w, 409, "c4_unavailable", "C4 synthetic capability or frozen authority unavailable; real execution is disabled")
	case errors.Is(err, sandbox.ErrC4Persistence):
		writeError(w, 409, "c4_persistence_unknown", "C4 persistence is unconfirmed; inspect recorded facts, never replay")
	case errors.Is(err, sandbox.ErrC4Execution):
		writeError(w, 409, "c4_not_passed", "C4 did not record a clean synthetic pass; inspect operation outcome and cleanup")
	case errors.Is(err, model.ErrAccountInferenceDisabled):
		writeError(w, http.StatusConflict, "r5a_account_inference_disabled", "account configuration is frozen; real account inference is not enabled")
	case errors.Is(err, store.ErrModelAccountUnavailable):
		writeError(w, 409, "model_account_unavailable", "the frozen account credential is unavailable")
	case errors.Is(err, domain.ErrInvalidModelSnapshot):
		writeError(w, 409, "invalid_mission_model_snapshot", "model snapshot is invalid or unsupported")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", "resource revision is stale")
	case isStateConflict(err):
		writeError(w, http.StatusConflict, "conflict", "operation is not valid in the current state")
	case isValidationError(err):
		writeError(w, http.StatusBadRequest, "validation", "request parameters are invalid")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "operation failed")
	}
}

func isStateConflict(err error) bool {
	message := strings.ToLower(err.Error())
	for _, part := range []string{
		"state", "active", "running", "approval", "approve", "paused", "busy",
		"cannot plan", "cannot deliver", "changed before approval", "does not match the current mission",
		"verification evidence is missing", "review for current candidate is required",
		"local test service", "no unconsumed verified checkpoint",
	} {
		if strings.Contains(message, part) {
			return true
		}
	}
	return false
}

func isValidationError(err error) bool {
	message := strings.ToLower(err.Error())
	for _, part := range []string{
		"unsupported workspace mode",
		"title and bounded goal", "acceptance command", "max_calls", "explicit scope",
		"invalid relative path", "sensitive path", "credential configuration", "at most 12",
		"command too long", "configured profile and model", "provider base url",
		"working git repository", "absolute source", "workspace data must be outside",
		"authorized context and bounded message", "unknown role binding",
		"explicit controlled read consent",
	} {
		if strings.Contains(message, part) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil && r.URL.Query().Get("after") != "" || after < 0 {
		writeError(w, http.StatusBadRequest, "validation", "after must be a non-negative sequence")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "event streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	poll := time.NewTicker(500 * time.Millisecond)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		events, eventErr := s.store.Events(after, 100)
		if eventErr != nil {
			return
		}
		for _, event := range events {
			data, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: change\ndata: %s\n\n", event.Seq, data)
			after = event.Seq
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if s.webDir == "" {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), string(filepath.Separator))
	target := filepath.Join(s.webDir, rel)
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		http.ServeFile(w, r, target)
		return
	}
	index := filepath.Join(s.webDir, "index.html")
	if info, err := os.Stat(index); err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, index)
}
