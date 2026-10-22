package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/sandbox"
	"strings"
	"testing"
)

// Catches dropping/forging safe diagnostics at persistence, or using a failed
// diagnostic as fresh readiness authority. No actual Docker capability exists.
func TestC4ReadinessDiagnosticPersistsWithoutAuthority(t *testing.T) {
	for _, code := range []string{"image_command_failed", "version_trust_failed", "info_protocol_failed", "readiness_gate_failed", "image_timeout", "info_canceled", "PRIVATE /secret/path", "image_arbitrary", ""} {
		t.Run(code, func(t *testing.T) {
			e, s, m, d, models, _ := c4FixtureMode(t, true)
			calls := 0
			e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
				calls++
				return sandbox.C4ReadinessResult{FailureCode: code}, errors.New("PRIVATE transport text")
			}
			m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			c := m.C4Controls[0]
			if _, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || calls != 0 {
				t.Fatal("unapproved dispatch")
			}
			m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
			if err != nil {
				t.Fatal(err)
			}
			m, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
			if !errors.Is(err, ErrC4Unavailable) || calls != 1 {
				t.Fatal("failure not stopped", err)
			}
			m, err = s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := code
			if code == "" || strings.HasPrefix(code, "PRIVATE") || code == "image_arbitrary" {
				want = "failed"
			}
			r := m.C4Controls[0].Result
			if r == nil || r.FailureCode != want || r.Status != "failed" || r.Fingerprint != "" || r.ValidUntil != "" {
				t.Fatalf("lost/redaction-bypassed receipt: %+v", r)
			}
			b, _ := json.Marshal(m.C4Controls)
			if strings.Contains(string(b), "PRIVATE") {
				t.Fatal("raw diagnostic persisted")
			}
			if _, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || calls != 1 {
				t.Fatal("replayed readiness")
			}
			if _, err = e.PrepareC4Baseline(context.Background(), m.ID, m.Revision); err == nil || d.creates != 0 || models.calls != 0 {
				t.Fatal("failure promoted to execution")
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			after, err := s.GetMission(m.ID)
			if err != nil || after.C4Controls[0].Result.FailureCode != want || calls != 1 {
				t.Fatal("recovery changed or replayed diagnostic", err)
			}
		})
	}
}

func TestC4ReadinessDiagnosticCannotCoexistWithSuccess(t *testing.T) {
	e, _, m, d, _, _ := c4FixtureMode(t, true)
	e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
		return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("f", 64), FailureCode: "image_protocol_failed"}, nil
	}
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[0]
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if !errors.Is(err, ErrC4Unavailable) || m.C4Controls[0].Result.Status != "failed" || m.C4Controls[0].Result.Fingerprint != "" {
		t.Fatal("contradictory result promoted", err)
	}
	if _, err = e.PrepareC4Baseline(context.Background(), m.ID, m.Revision); err == nil || d.creates != 0 {
		t.Fatal("diagnostic granted execution")
	}
}
