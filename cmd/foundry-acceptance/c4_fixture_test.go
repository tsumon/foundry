package main

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

// The real HTTP/engine/store flow must stop at each independent decision. The
// only fake boundary is the in-process model/driver, never a passing store seed.
func TestC4FixtureFullApprovalRepairDelivery(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	id := r.scenarios["c4_baseline_pending"]
	if id == "" {
		t.Fatal("missing pending C4 baseline fixture")
	}
	c := newAcceptanceAPIClient(t, r.URL)
	base := "/api/missions/" + id
	var response struct {
		Mission domain.Mission `json:"mission"`
	}
	c.json("GET", base, nil, 200, &response)
	m := response.Mission
	frozenProfiles := m.Profiles
	if m.Spec.C4 == nil || len(m.C4Operations) != 1 || m.C4Operations[0].State != "prepared" || m.C4Operations[0].Approval != nil || m.Budget.UsedCalls != 0 {
		t.Fatal("not an unapproved baseline", m)
	}
	source := map[string][32]byte{}
	for _, name := range []string{"main.go", "main_test.go", "go.mod", "go.sum", "fixture.json"} {
		b, err := os.ReadFile(filepath.Join(m.RepoPath, name))
		if err != nil {
			t.Fatal(err)
		}
		source[name] = sha256.Sum256(b)
	}
	sourceStatus := func() string {
		cmd := exec.Command("/usr/bin/git", "status", "--porcelain=v1", "--untracked-files=all")
		cmd.Dir = m.RepoPath
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	beforeStatus := sourceStatus()
	settings, _ := r.store.GetSettings()
	if settings.DefaultProfileID != "synthetic-stub" {
		t.Fatal("C4 seed changed legacy default", settings)
	}
	// Any normal/provider routing would fail against this unreachable declaration.
	if err := r.store.PutProfile(domain.Profile{ID: "never-route-c4", Name: "Unused declaration", Kind: "openai", Protocol: "chat_completions", BaseURL: "https://never-used.invalid", Model: "never"}); err != nil {
		t.Fatal(err)
	}
	if err := r.store.PutSettings(allRoleBindings("never-route-c4", nil)); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Profiles {
		if p.Kind != "synthetic" || p.HasKey || p.AccountID != "" {
			t.Fatal("not frozen accountless synthetic", p)
		}
	}
	if len(m.Profiles) != 7 {
		t.Fatal("expected exactly seven frozen roles")
	}
	action := func(name string, want int) {
		c.json("POST", base+"/"+name, map[string]any{"revision": m.Revision}, want, &response)
	}
	c4 := func(name string, revision int64, want int) {
		o := m.C4Operations[0]
		c.json("POST", base+"/c4", map[string]any{"action": name, "revision": revision, "operation_id": o.ID, "material_digest": o.MaterialDigest}, want, &response)
	}
	action("plan", 409)
	action("deliver", 409)
	c4("execute", m.Revision, 409)
	c4("approve", m.Revision-1, 409)
	c4("approve", m.Revision, 200)
	m = response.Mission
	if m.C4Operations[0].State != "approved" || m.C4Operations[0].Dispatch != nil {
		t.Fatal("approval executed baseline")
	}
	c4("execute", m.Revision, 200)
	m = response.Mission
	if !m.C4Operations[0].Passed() || len(m.Evidence) != 0 {
		t.Fatal("baseline is not separate passing fact", m.Error)
	}
	c4("execute", m.Revision, 409)
	action("deliver", 409)
	action("plan", 200)
	var err error
	m, err = waitSyntheticMission(r.store, id, "awaiting_approval", "blocked")
	if err != nil || m.State != "awaiting_approval" {
		t.Fatal("planning", err, m.Error)
	}
	if len(m.Evidence) != 0 {
		t.Fatal("planning dispatched candidate")
	}
	c.json("POST", base+"/chat", map[string]any{"revision": m.Revision, "message": "Explain the synthetic baseline"}, 200, &response)
	m = response.Mission
	if len(m.Messages) == 0 || !strings.Contains(m.Messages[len(m.Messages)-1].Content, "SYNTHETIC C4 fixture") {
		t.Fatal("Main escaped dedicated synthetic gateway")
	}
	action("approve", 200)
	m, err = waitSyntheticMission(r.store, id, "awaiting_delivery", "blocked")
	if err != nil || m.State != "awaiting_delivery" {
		t.Fatal("execution", err, m.Error)
	}
	if len(m.C4Operations) != 3 || m.C4Operations[1].Result.Class != "test_failed" || !m.C4Operations[2].Passed() || len(m.Evidence) != 2 || len(m.Reviews) != 2 || m.Delivery != nil {
		t.Fatal("failure/repair/review flow missing", m)
	}
	for _, v := range m.Evidence {
		if v.SourceKind != "synthetic_stub" {
			t.Fatal("wrong provenance", v)
		}
	}
	for _, review := range m.Reviews {
		if review.BindingDigest == "" || len(review.Checks) == 0 {
			t.Fatal("unbound review", review)
		}
	}
	if m.C4Operations[1].CandidateDigest == m.C4Operations[2].CandidateDigest {
		t.Fatal("unchanged repair")
	}
	action("deliver", 200)
	m = response.Mission
	if m.State != "delivered" || m.Delivery == nil {
		t.Fatal("not delivered")
	}
	patch, err := os.ReadFile(m.Delivery.PatchPath)
	if err != nil || !strings.Contains(string(patch), "+func Double(n int) int { return n * 2 }") || !strings.Contains(string(patch), "-func Double(n int) int { return n + n }") {
		t.Fatal("no meaningful corrected diff", err, string(patch))
	}
	for name, sum := range source {
		b, err := os.ReadFile(filepath.Join(m.RepoPath, name))
		if err != nil || sha256.Sum256(b) != sum {
			t.Fatal("source changed", name, err)
		}
	}
	if sourceStatus() != beforeStatus {
		t.Fatal("source Git state changed")
	}
	if !reflect.DeepEqual(m.Profiles, frozenProfiles) {
		t.Fatal("settings replaced snapshot")
	}
}

func TestC4FixtureCleanupFailureRecoveryIsCleanupOnly(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	id := r.scenarios["c4_cleanup_failure"]
	if id == "" {
		t.Fatal("missing cleanup failure fixture")
	}
	m, err := r.store.GetMission(id)
	if err != nil {
		t.Fatal(err)
	}
	o := m.C4Operations[0]
	if o.CleanupState() != "failed" || o.Passed() || o.Result.Class != "infrastructure_failed" {
		t.Fatal("failure fixture not explained", o)
	}
	c := newAcceptanceAPIClient(t, r.URL)
	var response struct {
		Mission domain.Mission `json:"mission"`
	}
	c.json("POST", "/api/missions/"+id+"/c4", map[string]any{"action": "cleanup", "revision": m.Revision, "operation_id": o.ID, "material_digest": o.MaterialDigest, "container_id": o.ContainerID}, 200, &response)
	cleaned := response.Mission.C4Operations[0]
	if cleaned.CleanupState() != "confirmed" || cleaned.Passed() || !reflect.DeepEqual(o.Result, cleaned.Result) || !reflect.DeepEqual(o.Started, cleaned.Started) {
		t.Fatal("cleanup replayed or promoted failure")
	}
}
