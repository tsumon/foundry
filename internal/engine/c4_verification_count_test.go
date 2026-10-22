package engine

import (
	"context"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"io"
	"reflect"
	"testing"
)

type c4FailedTestProcess struct {
	t     *testing.T
	calls int
}

func (p *c4FailedTestProcess) Run(_ context.Context, r c4verify.Request, out, _ io.Writer) (int, error) {
	p.t.Helper()
	p.calls++
	if r.Path != "/usr/local/go/bin/go" || r.Dir != "/workspace" {
		p.t.Fatal("unexpected command path or workspace", r)
	}
	if p.calls == 1 && reflect.DeepEqual(r.Args, []string{"version"}) {
		_, err := io.WriteString(out, "go version go1.27.3 linux/arm64\n")
		return 0, err
	}
	if p.calls != 2 || !reflect.DeepEqual(r.Args, []string{"test", "-json", "-count=1", "-timeout=90s", "./..."}) {
		p.t.Fatal("unexpected test command or retry", r.Args, p.calls)
	}
	_, err := io.WriteString(out, "{\"Action\":\"run\",\"Package\":\"synthetic-c4\",\"Test\":\"TestDouble\"}\n{\"Action\":\"fail\",\"Package\":\"synthetic-c4\",\"Test\":\"TestDouble\"}\n{\"Action\":\"fail\",\"Package\":\"synthetic-c4\"}\n")
	return 1, err
}

// Exercise the actual event parser and controller projection together. All
// process/lifecycle records below are local fixtures, NOT real Docker evidence.
func TestC4CompletedFailedTestPreservesProvenanceGates(t *testing.T) {
	e, _, m, _, forbidden, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	op := m.C4Operations[0]
	p := &c4FailedTestProcess{t: t}
	r := c4verify.Run(context.Background(), p)
	op.Result = &domain.C4Result{Class: r.Class, ExitCode: &r.ExitCode, Tests: r.Tests, Output: r.Output, At: op.Result.At}
	// Model the Docker-path projection only, without creating a real capability.
	m.Spec.C4.Mode = "docker"
	m.C4Controls[0].SourceKind = "docker_c4_control"
	for _, tc := range []struct {
		name   string
		change func(*domain.Mission, *domain.C4Operation)
		want   string
	}{
		{"complete failed test", func(*domain.Mission, *domain.C4Operation) {}, "isolated_execution"},
		{"no dispatch", func(_ *domain.Mission, o *domain.C4Operation) { o.Dispatch = nil }, "unknown"},
		{"no start", func(_ *domain.Mission, o *domain.C4Operation) { o.Started = nil }, "unknown"},
		{"no cleanup", func(_ *domain.Mission, o *domain.C4Operation) { o.Cleanup = nil }, "unknown"},
		{"wrong receipt id", func(_ *domain.Mission, o *domain.C4Operation) { o.ReadinessID = "other" }, "unknown"},
		{"wrong receipt digest", func(_ *domain.Mission, o *domain.C4Operation) { o.ReadinessDigest = "other" }, "unknown"},
		{"synthetic receipt", func(m *domain.Mission, _ *domain.C4Operation) { m.C4Controls[0].SourceKind = "synthetic_c4_control" }, "unknown"},
		{"missing readiness result", func(m *domain.Mission, _ *domain.C4Operation) { m.C4Controls[0].Result = nil }, "unknown"},
		{"failed readiness", func(m *domain.Mission, _ *domain.C4Operation) {
			m.C4Controls[0].Result = &domain.C4ControlResult{Status: "failed"}
		}, "unknown"},
		{"historical zero tests", func(_ *domain.Mission, o *domain.C4Operation) { o.Result.Tests = 0 }, "unknown"},
		{"no exit", func(_ *domain.Mission, o *domain.C4Operation) { o.Result.ExitCode = nil }, "unknown"},
		{"infrastructure failure", func(_ *domain.Mission, o *domain.C4Operation) { o.Result.Class = "infrastructure_failed" }, "unknown"},
		{"synthetic mode", func(m *domain.Mission, _ *domain.C4Operation) { m.Spec.C4.Mode = "synthetic" }, "synthetic_stub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, operation := m, op
			profile, result := *m.Spec.C4, *op.Result
			current.Spec.C4, operation.Result = &profile, &result
			current.C4Controls = append([]domain.C4Control(nil), m.C4Controls...)
			tc.change(&current, &operation)
			v := c4Verification(current, operation)
			if v.EffectiveSource() != tc.want || v.Passed {
				t.Fatalf("source=%s passed=%v; want failed evidence source=%s (parsed tests=%d)", v.EffectiveSource(), v.Passed, tc.want, r.Tests)
			}
		})
	}
	if p.calls != 2 || forbidden.calls != 0 {
		t.Fatal("unexpected retry, provider or legacy runner", p.calls, forbidden.calls)
	}
}
