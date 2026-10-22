package sandbox

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"reflect"
	"strings"
	"testing"
)

// Uses the real durable-change validator/core/driver/parser. Only the lowest
// process and executable/socket trust effects are fake; all evidence synthetic.
func newC4DockerHarness(t *testing.T) (*c4Harness, *c4ScriptProcess, *c4CountingTrust) {
	h := newC4Harness(t)
	h.p.Mode = "docker"
	h.op.ProfileDigest = h.p.ProfileDigest()
	h.op.MaterialDigest = domain.C4MaterialDigest(h.op)
	h.op.Approval.Digest = h.op.MaterialDigest
	h.mission.Spec.C4 = &h.p
	h.mission.C4Operations = []domain.C4Operation{cloneC4Operation(h.op)}
	plan, err := BuildC4Plan(h.p, h.op, h.trust, h.candidate)
	if err != nil {
		t.Fatal(err)
	}
	steps := c4DriverSteps(plan)
	// Lifecycle adds its own exact-ID inspections after create and before remove.
	steps = append(steps[:2], append([]c4ProcessStep{steps[2]}, steps[2:]...)...)
	steps = append(steps[:8], append([]c4ProcessStep{steps[8]}, steps[8:]...)...)
	for i := range steps {
		steps[i].out = strings.ReplaceAll(steps[i].out, "/synthetic/candidate", h.candidate)
	}
	return h, &c4ScriptProcess{t: t, steps: steps, trace: &h.trace}, &c4CountingTrust{trace: &h.trace}
}
func runC4DockerHarness(h *c4Harness, f *c4ScriptProcess, trust *c4CountingTrust) (domain.C4Operation, error) {
	return executeC4Docker(context.Background(), h.p, h.op, C4DockerConfig{Enabled: true, Trust: h.trust}, h.candidate, h.persist, f, trust)
}

// Valid terminal recovery derived from a successful lower-seam lifecycle. Its
// pending cleanup and unknown result match the existing explicit recovery case.
func c4DockerDisabledRecoveryFixture(t *testing.T) (*c4Harness, *c4ScriptProcess, *c4CountingTrust) {
	t.Helper()
	h, f, trust := newC4DockerHarness(t)
	o, err := runC4DockerHarness(h, f, trust)
	if err != nil || !o.Passed() {
		t.Fatal("recovery fixture execution failed", err)
	}
	o.Result.Class = "outcome_unknown"
	o.Cleanup = nil
	h.op = o
	h.mission.C4Operations = []domain.C4Operation{cloneC4Operation(o)}
	h.trace = nil
	plan, err := BuildC4Plan(h.p, o, h.trust, h.candidate)
	if err != nil {
		t.Fatal(err)
	}
	steps := c4DriverSteps(plan)[7:]
	steps = append([]c4ProcessStep{steps[0]}, steps...)
	for i := range steps {
		steps[i].out = strings.ReplaceAll(steps[i].out, "/synthetic/candidate", h.candidate)
	}
	return h, &c4ScriptProcess{t: t, steps: steps, trace: &h.trace}, &c4CountingTrust{trace: &h.trace}
}

// Catches removal of Enabled as the sole rejection reason. Both calls have a
// valid candidate, approval/digests, trust declaration and working fake process.
// Safe to run with a gate-removal mutation: no native boundary is constructed.
func TestC4DockerDisabledGateWithValidInputs(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		name := "execute"
		if cleanup {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			h, f, trust := newC4DockerHarness(t)
			if cleanup {
				h, f, trust = c4DockerDisabledRecoveryFixture(t)
			}
			config := C4DockerConfig{Trust: h.trust}
			run := func() (domain.C4Operation, error) {
				if cleanup {
					return cleanupC4Docker(context.Background(), h.p, h.op, config, h.candidate, h.op.ContainerID, h.persist, f, trust)
				}
				return executeC4Docker(context.Background(), h.p, h.op, config, h.candidate, h.persist, f, trust)
			}
			got, err := run()
			if err != errC4Docker || len(h.trace) != 0 || f.calls != 0 || trust.calls != 0 || !reflect.DeepEqual(got, h.op) {
				t.Fatalf("disabled gate bypassed: err=%v process=%d trust=%d trace=%v", err, f.calls, trust.calls, h.trace)
			}
			// Changing only Enabled must permit this exact fixture to complete.
			config.Enabled = true
			got, err = run()
			if err != nil || got.CleanupState() != "confirmed" || f.calls == 0 || trust.calls == 0 || len(h.trace) == 0 || (!cleanup && !got.Passed()) || (cleanup && got.Result.Class != "outcome_unknown") {
				t.Fatal("otherwise-valid fixture could not complete", got, err, h.trace)
			}
		})
	}
}

// Catches bypassing durable intent, stage reordering, and stale approvals replaying effects.
func TestC4DockerLifecycleDurableSuccessAndNoReplay(t *testing.T) {
	h, f, trust := newC4DockerHarness(t)
	o, err := runC4DockerHarness(h, f, trust)
	if err != nil || !o.Passed() || o.Result.Output != "verified\nbenign CLI configuration warning\n" {
		t.Fatalf("pass missing: %+v %v trace=%v", o, err, h.trace)
	}
	want := []string{"persist:dispatch", "trust", "process:image inspect", "trust", "process:create --pull=never", "trust", "process:container inspect", "persist:created", "trust", "process:container inspect", "trust", "process:container start", "trust", "process:container inspect", "persist:started", "trust", "process:container wait", "trust", "process:container inspect", "persist:result", "trust", "process:container inspect", "trust", "process:container inspect", "trust", "process:container rm", "persist:cleanup"}
	if !reflect.DeepEqual(h.trace, want) {
		t.Fatal("durability ordering", h.trace)
	}
	before := f.calls
	h.trace = nil
	if _, err = runC4DockerHarness(h, f, trust); !errors.Is(err, ErrC4Persistence) || f.calls != before || !reflect.DeepEqual(h.trace, []string{"persist:dispatch"}) {
		t.Fatal("stale CAS replay", err, h.trace)
	}
	h.op = o
	h.trace = nil
	if _, err = runC4DockerHarness(h, f, trust); err == nil || f.calls != before || len(h.trace) != 0 {
		t.Fatal("completed replay", err, h.trace)
	}
}

// Catches any further driver effect after each failed or uncertain durable write.
func TestC4DockerLifecyclePersistenceStopsEffects(t *testing.T) {
	for _, stage := range []string{"dispatch", "created", "started", "result", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			h, f, trust := newC4DockerHarness(t)
			h.failPersist = stage
			o, err := runC4DockerHarness(h, f, trust)
			if !errors.Is(err, ErrC4Persistence) || o.Passed() || len(h.trace) == 0 || h.trace[len(h.trace)-1] != "persist:"+stage {
				t.Fatal("effect after failed write", err, h.trace)
			}
		})
	}
}

// Catches probing before structural authorization or dispatch CAS.
func TestC4DockerLifecycleInvalidApprovalHasNoEffects(t *testing.T) {
	for _, name := range []string{"mode", "missing approval", "foreign approval", "stale material", "not approved", "nil persist", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			h, f, trust := newC4DockerHarness(t)
			config := C4DockerConfig{Enabled: true, Trust: h.trust}
			ctx := context.Background()
			persist := h.persist
			switch name {
			case "mode":
				h.p.Mode = "synthetic"
			case "missing approval":
				h.op.Approval = nil
			case "foreign approval":
				h.op.Approval.Digest = strings.Repeat("f", 64)
			case "stale material":
				h.op.MaterialDigest = strings.Repeat("f", 64)
			case "not approved":
				h.op.State = "prepared"
			case "nil persist":
				persist = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := executeC4Docker(ctx, h.p, h.op, config, "/nonexistent/candidate", persist, f, trust)
			if err != errC4Docker || len(h.trace) != 0 || f.calls != 0 || trust.calls != 0 {
				t.Fatal("invalid approval reached effects", err, h.trace)
			}
		})
	}
}

// Catches new Docker transport entering the synthetic authority path.
func TestC4DockerDriverCannotBecomeSynthetic(t *testing.T) {
	h, f, trust := newC4DockerHarness(t)
	plan, _ := BuildC4Plan(h.p, h.op, h.trust, h.candidate)
	d := newC4DockerDriver(plan, f, trust, "")
	synthetic := NewC4SyntheticDriver(d)
	if synthetic != nil {
		t.Fatal("real driver wrapped as synthetic")
	}
}

// Catches recovery replaying execution, changing the result, or deleting a foreign ID.
func TestC4DockerLifecycleCleanupRecovery(t *testing.T) {
	// Recovery fixtures start with a valid already-recorded unknown terminal result.
	h, f, trust := newC4DockerHarness(t)
	o, err := runC4DockerHarness(h, f, trust)
	if err != nil {
		t.Fatal(err)
	}
	o.Result.Class = "outcome_unknown"
	o.Cleanup = nil
	h.op = o
	h.mission.C4Operations = []domain.C4Operation{cloneC4Operation(o)}
	h.trace = nil
	plan, _ := BuildC4Plan(h.p, o, h.trust, h.candidate)
	steps := c4DriverSteps(plan)[7:]
	steps = append([]c4ProcessStep{steps[0]}, steps...)
	for i := range steps {
		steps[i].out = strings.ReplaceAll(steps[i].out, "/synthetic/candidate", h.candidate)
	}
	f = &c4ScriptProcess{t: t, steps: steps, trace: &h.trace}
	trust = &c4CountingTrust{trace: &h.trace}
	recovered, err := cleanupC4Docker(context.Background(), h.p, o, C4DockerConfig{Enabled: true, Trust: h.trust}, h.candidate, o.ContainerID, h.persist, f, trust)
	if err != nil || recovered.Result.Class != "outcome_unknown" || recovered.CleanupState() != "confirmed" || recovered.Passed() || f.calls != 3 {
		t.Fatal("recovery replay/promoted", recovered, err, h.trace)
	}
}

// Catches a non-pass escaping the shared lifecycle as pass/repair, cleanup
// occurring for an unowned object, and unacknowledged execution being invented.
func TestC4DockerLifecycleFaultMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, class, cleanup string
		started              bool
	}{
		{"test failure", "test_failed", "confirmed", true}, {"wait disagreement", "infrastructure_failed", "confirmed", true}, {"wait timeout", "timeout", "confirmed", true}, {"wait cancellation", "interrupted", "confirmed", true}, {"oom", "oom", "confirmed", true},
		{"start cancel", "outcome_unknown", "confirmed", false}, {"start timeout", "outcome_unknown", "confirmed", false}, {"startup127", "infrastructure_failed", "confirmed", false}, {"missing started fact", "outcome_unknown", "confirmed", false},
		{"malformed envelope", "infrastructure_failed", "confirmed", true}, {"truncated logs", "passed", "confirmed", true}, {"foreign created", "outcome_unknown", "pending", false}, {"foreign result", "outcome_unknown", "pending", true}, {"foreign cleanup", "passed", "failed", true}, {"remove failure", "passed", "failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, trust := newC4DockerHarness(t)
			switch tc.name {
			case "test failure":
				f.steps[4].out = `{"class":"test_failed","exit_code":1,"tests":1,"output":"password=synthetic-secret","truncated":false}`
				f.steps[4].exit = 1
				f.steps[6].out = "1\n"
				for _, i := range []int{5, 7, 8, 9} {
					f.steps[i].out = strings.Replace(f.steps[i].out, `"ExitCode":0`, `"ExitCode":1`, 1)
				}
			case "wait disagreement":
				f.steps[6].out = "1\n"
			case "wait timeout":
				f.steps[6].err = context.DeadlineExceeded
			case "wait cancellation":
				f.steps[6].err = context.Canceled
			case "oom":
				f.steps[7].out = strings.Replace(f.steps[7].out, `"OOMKilled":false`, `"OOMKilled":true`, 1)
			case "start cancel", "start timeout":
				if tc.name == "start cancel" {
					f.steps[4].err = context.Canceled
				} else {
					f.steps[4].err = context.DeadlineExceeded
				}
				f.steps = append(f.steps[:5], f.steps[8:]...)
			case "startup127", "missing started fact":
				f.steps[5].out = strings.Replace(f.steps[5].out, "2026-10-21T00:00:01Z", "0001-01-01T00:00:00Z", 1)
				if tc.name == "startup127" {
					f.steps[4].exit = 127
					f.steps[5].out = strings.Replace(f.steps[5].out, `"ExitCode":0`, `"ExitCode":127`, 1)
				}
				f.steps = append(f.steps[:6], f.steps[8:]...)
			case "malformed envelope":
				f.steps[4].out = "not metadata"
			case "truncated logs":
				f.steps[4].out = strings.Replace(c4PassedWire, `"truncated":false`, `"truncated":true`, 1)
			case "foreign created":
				f.steps = f.steps[:3]
				f.steps[2].out = strings.Replace(f.steps[2].out, "owner-1", "foreign", 1)
			case "foreign result":
				f.steps = f.steps[:8]
				f.steps[7].out = strings.Replace(f.steps[7].out, "owner-1", "foreign", 1)
			case "foreign cleanup":
				f.steps = f.steps[:9]
				f.steps[8].out = strings.Replace(f.steps[8].out, "owner-1", "foreign", 1)
			case "remove failure":
				f.steps[10].err = errors.New("private removal error")
			}
			o, err := runC4DockerHarness(h, f, trust)
			if o.Result == nil || o.Result.Class != tc.class || o.CleanupState() != tc.cleanup || (o.Started != nil) != tc.started || strings.Contains(o.Result.Output, "synthetic-secret") {
				t.Fatalf("fault classification: result=%+v cleanup=%s started=%v err=%v trace=%v", o.Result, o.CleanupState(), o.Started, err, h.trace)
			}
			if (tc.name == "missing started fact" || tc.name == "startup127") && !strings.Contains(o.Result.Output, "phase=start_postcheck") {
				t.Fatal("post-start state rejection lost its stage", o.Result.Output)
			}
			if tc.name == "truncated logs" {
				if err != nil || !o.Result.Truncated || !o.Passed() {
					t.Fatal("lost bounded human-output truncation", o, err)
				}
			} else if err == nil || o.Passed() {
				t.Fatal("fault passed", o, err)
			}
			if f.calls != len(f.steps) {
				t.Fatal("unconsumed expected lifecycle", f.calls, len(f.steps))
			}
		})
	}
}

// Catches the recovery wrapper using an unapproved, foreign, or already-cleaned
// operation to reach trust/process effects. No candidate path is probed.
func TestC4DockerCleanupRejectsInvalidAuthority(t *testing.T) {
	for _, name := range []string{"missing approval", "foreign approval", "foreign id", "short id", "no terminal", "confirmed", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			h, f, trust := newC4DockerHarness(t)
			o, err := runC4DockerHarness(h, f, trust)
			if err != nil {
				t.Fatal(err)
			}
			o.Cleanup = nil
			id := o.ContainerID
			config := C4DockerConfig{Enabled: true, Trust: h.trust}
			ctx := context.Background()
			switch name {
			case "missing approval":
				o.Approval = nil
			case "foreign approval":
				o.Approval.Digest = strings.Repeat("f", 64)
			case "foreign id":
				id = strings.Repeat("e", 64)
			case "short id":
				id = "dddd"
			case "no terminal":
				o.Result = nil
			case "confirmed":
				o = h.mission.C4Operations[0]
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			h.trace = nil
			calls, probes := f.calls, trust.calls
			_, err = cleanupC4Docker(ctx, h.p, o, config, "/nonexistent/candidate", id, h.persist, f, trust)
			if err != errC4Docker || len(h.trace) != 0 || f.calls != calls || trust.calls != probes {
				t.Fatal("invalid recovery reached effects", err, h.trace)
			}
		})
	}
}

// Catches demanding an invented created/started fact after an ambiguous create,
// or upgrading its durable unknown outcome during explicitly requested cleanup.
func TestC4DockerCleanupKnownIDWithoutCreatedFact(t *testing.T) {
	h, _, _ := newC4DockerHarness(t)
	o := cloneC4Operation(h.op)
	o.Dispatch = &domain.C4Fact{Revision: 2, At: o.PreparedAt}
	o.Result = &domain.C4Result{Class: "outcome_unknown", Revision: 3, At: o.PreparedAt}
	o.State = "finished"
	h.mission.Revision = 4
	h.mission.C4Operations = []domain.C4Operation{cloneC4Operation(o)}
	plan, err := BuildC4Plan(h.p, o, h.trust, h.candidate)
	if err != nil {
		t.Fatal(err)
	}
	steps := c4DriverSteps(plan)[7:]
	steps = append([]c4ProcessStep{steps[0]}, steps...)
	for i := range steps {
		steps[i].out = strings.ReplaceAll(steps[i].out, "/synthetic/candidate", h.candidate)
	}
	f := &c4ScriptProcess{t: t, steps: steps}
	trust := &c4CountingTrust{}
	got, err := cleanupC4Docker(context.Background(), h.p, o, C4DockerConfig{Enabled: true, Trust: h.trust}, h.candidate, strings.Repeat("d", 64), h.persist, f, trust)
	if err != nil || got.ContainerID != "" || got.Created != nil || got.Started != nil || got.Result.Class != "outcome_unknown" || got.Passed() || got.CleanupState() != "confirmed" || f.calls != 3 {
		t.Fatal("recovery invented facts", got, err)
	}
}
