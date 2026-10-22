package sandbox

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"io"
	"strings"
	"testing"
	"time"
)

func c4ReadinessFixture() (domain.C4Profile, C4DockerConfig, []c4ProcessStep) {
	p := c4DriverPlan().profile
	p.Mode = "docker"
	p.ControlVersion = 1
	c := C4DockerConfig{Enabled: true, Trust: C4Trust{DockerPath: p.DockerPath, Endpoint: p.Endpoint}}
	return p, c, []c4ProcessStep{
		{args: c4Args("version", "--format", "{{json .}}"), out: `{"Server":{"Version":"27.0.1","Os":"linux","Arch":"arm64"}}`},
		{args: c4Args("info", "--format", "{{json .}}"), out: `{"ID":"fixture-daemon-id","OSType":"linux","Architecture":"aarch64"}`},
		{args: (C4Plan{profile: p}).imageInspection().Args, out: string(c4DockerImageFixture())},
	}
}
func TestC4ReadinessDisabledMalformedNoEffects(t *testing.T) {
	for _, kind := range []string{"disabled", "mode", "version", "profile", "path", "endpoint", "nil process", "nil trust"} {
		t.Run(kind, func(t *testing.T) {
			p, c, _ := c4ReadinessFixture()
			f := &c4ScriptProcess{t: t}
			tr := &c4CountingTrust{}
			var process c4DockerProcess = f
			var trust c4DockerTrustCheck = tr
			switch kind {
			case "disabled":
				c.Enabled = false
			case "mode":
				p.Mode = "synthetic"
			case "version":
				p.ControlVersion = 0
			case "profile":
				p.Platform = "linux/amd64"
			case "path":
				c.Trust.DockerPath = "/different"
			case "endpoint":
				c.Trust.Endpoint = "unix:///different"
			case "nil process":
				process = nil
			case "nil trust":
				trust = nil
			}
			if _, err := checkC4DockerReadiness(context.Background(), p, c, process, trust); err == nil || f.calls != 0 || tr.calls != 0 {
				t.Fatal("invalid readiness had effects", err, f.calls, tr.calls)
			}
		})
	}
	if _, err := CheckC4DockerReadiness(context.Background(), domain.C4Profile{}, C4DockerConfig{}); err == nil {
		t.Fatal("disabled real wrapper accepted")
	}
}
func TestC4ReadinessFixedSequenceAndFingerprint(t *testing.T) {
	p, c, steps := c4ReadinessFixture()
	f := &c4ScriptProcess{t: t, steps: steps}
	trust := &c4CountingTrust{}
	result, err := checkC4DockerReadiness(context.Background(), p, c, f, trust)
	if err != nil || len(result.Fingerprint) != 64 || strings.Contains(result.Fingerprint, "fixture") || f.calls != 3 || trust.calls != 3 {
		t.Fatal(result, err, f.calls, trust.calls)
	}
	steps[1].out = strings.Replace(steps[1].out, "aarch64", "arm64", 1)
	f = &c4ScriptProcess{t: t, steps: steps}
	normalized, err := checkC4DockerReadiness(context.Background(), p, c, f, &c4CountingTrust{})
	if err != nil || normalized != result {
		t.Fatal("explicit arm64 normalization", err)
	}
	steps[1].out = strings.Replace(steps[1].out, "fixture-daemon-id", "different-daemon-id", 1)
	f = &c4ScriptProcess{t: t, steps: steps}
	changed, err := checkC4DockerReadiness(context.Background(), p, c, f, &c4CountingTrust{})
	if err != nil || changed == result {
		t.Fatal("fingerprint did not bind identity", err)
	}
}
func TestC4ReadinessRejectsAndStops(t *testing.T) {
	for stage := 0; stage < 3; stage++ {
		for _, kind := range []string{"exit", "transport", "truncated", "duplicate", "wrong type", "trailing", "missing", "trust"} {
			t.Run(string(rune('0'+stage))+kind, func(t *testing.T) {
				p, c, steps := c4ReadinessFixture()
				tr := &c4CountingTrust{}
				switch kind {
				case "exit":
					steps[stage].exit = 1
				case "transport":
					steps[stage].err = errors.New("PRIVATE detail")
				case "truncated":
					steps[stage].out = strings.Repeat("x", c4DockerInspectLimit+1)
				case "duplicate":
					steps[stage].out = `{"ID":"one","ID":"two"}`
				case "wrong type":
					steps[stage].out = `{"Server":[],"ID":10}`
				case "trailing":
					steps[stage].out += "{}"
				case "missing":
					steps[stage].out = "{}"
				case "trust":
					tr.failAt = stage + 1
				}
				f := &c4ScriptProcess{t: t, steps: steps}
				r, err := checkC4DockerReadiness(context.Background(), p, c, f, tr)
				want := stage + 1
				if kind == "trust" {
					want = stage
				}
				if err == nil || r.Fingerprint != "" || f.calls != want || strings.Contains(err.Error(), "PRIVATE") {
					t.Fatal("unsafe failure", r, err, f.calls, want)
				}
			})
		}
	}
}

type c4ReadinessProcessFunc func(context.Context, C4Command, io.Writer, io.Writer) (int, error)

func (f c4ReadinessProcessFunc) Run(ctx context.Context, c C4Command, o, e io.Writer) (int, error) {
	return f(ctx, c, o, e)
}
func TestC4ReadinessCancellationAndTotalDeadline(t *testing.T) {
	p, c, _ := c4ReadinessFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &c4ScriptProcess{t: t}
	tr := &c4CountingTrust{}
	if _, err := checkC4DockerReadiness(ctx, p, c, f, tr); err == nil || f.calls != 0 || tr.calls != 0 {
		t.Fatal("canceled preflight effects")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	calls := 0
	process := c4ReadinessProcessFunc(func(ctx context.Context, _ C4Command, _, _ io.Writer) (int, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("missing total deadline")
		}
		<-ctx.Done()
		return -1, ctx.Err()
	})
	if _, err := checkC4DockerReadiness(ctx, p, c, process, &c4CountingTrust{}); !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatal("deadline did not stop sequence", err, calls)
	}
}

func TestC4ReadinessRequiresBoundedExactMetadata(t *testing.T) {
	for _, tc := range []struct {
		stage int
		raw   string
	}{
		{0, `{"Server":{"Version":"","Os":"linux","Arch":"arm64"}}`},
		{0, `{"Server":{"Version":null,"Os":"linux","Arch":"arm64"}}`},
		{0, `{"Server":{"Version":"27","Os":"darwin","Arch":"arm64"}}`},
		{0, `{"Server":{"Version":"27","Os":"linux","Arch":"amd64"}}`},
		{0, `{"server":{"Version":"27","Os":"linux","Arch":"arm64"}}`},
		{0, `{"Server":{"Version":"27","version":"27","Os":"linux","Arch":"arm64"}}`},
		{0, `{"Server":{"Version":"` + strings.Repeat("v", 129) + `","Os":"linux","Arch":"arm64"}}`},
		{1, `{"ID":"","OSType":"linux","Architecture":"arm64"}`},
		{1, `{"ID":false,"OSType":"linux","Architecture":"arm64"}`},
		{1, `{"ID":"id","OSType":"windows","Architecture":"arm64"}`},
		{1, `{"ID":"id","OSType":"linux","Architecture":"ARM64"}`},
		{1, `{"ID":"id\nprivate","OSType":"linux","Architecture":"arm64"}`},
		{1, `{"ID":"id","OSType":"linux","Architecture":"arm64"`},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			p, c, steps := c4ReadinessFixture()
			steps[tc.stage].out = tc.raw
			f := &c4ScriptProcess{t: t, steps: steps}
			if r, err := checkC4DockerReadiness(context.Background(), p, c, f, &c4CountingTrust{}); err == nil || r.Fingerprint != "" || f.calls != tc.stage+1 {
				t.Fatal("invalid metadata continued", r, err, f.calls)
			}
		})
	}
}
func TestC4ReadinessAddsThirtySecondDeadlineWithoutCallerDeadline(t *testing.T) {
	p, c, _ := c4ReadinessFixture()
	calls := 0
	f := c4ReadinessProcessFunc(func(ctx context.Context, _ C4Command, _, _ io.Writer) (int, error) {
		calls++
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > 30*time.Second {
			t.Fatal("missing bounded total deadline", remaining)
		}
		return -1, context.Canceled
	})
	if _, err := checkC4DockerReadiness(context.Background(), p, c, f, &c4CountingTrust{}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("cancel did not stop", err, calls)
	}
}
