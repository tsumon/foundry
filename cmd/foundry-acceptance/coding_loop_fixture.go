package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
)

const (
	codingLoopGoalMarker = "SYNTHETIC C1_CONTENT_REPAIR"
	codingLoopImage      = "synthetic:coding-loop-content-fixture"
	codingLoopImageID    = "sha256:synthetic-coding-loop-content-fixture"
	codingLoopCommand    = "test -f /workspace/src/main.txt"
	codingLoopOutput     = "SIMULATED content check; no command executed"
)

type codingLoopGateway struct{ fallback acceptanceGateway }

type codingLoopPrompt struct {
	Goal     string                `json:"goal"`
	Changes  []domain.Change       `json:"changes"`
	Evidence []domain.Verification `json:"evidence"`
}

func codingLoopFailedContent(input codingLoopPrompt) bool {
	if len(input.Changes) == 0 || len(input.Evidence) == 0 {
		return false
	}
	change := input.Changes[len(input.Changes)-1]
	verification := input.Evidence[len(input.Evidence)-1]
	if change.Path != "src/main.txt" || change.After != "still-wrong\n" || verification.ID == "" || verification.Passed || verification.ImageID != codingLoopImageID || verification.AcceptanceRevision < 1 || len(verification.Results) != 1 || len(verification.CandidateDigest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(verification.CandidateDigest); err != nil {
		return false
	}
	result := verification.Results[0]
	return result.Command == codingLoopCommand && result.Output == codingLoopOutput && result.ExitCode == 1 && !result.TimedOut
}

func (g codingLoopGateway) Complete(ctx context.Context, request model.Request) (model.Result, error) {
	var input codingLoopPrompt
	if json.Unmarshal([]byte(request.Prompt), &input) != nil || !strings.HasPrefix(input.Goal, codingLoopGoalMarker+" ") {
		return g.fallback.Complete(ctx, request)
	}
	mode := strings.TrimSpace(strings.TrimPrefix(input.Goal, codingLoopGoalMarker))
	if i := strings.IndexByte(mode, ':'); i >= 0 {
		mode = strings.TrimSpace(mode[:i])
	}
	var text string
	switch request.Role {
	case domain.Planner:
		text = `{"summary":"SYNTHETIC C1 repair proposal; human revision and approval required","steps":[{"id":"repair","title":"Repair the fixture","description":"Change only the disposable source file","files":["src/main.txt"],"depends_on":[]}]}`
	case domain.Worker:
		if mode == "unknown_output" {
			text = `{"unknown_result":"cannot authorize an edit"}`
		} else {
			text = `{"summary":"SYNTHETIC intentionally wrong first edit","edits":[{"path":"src/main.txt","content":"still-wrong\n"}]}`
		}
	case domain.Debugger:
		if !codingLoopFailedContent(input) {
			return model.Result{}, errors.New("synthetic C1 debugger requires the latest failed content evidence")
		}
		content := "repaired\n"
		if mode == "same_debugger" {
			content = "still-wrong\n"
		}
		raw, err := json.Marshal(map[string]any{"summary": "SYNTHETIC debugger edit", "edits": []map[string]any{{"path": "src/main.txt", "content": content}}})
		if err != nil {
			return model.Result{}, err
		}
		text = string(raw)
	case domain.QA:
		if mode == "qa_reject" {
			text = `{"passed":false,"summary":"SYNTHETIC QA rejection","findings":[]}`
		} else {
			text = `{"passed":true,"summary":"SYNTHETIC QA accepted the simulated evidence","findings":[]}`
		}
	case domain.Auditor:
		if mode == "auditor_reject" {
			text = `{"passed":false,"summary":"SYNTHETIC Auditor rejection","findings":[]}`
		} else {
			text = `{"passed":true,"summary":"SYNTHETIC Auditor accepted the simulated evidence","findings":[]}`
		}
	default:
		return g.fallback.Complete(ctx, request)
	}
	return model.Result{Text: text}, nil
}

func (g codingLoopGateway) PreflightModelSelection(ctx context.Context, schema int, selection domain.RoleModelSelection) error {
	return g.fallback.PreflightModelSelection(ctx, schema, selection)
}

func (g codingLoopGateway) PreflightAccountProfile(ctx context.Context, profile domain.Profile) error {
	return g.fallback.PreflightAccountProfile(ctx, profile)
}

type codingLoopRunner struct{ fallback syntheticRunner }

func (r codingLoopRunner) Resolve(ctx context.Context, image string) (string, error) {
	if image == codingLoopImage {
		return codingLoopImageID, nil
	}
	return r.fallback.Resolve(ctx, image)
}

func (r codingLoopRunner) Run(ctx context.Context, missionID, candidate, imageID, command string) (domain.CommandResult, error) {
	if imageID != codingLoopImageID {
		return r.fallback.Run(ctx, missionID, candidate, imageID, command)
	}
	content, err := os.ReadFile(filepath.Join(candidate, "src/main.txt"))
	result := domain.CommandResult{Command: command, ExitCode: 1, Output: codingLoopOutput}
	if err == nil && string(content) == "repaired\n" && command == codingLoopCommand {
		result.ExitCode = 0
	}
	return result, err
}

func (r codingLoopRunner) Reconcile(ctx context.Context) error {
	return r.fallback.Reconcile(ctx)
}

func codingLoopCreateRequest(repo, mode string) engine.CreateRequest {
	return engine.CreateRequest{
		Title:         "SYNTHETIC C1 content repair " + mode,
		Goal:          codingLoopGoalMarker + " " + mode + ": repair the disposable text fixture",
		RepoPath:      repo,
		WorkspaceMode: domain.WorkspaceModeGenerationsV1,
		Spec: domain.Spec{
			Scope:      []string{"src/"},
			Acceptance: []string{codingLoopCommand},
			Image:      codingLoopImage,
			ProfileID:  "synthetic-stub",
			MaxCalls:   16,
		},
	}
}

func seedCodingLoopScenario(r *acceptanceRuntime) error {
	mission, err := r.engine.Create(context.Background(), codingLoopCreateRequest(r.Repo, "default"))
	if err != nil {
		return err
	}
	r.scenarios["coding_loop_repair"] = mission.ID
	return nil
}
