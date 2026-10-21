package engine

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/workspace"
)

type sourceRunner struct {
	*fakeRunner
	kind string
}

func (r sourceRunner) VerificationSource(string) string { return r.kind }

func TestVerificationSourceComesOnlyFromTrustedRunner(t *testing.T) {
	for _, kind := range []string{"synthetic_stub", "synthetic_content_check", "isolated_execution", "unknown", "unsupported", ""} {
		t.Run(kind, func(t *testing.T) {
			e, s, _, r, m := setup(t, research, plan, edit, review, review)
			// An image name that claims execution must never establish provenance.
			r.image = "sha256:isolated_execution"
			if kind != "" {
				e.runner = sourceRunner{r, kind}
			}
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = wait(t, s, m.ID, "awaiting_approval")
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = wait(t, s, m.ID, "awaiting_delivery")
			raw, _ := json.Marshal(m.Evidence[0])
			var record map[string]any
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			want := kind
			if kind == "" || kind == "unsupported" {
				want = "unknown"
			}
			if record["source_version"] != float64(1) || record["source_kind"] != want {
				t.Fatalf("untrusted/missing provenance: %s; want v1 %s", raw, want)
			}
		})
	}
}

func TestVerificationReportNamesSourceAndOutcomeIndependently(t *testing.T) {
	for _, tc := range []struct {
		source string
		label  string
	}{
		{`"source_version":1,"source_kind":"synthetic_stub",`, "流程模拟（未执行命令）"},
		{`"source_version":1,"source_kind":"synthetic_content_check",`, "内容检查模拟（未执行命令）"},
		{`"source_version":1,"source_kind":"isolated_execution",`, "隔离命令执行"},
		{`"source_version":1,"source_kind":"future",`, "来源未记录"},
		{`"source_version":2,"source_kind":"isolated_execution",`, "来源未记录"},
		{`"source_kind":"isolated_execution",`, "来源未记录"},
		{"", "来源未记录"},
	} {
		var v domain.Verification
		if err := json.Unmarshal([]byte(`{`+tc.source+`"id":"one","passed":true}`), &v); err != nil {
			t.Fatal(err)
		}
		report := marshalReport(domain.Mission{Evidence: []domain.Verification{v}})
		if !strings.Contains(report, tc.label) {
			t.Errorf("report missing %q: %s", tc.label, report)
		}
		if strings.Contains(tc.source, "synthetic_") && !strings.Contains(report, "模拟验证通过") {
			t.Error("simulation success claimed command execution")
		}
	}
}

func TestVerificationModelCannotDeclareSource(t *testing.T) {
	for _, text := range []string{
		`{"summary":"edit","edits":[],"source_kind":"isolated_execution"}`,
		`{"passed":true,"summary":"review","findings":[],"source_version":1,"source_kind":"isolated_execution"}`,
		`{"passed":true,"summary":"review","findings":[],"evidence_digest":"self-reported"}`,
	} {
		var out editOutput
		if strings.Contains(text, "passed") {
			var result reviewOutput
			if decodeStrict(text, &result) == nil {
				t.Fatal("model supplied provenance accepted")
			}
		} else if decodeStrict(text, &out) == nil {
			t.Fatal("model supplied provenance accepted")
		}
	}
}

func TestVerificationSourceBindsReviewsAndExportIntent(t *testing.T) {
	e, _, _, _, m := readyGeneration(t)
	before, err := e.exportIntent(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"kind", "version", "remove", "mixed"} {
		t.Run(field, func(t *testing.T) {
			changed := m
			changed.Evidence = append([]domain.Verification(nil), m.Evidence...)
			switch field {
			case "kind":
				changed.Evidence[0].SourceKind = "isolated_execution"
			case "version":
				changed.Evidence[0].SourceVersion = 99
			case "remove":
				changed.Evidence[0].SourceVersion, changed.Evidence[0].SourceKind = 0, ""
			case "mixed":
				older := changed.Evidence[0]
				older.SourceKind = "synthetic_stub"
				changed.Evidence = append([]domain.Verification{older}, changed.Evidence...)
			}
			if domain.CanDeliver(changed, changed.CandidateDigest) == nil {
				t.Error("changed provenance reused prior review")
			}
			after, err := e.exportIntent(changed)
			if err != nil || before.IntentDigest == after.IntentDigest || before.ExportReport == after.ExportReport {
				t.Error("changed provenance did not change export binding", err)
			}
		})
	}
	for _, review := range m.Reviews {
		raw, _ := json.Marshal(review)
		if !strings.Contains(string(raw), `"evidence_digest":`) {
			t.Fatal("controller review not bound to evidence", string(raw))
		}
	}
}

func TestVerificationCommittedExportRejectsChangedSource(t *testing.T) {
	e, s, _, _, m := readyGeneration(t)
	m, err := e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateMission(m.ID, m.Revision, "fixture.corruption", func(current *domain.Mission) error {
		current.Evidence[0].SourceKind = "isolated_execution"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ReadDelivery(m.ID, "report"); err == nil {
		t.Fatal("source mutation did not invalidate committed report binding")
	}
}

func TestVerificationOrdinaryExportBindsSourceAndReport(t *testing.T) {
	for _, variant := range []string{"source", "version", "remove", "report_bytes"} {
		t.Run(variant, func(t *testing.T) {
			e, s, _, _, m := setup(t, research, plan, edit, review, review)
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = wait(t, s, m.ID, "awaiting_approval")
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = wait(t, s, m.ID, "awaiting_delivery")
			m, err := e.Deliver(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.ReadDelivery(m.ID, "report"); err != nil {
				t.Fatal("new ordinary report unreadable", err)
			}
			if variant == "report_bytes" {
				if err = os.WriteFile(m.Delivery.ReportPath, []byte("tampered report"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err = s.UpdateMission(m.ID, m.Revision, "fixture.corruption", func(current *domain.Mission) error {
					switch variant {
					case "source":
						current.Evidence[0].SourceKind = "isolated_execution"
					case "version":
						current.Evidence[0].SourceVersion = 99
					case "remove":
						current.Evidence[0].SourceVersion, current.Evidence[0].SourceKind = 0, ""
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = e.ReadDelivery(m.ID, "patch"); err == nil {
					t.Error("source mutation left ordinary patch downloadable")
				}
			}
			if _, err = e.ReadDelivery(m.ID, "report"); err == nil {
				t.Fatal("ordinary report accepted changed provenance or bytes")
			}
		})
	}
}

func TestVerificationOrdinaryHistoricalDeliveryWithoutHashRemainsReadable(t *testing.T) {
	e, s, _, _, m := setup(t, research, plan, edit, review, review)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_delivery")
	m, err := e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateMission(m.ID, m.Revision, "fixture.legacy", func(current *domain.Mission) error {
		current.Delivery.ReportSHA256 = ""
		for i := range current.Evidence {
			current.Evidence[i].SourceVersion, current.Evidence[i].SourceKind = 0, ""
		}
		for i := range current.Reviews {
			current.Reviews[i].EvidenceDigest = ""
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	const historical = "historical report without source metadata or hash\n"
	if err = os.WriteFile(m.Delivery.ReportPath, []byte(historical), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := e.ReadDelivery(m.ID, "report")
	if err != nil || string(got) != historical {
		t.Fatal("legacy report no longer readable unchanged", err)
	}
}

func TestVerificationLegacyBytesAndPreparedExportCompatibility(t *testing.T) {
	const raw = `{"id":"old","candidate_digest":"c","image_id":"i","created_at":"t","acceptance_revision":1,"results":[],"passed":true}`
	var old domain.Verification
	if err := json.Unmarshal([]byte(raw), &old); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(old)
	if string(encoded) != raw || old.EffectiveSource() != "unknown" {
		t.Fatal("legacy evidence rewritten", string(encoded))
	}

	for _, variant := range []string{"legacy", "new_source", "changed_report"} {
		t.Run(variant, func(t *testing.T) {
			e, s, _, _, m := readyGeneration(t)
			// Restore the precise pre-C1 persisted shape, including old reviews.
			m, err := s.UpdateMission(m.ID, m.Revision, "fixture.legacy", func(current *domain.Mission) error {
				for i := range current.Evidence {
					current.Evidence[i].SourceVersion, current.Evidence[i].SourceKind = 0, ""
				}
				for i, r := range current.Reviews {
					current.Reviews[i] = domain.Review{Role: r.Role, Passed: r.Passed, Summary: r.Summary, Findings: r.Findings, CandidateDigest: r.CandidateDigest}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.MarshalIndent(struct {
				BaseCommit, CandidateDigest, ImageID string
				Evidence                             []domain.Verification
				Reviews                              []domain.Review
			}{m.BaseCommit, m.CandidateDigest, m.Spec.ImageID, m.Evidence, m.Reviews}, "", "  ")
			oldReport := "# FOUNDRY local delivery\n\nLocal patch only; not applied, pushed or deployed. Command success is bounded evidence, not a guarantee of correctness. No browser or web research verification was performed.\n\n```json\n" + string(b) + "\n```\n"
			if variant == "changed_report" {
				oldReport += "altered"
			}
			if variant == "new_source" {
				m.Evidence[0].SourceVersion, m.Evidence[0].SourceKind = 1, "synthetic_stub"
				for i := range m.Reviews {
					m.Reviews[i].EvidenceDigest = domain.VerificationDigest(m.Evidence)
				}
				// Same CAS revision, modeling tampered disk state rather than a user action.
				data, _ := json.Marshal(m)
				if _, err = failureDB(t, e).Exec("UPDATE missions SET data=? WHERE id=?", string(data), m.ID); err != nil {
					t.Fatal(err)
				}
			}
			op, err := e.localIntent(m, "export", struct {
				Report    string
				Evidence  []domain.Verification
				Reviews   []domain.Review
				Approvals []domain.Approval
			}{oldReport, m.Evidence, m.Reviews, m.Approvals})
			if err != nil {
				t.Fatal(err)
			}
			op.ExportReport = oldReport
			op, err = s.BeginLocalOperation(op)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := e.selectedCandidate(m)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := workspace.PrepareExport(context.Background(), e.dir(m.ID), op.ID, candidate, oldReport)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: prepared.PatchPath, ReportPath: prepared.ReportPath, PatchSHA256: prepared.PatchSHA256, ReportSHA256: prepared.ReportSHA256, ReportInput: oldReport}})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, err = e.Deliver(context.Background(), m.ID, m.Revision)
			if variant == "legacy" && err != nil {
				t.Fatal("exact legacy prepared export no longer resumable", err)
			}
			if variant == "legacy" {
				download, err := e.ReadDelivery(m.ID, "report")
				if err != nil || string(download) != oldReport {
					t.Fatal("committed legacy seal no longer readable", err)
				}
			}
			if variant != "legacy" && err == nil {
				t.Fatal("legacy compatibility accepted new/modified material")
			}
			retained, err := os.ReadFile(prepared.ReportPath)
			if err != nil || string(retained) != oldReport {
				t.Fatal("historical report rewritten", err)
			}
		})
	}
}
