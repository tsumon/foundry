package acceptance

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDesktopInferencePreparationKeepsIndependentBudgetGate(t *testing.T) {
	for _, missingMedia := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "missing-media"}[missingMedia], func(t *testing.T) {
			prior, db, a, r, catalog, resolver, calls, _ := inferenceController(t)
			c := NewDesktopCoordinator(db, DesktopOptions{Resolver: resolver, Probe: func() error { t.Fatal("inference preparation probed native credentials"); return nil }})
			defer c.Close()
			if err := c.Prepare(a.ID, "inference", r.ID, a.Revision); err != nil {
				t.Fatalf("desktop inference cannot prepare: %v", err)
			}
			var state struct {
				InferenceReady bool `json:"inference_ready"`
			}
			raw, _ := json.Marshal(c.Status(a.ID))
			if err := json.Unmarshal(raw, &state); err != nil || !state.InferenceReady || c.Status(a.ID).CatalogReady || c.Status(a.ID).ConnectionReady {
				t.Fatal("wrong desktop authority", string(raw))
			}
			var approved domain.ModelAcceptanceOperation
			c.WithService(func(s *Service) {
				s.inference.http.Transport = prior.inference.http.Transport
				if missingMedia {
					s.inference.http.Transport = catalogTransport(func(req *http.Request) (*http.Response, error) {
						calls.Add(1)
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(inferenceCompletion)), Request: req}, nil
					})
				}
				pending := proposeInference(t, s, a, r, catalog)
				if _, err := s.Execute(context.Background(), a.ID, pending.ID, pending.Revision, pending.MaterialDigest); err == nil {
					t.Fatal("pending inference dispatched")
				}
				approved = approveCatalog(t, s, pending)
			})
			m, err := db.GetMission("inference-budget-task")
			if err != nil || m.Budget.UsedCalls != 3 || calls.Load() != 0 || resolver.reads.Load() != 0 {
				t.Fatal("prepare/approve caused effects")
			}
			c.WithService(func(s *Service) {
				got, err := s.Execute(context.Background(), a.ID, approved.ID, approved.Revision, approved.MaterialDigest)
				if err != nil {
					t.Fatal(err)
				}
				if missingMedia {
					if got.State != "unknown" || got.InferenceDiagnostic == nil || got.InferenceDiagnostic.Response.Reason != "missing_media_type" {
						t.Fatal("strict media gate lost")
					}
				} else if got.State != "succeeded" || got.OutputMatched == nil || !*got.OutputMatched {
					t.Fatal("completion missing")
				}
				if _, err = s.Execute(context.Background(), a.ID, approved.ID, approved.Revision, approved.MaterialDigest); err == nil {
					t.Fatal("duplicate dispatch")
				}
			})
			m, err = db.GetMission(m.ID)
			if err != nil || m.Budget.UsedCalls != 4 || m.Budget.MaxCalls != 7 || m.State != "blocked" || calls.Load() != 1 || resolver.reads.Load() != 1 {
				t.Fatal("wrong single-call effects")
			}
		})
	}
}

func TestDesktopInferenceAuthorityReplacementAndRestart(t *testing.T) {
	_, db, a, r, catalog, resolver, calls, _ := inferenceController(t)
	c := NewDesktopCoordinator(db, DesktopOptions{Resolver: resolver})
	defer c.Close()
	if err := c.Prepare(a.ID, "inference", r.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	var pending domain.ModelAcceptanceOperation
	c.WithService(func(s *Service) { pending = proposeInference(t, s, a, r, catalog) })
	if err := c.Prepare(a.ID, "catalog", r.ID, a.Revision); err != nil {
		t.Fatal(err)
	}
	c.WithService(func(s *Service) {
		if _, err := s.Decide(a.ID, pending.ID, pending.Revision, pending.MaterialDigest, "approve"); err == nil {
			t.Fatal("replaced inference authority used")
		}
	})
	c.Close()
	next := NewDesktopCoordinator(db, DesktopOptions{Resolver: resolver})
	defer next.Close()
	if next.Status(a.ID).Phase != "unprepared" {
		t.Fatal("restart restored authority")
	}
	next.WithService(func(s *Service) {
		if _, err := s.Decide(a.ID, pending.ID, pending.Revision, pending.MaterialDigest, "approve"); err == nil {
			t.Fatal("restart accepted old inference")
		}
	})
	if calls.Load() != 0 || resolver.reads.Load() != 0 {
		t.Fatal("metadata phase change dispatched")
	}
}
