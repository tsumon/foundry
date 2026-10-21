package main

import (
	"context"
	"encoding/json"
	"errors"

	"foundry/internal/domain"
	"foundry/internal/model"
)

// These doubles belong only to the explicit acceptance binary. They neither
// contact a model provider nor run a shell, container or acceptance command.
type syntheticGateway struct{}

func (syntheticGateway) Complete(_ context.Context, r model.Request) (model.Result, error) {
	var text string
	switch r.Role {
	case domain.Researcher:
		var input struct {
			Extra struct {
				Observations []struct {
					Kind         string `json:"kind"`
					InvocationID string `json:"invocation_id"`
					Digest       string `json:"digest"`
				} `json:"observations"`
			} `json:"extra"`
		}
		if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
			return model.Result{}, err
		}
		text = `{"kind":"mcp","tool_id":"reference.lookup","arguments":{"topic":"mcp-workflow"},"reason":"SYNTHETIC acceptance: inspect the fixed local reference, not user repository facts"}`
		if len(input.Extra.Observations) > 0 {
			o := input.Extra.Observations[0]
			if o.Kind != "mcp" || o.InvocationID == "" || o.Digest == "" {
				return model.Result{}, errors.New("synthetic model requires the actual sealed MCP observation")
			}
			raw, err := json.Marshal(domain.Research{Summary: "SYNTHETIC local reference inspected; fake-model acceptance only", Findings: []domain.Finding{{Severity: "info", Summary: "Synthetic fixture reference; not a fact about the repository", Paths: []string{}, Sources: []domain.ResearchSourceRef{{Kind: "mcp", InvocationID: o.InvocationID, Digest: o.Digest}}}}, Unknowns: []string{"Cloud-model quality and actual Docker execution are not tested by this harness"}})
			if err != nil {
				return model.Result{}, err
			}
			text = `{"kind":"final","research":` + string(raw) + `}`
		}
	case domain.Planner:
		text = `{"summary":"SYNTHETIC stub plan; execution still needs separate approval","steps":[{"id":"one","title":"Update synthetic text","description":"Change only the disposable fixture","files":["src/main.txt"],"depends_on":[]}]}`
	case domain.Worker, domain.Debugger:
		text = `{"summary":"SYNTHETIC stub edit","edits":[{"path":"src/main.txt","content":"SYNTHETIC source after\n"}]}`
	case domain.QA, domain.Auditor:
		text = `{"passed":true,"summary":"SYNTHETIC stub review only; no Docker or cloud-model verification","findings":[]}`
	case domain.Main:
		text = `{"answer":"SYNTHETIC acceptance only: approvals remain separate; model and runner are stubs."}`
	default:
		return model.Result{}, errors.New("unsupported synthetic model role")
	}
	return model.Result{Text: text}, nil
}

// acceptanceGateway keeps the pre-existing, explicitly labeled schema1
// compatibility scenario on the synthetic model while forcing every schema2
// selection through the actual reviewed Router and owned HTTP fixture.
type acceptanceGateway struct{ router *model.Router }

func (g acceptanceGateway) Complete(ctx context.Context, request model.Request) (model.Result, error) {
	if request.SnapshotSchema == 2 || request.Selection != nil || request.Profile.RouteID != "" {
		return g.router.Complete(ctx, request)
	}
	return (syntheticGateway{}).Complete(ctx, request)
}

func (g acceptanceGateway) PreflightModelSelection(ctx context.Context, schema int, selection domain.RoleModelSelection) error {
	return g.router.PreflightModelSelection(ctx, schema, selection)
}

func (acceptanceGateway) PreflightAccountProfile(context.Context, domain.Profile) error {
	// Compatibility only: schema1 remains a visibly synthetic fake-model task.
	return nil
}

type syntheticRunner struct{}

func (syntheticRunner) VerificationSource(string) string { return "synthetic_stub" }

func (syntheticRunner) Resolve(_ context.Context, image string) (string, error) {
	if image != "synthetic:stub" {
		return "", errors.New("synthetic runner accepts only synthetic:stub; no Docker image is resolved")
	}
	return "sha256:synthetic-stub-not-a-real-image", nil
}
func (syntheticRunner) Run(_ context.Context, _, _, _, command string) (domain.CommandResult, error) {
	return domain.CommandResult{Command: command, ExitCode: 0, Output: "SYNTHETIC STUB: command was NOT executed; no Docker/container verification"}, nil
}
func (syntheticRunner) Reconcile(context.Context) error { return nil }
