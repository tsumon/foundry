package main

import (
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestC4ControlsFixtureHTTPIndependentActions(t *testing.T) {
	web := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := startC4ControlsAcceptance(web)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.router != nil || r.siwc != nil || r.mcp != nil || r.protocol != nil {
		t.Fatal("unrelated services constructed")
	}
	if len(r.scenarios) != 2 {
		t.Fatal(r.scenarios)
	}
	if r.manifest().C4SyntheticReadinessCalls != 1 {
		t.Fatal("only failure scenario may seed readiness")
	}
	c := newAcceptanceAPIClient(t, r.URL)
	var res struct {
		Mission domain.Mission `json:"mission"`
	}
	m, _ := r.store.GetMission(r.scenarios["c4_controls_baseline_pending"])
	originalApprovals := append([]domain.Approval(nil), m.Approvals...)
	failed, _ := r.store.GetMission(r.scenarios["c4_controls_cleanup_failure"])
	if m.Spec.C4.ControlVersion != 1 || len(m.C4Controls) != 0 || len(m.C4Operations) != 0 || m.RepositoryImport.BaseDigest != failed.RepositoryImport.BaseDigest || m.RepoPath != failed.RepoPath {
		t.Fatal("not independent pending shared-base fixture")
	}
	route := "/api/missions/" + m.ID + "/c4"
	post := func(body map[string]any, want int) {
		t.Helper()
		c.json("POST", route, body, want, &res)
		if want == 200 {
			m = res.Mission
		}
	}
	var status struct {
		ReadOnly bool `json:"read_only"`
		Real     bool `json:"real_execution"`
		Control  struct {
			Available        bool   `json:"available"`
			Source           string `json:"source_kind"`
			ActivationDigest string `json:"activation_digest"`
			ExpiresAt        string `json:"expires_at"`
		} `json:"control"`
	}
	c.json("GET", route, nil, 200, &status)
	if status.ReadOnly || status.Real || !status.Control.Available || status.Control.Source != "synthetic_c4_control" {
		t.Fatal(status)
	}
	post(map[string]any{"action": "prepare", "revision": m.Revision}, 409)
	post(map[string]any{"action": "prepare_readiness", "revision": m.Revision}, 200)
	control := m.C4Controls[0]
	c.json("GET", route, nil, 200, &status)
	if status.Control.ActivationDigest != control.ActivationDigest || status.Control.ExpiresAt != control.ActivationExpiresAt {
		t.Fatal("status does not expose current activation binding", status)
	}
	action := func(name string, want int) {
		t.Helper()
		post(map[string]any{"action": name, "revision": m.Revision, "control_id": control.ID, "material_digest": control.MaterialDigest}, want)
	}
	action("execute_readiness", 409)
	action("approve_control", 200)
	if r.c4Controls.readiness.Load() != 1 {
		t.Fatal("prepare or approval invoked readiness")
	}
	if m.C4Controls[0].Dispatch != nil || len(m.C4Operations) > 0 {
		t.Fatal("approval executed")
	}
	action("execute_cleanup", 409)
	action("execute_readiness", 200)
	if r.c4Controls.readiness.Load() != 2 {
		t.Fatal("readiness was not exactly once")
	}
	if m.C4Controls[0].Result.Status != "ready" || len(m.C4Operations) > 0 || !reflect.DeepEqual(m.Approvals, originalApprovals) {
		t.Fatal("readiness granted another approval")
	}
	action("execute_readiness", 409)
	if r.c4Controls.readiness.Load() != 2 {
		t.Fatal("consumed readiness replayed")
	}
	post(map[string]any{"action": "prepare", "revision": m.Revision}, 200)
	if m.C4Operations[0].Approval != nil {
		t.Fatal("baseline autoapproved")
	}
	before := m
	post(map[string]any{"action": "prepare_readiness", "revision": m.Revision}, 409)
	stored, err := r.store.GetMission(m.ID)
	if err != nil || !reflect.DeepEqual(stored, before) || r.c4Controls.readiness.Load() != 2 {
		t.Fatal("conflicting preparation changed facts or performed readiness", err)
	}
	// Recovery only accepts the recorded exact ID and consumes separate material.
	m = failed
	route = "/api/missions/" + m.ID + "/c4"
	op := m.C4Operations[0]
	if op.CleanupState() != "failed" || op.Passed() {
		t.Fatal(op)
	}
	post(map[string]any{"action": "prepare_cleanup", "revision": m.Revision, "operation_id": op.ID, "material_digest": op.MaterialDigest, "container_id": strings.Repeat("f", 64)}, 409)
	post(map[string]any{"action": "prepare_cleanup", "revision": m.Revision, "operation_id": op.ID, "material_digest": op.MaterialDigest, "container_id": op.ContainerID}, 200)
	control = m.C4Controls[len(m.C4Controls)-1]
	action("execute_cleanup", 409)
	action("approve_control", 200)
	action("execute_cleanup", 200)
	if m.C4Operations[0].CleanupState() != "confirmed" || !reflect.DeepEqual(op.Result, m.C4Operations[0].Result) || m.C4Operations[0].Passed() {
		t.Fatal("cleanup promoted failed run")
	}
	action("execute_cleanup", 409)
	var health struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	c.json("GET", "/api/health", nil, 200, &health)
	for _, k := range []string{"account_management", "credential_versions", "model_directory_probe", "magpie_discovery", "mcp_discovery", "controlled_builtin_tools", "observation_hooks", "c4_real_execution", "docker_verification"} {
		if health.Capabilities[k] {
			t.Fatal("unavailable capability advertised", k)
		}
	}
	if !health.Capabilities["c4_controlled_simulation"] {
		t.Fatal("controlled availability absent")
	}
}
