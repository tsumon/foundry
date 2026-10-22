package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/workspace"
)

func readyC4ReportExport(t *testing.T) (*Engine, domain.Mission) {
	t.Helper()
	e, s, m, _, forbidden, _ := c4Fixture(t)
	m = c4Baseline(t, e, m)
	e.options.C4.models = &codingReviewGateway{script: &scripted{outputs: []string{
		`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`,
		`{"summary":"changed","edits":[{"path":"main.go","content":"package demo\nfunc Value() int { return 2 }\n"}]}`,
		codingPass, codingPass,
	}}}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error)
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_delivery" || forbidden.calls != 0 {
		t.Fatal(m.State, m.Error, forbidden.calls)
	}
	return e, m
}

// Catches a renderer upgrade rewriting sealed bytes or rejecting their original
// binding, and a compatibility fallback accepting altered report/material.
func TestC4ReportPreparedAndCommittedHistoryPreserved(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "altered report"}[tamper], func(t *testing.T) {
			e, m := readyC4ReportExport(t)
			old := reportWithSources(m, true)
			if !strings.Contains(old, "Synthetic models and command outcomes only.") {
				t.Fatal("historical renderer changed")
			}
			if tamper {
				old += "altered"
			}
			op, err := e.exportIntentForReport(m, old)
			if err != nil {
				t.Fatal(err)
			}
			op, err = e.s.BeginLocalOperation(op)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := e.selectedCandidate(m)
			if err != nil {
				t.Fatal(err)
			}
			p, err := workspace.PrepareExport(context.Background(), e.dir(m.ID), op.ID, candidate, old)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: p.PatchPath, ReportPath: p.ReportPath, PatchSHA256: p.PatchSHA256, ReportSHA256: p.ReportSHA256, ReportInput: old}})
			if err != nil {
				t.Fatal(err)
			}
			delivered, err := e.Deliver(context.Background(), m.ID, m.Revision)
			if tamper {
				if err == nil {
					t.Fatal("altered historical report accepted")
				}
			} else {
				if err != nil {
					t.Fatal("exact historical prepared report rejected", err)
				}
				got, err := e.ReadDelivery(m.ID, "report")
				if err != nil || string(got) != old {
					t.Fatal("original sealed bytes no longer readable", err)
				}
				if delivered.Delivery.ReportSHA256 != p.ReportSHA256 {
					t.Fatal("historical report resealed")
				}
				changed := delivered
				changed.C4Operations = append([]domain.C4Operation(nil), delivered.C4Operations...)
				r := *changed.C4Operations[0].Result
				r.Output += "changed"
				changed.C4Operations[0].Result = &r
				if e.verifyDelivery(changed) == nil {
					t.Fatal("changed operation accepted under legacy format")
				}
				if err := os.WriteFile(p.ReportPath, []byte(old+"tampered"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := e.ReadDelivery(m.ID, "report"); err == nil {
					t.Fatal("changed artifact hash accepted")
				}
				return
			}
			got, err := os.ReadFile(p.ReportPath)
			if err != nil || string(got) != old {
				t.Fatal("rejected historical bytes rewritten", err)
			}
		})
	}
}
