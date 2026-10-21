package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
)

func TestCodingReplanScenarioFullHTTPWorkflow(t *testing.T) {
	r := startProtocolAcceptanceForTest(t)
	id := r.scenarios["coding_loop_replan"]
	if id == "" {
		t.Fatal("missing draft coding_loop_replan scenario")
	}
	c := newAcceptanceAPIClient(t, r.URL)
	base := "/api/missions/" + id
	var response struct {
		Mission domain.Mission `json:"mission"`
	}
	c.json("GET", base, nil, 200, &response)
	m := response.Mission
	if m.State != "draft" || m.CodingVersion != 1 || m.Plan != nil || m.Budget.UsedCalls != 0 {
		t.Fatal("scenario was preapproved", m.State)
	}
	source, err := os.ReadFile(r.Repo + "/src/main.txt")
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(source)
	status := func() string {
		cmd := exec.Command("/usr/bin/git", "status", "--porcelain=v1", "--untracked-files=all")
		cmd.Dir = r.Repo
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	beforeStatus := status()
	action := func(name string, revision int64, status int) {
		c.json("POST", base+"/"+name, map[string]any{"revision": revision}, status, &response)
	}
	action("approve", m.Revision, 409)
	action("deliver", m.Revision, 409)
	action("plan", m.Revision, 200)
	m, err = waitSyntheticMission(r.store, id, "awaiting_approval", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_approval" || len(m.Evidence) != 0 {
		t.Fatal("initial approval stop missing", m.Error)
	}
	action("approve", m.Revision, 200)
	m, err = waitSyntheticMission(r.store, id, "blocked", "awaiting_delivery")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "blocked" || len(m.Evidence) != 3 || len(m.CodingAttempts) != 6 || len(m.Reviews) != 0 {
		t.Fatal("did not exhaust unchanged repairs", m.Error)
	}
	for _, v := range m.Evidence {
		if v.Passed || v.SourceKind != "synthetic_content_check" {
			t.Fatal("bad content passed or source lost")
		}
	}
	oldCandidate, oldCalls := *m.CandidateGeneration, m.Budget.UsedCalls
	action("deliver", m.Revision, 409)
	action("plan", m.Revision, 200)
	m, err = waitSyntheticMission(r.store, id, "awaiting_approval", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_approval" || m.ReplanContext == nil || len(m.Plan.Adjustments) != 3 || len(m.Evidence) != 0 || len(m.RunArchives) != 1 || *m.CandidateGeneration == oldCandidate || m.Budget.UsedCalls != oldCalls+1 {
		t.Fatalf("failure replan did not stop at fresh approval: %s %s", m.State, m.Error)
	}
	assertCodingReplanFixtureReferences(t, r, m)
	var draft engine.EditablePlan
	raw, _ := json.Marshal(m.Plan)
	if err = json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	draft.Summary = "User reviewed targeted failure correction"
	c.json("POST", base+"/plan-revisions", engine.PlanRevisionRequest{ExpectedMissionRevision: m.Revision, ExpectedPlanRevision: m.Plan.Revision, Reason: "Review frozen failure and target src/main.txt only", Plan: draft}, 200, &response)
	revised := response.Mission
	if revised.State != "awaiting_approval" || revised.Budget.UsedCalls != m.Budget.UsedCalls {
		t.Fatal("revision dispatched execution")
	}
	action("approve", m.Revision, 409)
	action("approve", revised.Revision, 200)
	m, err = waitSyntheticMission(r.store, id, "awaiting_delivery", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "awaiting_delivery" || len(m.Reviews) != 2 || len(m.Reviews[0].Checks) != 1 || len(m.Reviews[1].Checks) != 4 || len(m.Evidence) != 1 || !m.Evidence[0].Passed || m.Delivery != nil {
		t.Fatalf("corrected path failed: %s %s", m.State, m.Error)
	}
	for _, a := range m.Approvals {
		if a.Kind == "deliver" {
			t.Fatal("delivery approved by execution")
		}
	}
	action("deliver", m.Revision, 200)
	m = response.Mission
	for _, kind := range []string{"patch", "report"} {
		req, _ := http.NewRequest("GET", r.URL+base+"/"+kind, nil)
		req.AddCookie(c.cookie)
		res, err := c.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("download %s: %d %v %s", kind, res.StatusCode, err, b)
		}
		if kind == "patch" && !strings.Contains(string(b), "+repaired") {
			t.Fatal("patch missing correction")
		}
		if kind == "report" {
			for _, marker := range []string{`"coding_attempts"`, `"replan_context"`, `"adjustments"`, `"checks"`, "内容检查模拟（未执行命令）"} {
				if !strings.Contains(string(b), marker) {
					t.Fatal("report lost C2 material", marker)
				}
			}
		}
	}
	check := exec.Command("/usr/bin/git", "apply", "--check", m.Delivery.PatchPath)
	check.Dir = r.Repo
	if b, err := check.CombinedOutput(); err != nil {
		t.Fatalf("patch check-only: %v %s", err, b)
	}
	after, err := os.ReadFile(r.Repo + "/src/main.txt")
	if err != nil || sha256.Sum256(after) != checksum || status() != beforeStatus {
		t.Fatal("source changed")
	}
}

func assertCodingReplanFixtureReferences(t *testing.T, r *acceptanceRuntime, m domain.Mission) {
	t.Helper()
	gateway := codingReplanGateway{store: r.store}
	for _, name := range []string{"valid", "missing", "foreign", "mismatch", "wrong_candidate", "wrong_epoch", "malformed"} {
		t.Run("fixture_"+name, func(t *testing.T) {
			raw, _ := json.Marshal(m.ReplanContext)
			var failure domain.FailureBundle
			if err := json.Unmarshal(raw, &failure); err != nil {
				t.Fatal(err)
			}
			identity := map[string]any{"mission_id": m.ID, "epoch": m.Epoch, "candidate_generation": m.CandidateGeneration}
			input := map[string]any{"goal": m.Goal, "coding_identity": identity, "replan_context": &failure}
			switch name {
			case "malformed":
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				fields["unknown_field"] = "do not fall back"
				input["replan_context"] = fields
			case "missing":
				delete(input, "replan_context")
			case "foreign":
				failure.MissionID = "other"
			case "mismatch":
				failure.Failures[0].SourceDigest = "other"
			case "wrong_candidate":
				identity["candidate_generation"] = domain.GenerationRef{ID: "foreign", Digest: m.CandidateDigest}
			case "wrong_epoch":
				identity["epoch"] = m.Epoch - 1
			}
			prompt, _ := json.Marshal(input)
			result, err := gateway.Complete(context.Background(), model.Request{Role: domain.Planner, Prompt: string(prompt)})
			if name == "valid" {
				if err != nil || !strings.Contains(result.Text, "adjustments") {
					t.Fatal("matching failure fixture refused", err)
				}
			} else if err == nil {
				t.Fatal("fixture accepted absent/foreign failure references", name)
			}
		})
	}
}
