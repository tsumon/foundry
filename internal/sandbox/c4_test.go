package sandbox

import (
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type c4DriverFixture struct {
	t                *testing.T
	op               domain.C4Operation
	trace            *[]string
	fail             string
	inspection       C4Inspection
	logs             string
	captureTruncated bool
	inspectCount     int
}

func (f *c4DriverFixture) record(ctx context.Context, c C4Command, stage string) error {
	f.t.Helper()
	*f.trace = append(*f.trace, stage)
	d, ok := ctx.Deadline()
	bound := 10 * time.Second
	if stage == "start" || stage == "wait" {
		bound = 120 * time.Second
	}
	if !ok || time.Until(d) > bound || ctx.Err() != nil {
		f.t.Fatalf("unbounded/cancelled %s context", stage)
	}
	if stage == "logs" {
		if c.Path != "" || len(c.Args) != 0 || len(c.Env) != 0 || c.CaptureID != strings.Repeat("d", 64) {
			f.t.Fatal("logs must drain exact attached capture", c)
		}
		if f.fail == stage {
			return errors.New("injected error")
		}
		return nil
	}
	if c.Path != "/synthetic/docker" || !reflect.DeepEqual(c.Args[:2], []string{"--host", "unix:///synthetic/docker.sock"}) || !reflect.DeepEqual(c.Env, []string{"DOCKER_CONFIG=/dev/null"}) {
		f.t.Fatal("unsafe command", c)
	}
	if stage != "create" && c.Args[len(c.Args)-1] != strings.Repeat("d", 64) {
		f.t.Fatal("not exact ID", c)
	}
	if stage == "start" && f.fail == "startup127" {
		return C4StartupError{ExitCode: 127}
	}
	if f.fail == stage {
		return errors.New("injected error")
	}
	return nil
}
func (f *c4DriverFixture) Create(ctx context.Context, c C4Command) (string, error) {
	err := f.record(ctx, c, "create")
	if err != nil {
		return "", err
	}
	return strings.Repeat("d", 64), nil
}
func (f *c4DriverFixture) Start(ctx context.Context, c C4Command) error {
	err := f.record(ctx, c, "start")
	if f.fail == "start timeout" {
		return context.DeadlineExceeded
	}
	if f.fail == "start cancel" {
		return context.Canceled
	}
	return err
}
func (f *c4DriverFixture) Wait(ctx context.Context, c C4Command) error {
	err := f.record(ctx, c, "wait")
	if f.fail == "timeout" {
		return context.DeadlineExceeded
	}
	if f.fail == "cancel" {
		return context.Canceled
	}
	return err
}
func (f *c4DriverFixture) Inspect(ctx context.Context, c C4Command) (C4Inspection, error) {
	f.inspectCount++
	err := f.record(ctx, c, "inspect")
	if f.fail == "inspect outcome" && f.inspectCount == 2 {
		err = errors.New("inspection unavailable")
	}
	if f.fail == "foreign cleanup" && f.inspectCount == 3 {
		v := f.inspection
		v.Name = "foreign"
		return v, nil
	}
	return f.inspection, err
}
func (f *c4DriverFixture) Logs(ctx context.Context, c C4Command, w io.Writer) (bool, error) {
	err := f.record(ctx, c, "logs")
	io.WriteString(w, f.logs)
	return f.captureTruncated, err
}
func (f *c4DriverFixture) Remove(ctx context.Context, c C4Command) error {
	return f.record(ctx, c, "remove")
}

type c4Harness struct {
	p           domain.C4Profile
	op          domain.C4Operation
	trust       C4Trust
	candidate   string
	driver      *c4DriverFixture
	trace       []string
	mission     domain.Mission
	failPersist string
}

func newC4Harness(t *testing.T) *c4Harness {
	p, o, trust, candidate := c4PlanFixture(t)
	h := &c4Harness{p: p, op: o, trust: trust, candidate: candidate}
	h.mission = domain.Mission{ID: o.MissionID, Epoch: o.Epoch, Revision: 2, BaseCommit: o.BaseCommit, Spec: domain.Spec{C4: &p}, C4Operations: []domain.C4Operation{o}}
	h.driver = &c4DriverFixture{t: t, op: o, trace: &h.trace, inspection: C4Inspection{ID: strings.Repeat("d", 64), Name: o.ContainerName, Labels: map[string]string{"foundry.owner": o.Owner, "foundry.operation": o.ID, "foundry.mission": o.MissionID, "foundry.name": o.ContainerName}, ExitCode: 0, Verification: &c4verify.Result{Class: "passed", ExitCode: 0, Tests: 1}}}
	return h
}
func (h *c4Harness) persist(ctx context.Context, before domain.C4Operation, change C4Change) (domain.C4Operation, error) {
	h.trace = append(h.trace, "persist:"+change.Stage)
	if _, ok := ctx.Deadline(); !ok {
		return before, errors.New("unbounded persistence")
	}
	if h.failPersist == change.Stage {
		return before, errors.New("persistence unavailable")
	}
	if !reflect.DeepEqual(before, h.mission.C4Operations[0]) {
		return before, errors.New("CAS mismatch")
	}
	next := h.mission
	next.C4Operations = append([]domain.C4Operation{}, h.mission.C4Operations...)
	if err := change.Apply(&next.C4Operations[0], domain.C4Fact{Revision: h.mission.Revision, At: "2026-10-21T00:00:01Z"}); err != nil {
		return before, err
	}
	next.Revision++
	if err := domain.ValidateC4Transition(h.mission, next); err != nil {
		return before, err
	}
	h.mission = next
	return next.C4Operations[0], nil
}
func (h *c4Harness) run() (domain.C4Operation, error) {
	return ExecuteC4(context.Background(), h.p, h.op, h.trust, h.candidate, NewC4SyntheticDriver(h.driver), h.persist)
}

func TestC4LifecyclePersistsBeforeEffectsAndRejectsRedispatch(t *testing.T) {
	h := newC4Harness(t)
	o, err := h.run()
	if err != nil || !o.Passed() {
		t.Fatalf("not clean pass: %+v %v trace=%v", o, err, h.trace)
	}
	want := []string{"persist:dispatch", "create", "inspect", "persist:created", "start", "persist:started", "wait", "inspect", "logs", "persist:result", "inspect", "remove", "persist:cleanup"}
	if !reflect.DeepEqual(h.trace, want) {
		t.Fatal(h.trace)
	}
	h.trace = nil
	if _, err = h.run(); err == nil || !reflect.DeepEqual(h.trace, []string{"persist:dispatch"}) {
		t.Fatal("replayed stale approved operation", err, h.trace)
	}
	h.op = o
	h.trace = nil
	if _, err = h.run(); err == nil || len(h.trace) != 0 {
		t.Fatal("replayed completed operation", err, h.trace)
	}
}

func TestC4PersistenceFailureStopsNextEffect(t *testing.T) {
	for _, stage := range []string{"dispatch", "created", "started", "result", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			h := newC4Harness(t)
			h.failPersist = stage
			o, err := h.run()
			if !errors.Is(err, ErrC4Persistence) || o.Passed() {
				t.Fatal("persistence failure promoted", o, err)
			}
			if h.trace[len(h.trace)-1] != "persist:"+stage {
				t.Fatal("effect after failed persist", h.trace)
			}
		})
	}
}

func TestC4UnacknowledgedStartRecordsCauseWithoutInventingStarted(t *testing.T) {
	for _, tc := range []struct{ failure, cause string }{
		{"start timeout", "C4 phase=start_ack reason=timeout; execution outcome unknown. C4 start acknowledgment deadline exceeded; execution outcome unknown."},
		{"start cancel", "C4 phase=start_ack reason=canceled; execution outcome unknown. C4 start acknowledgment canceled; execution outcome unknown."},
	} {
		t.Run(tc.failure, func(t *testing.T) {
			h := newC4Harness(t)
			h.driver.fail = tc.failure
			o, err := h.run()
			if !errors.Is(err, ErrC4Execution) || o.Started != nil || o.Result == nil || o.Result.Class != "outcome_unknown" || o.Result.Output != tc.cause || o.Passed() {
				t.Fatalf("lost start cause or fabricated execution: result=%+v started=%+v err=%v", o.Result, o.Started, err)
			}
			want := []string{"persist:dispatch", "create", "inspect", "persist:created", "start", "persist:result", "inspect", "remove", "persist:cleanup"}
			if !reflect.DeepEqual(h.trace, want) || o.CleanupState() != "confirmed" || o.Cleanup[0].ContainerID != strings.Repeat("d", 64) || o.Cleanup[0].Owner != o.Owner {
				t.Fatalf("unexpected retry/wait or non-owned cleanup: trace=%v cleanup=%+v", h.trace, o.Cleanup)
			}
		})
	}
}

func TestC4FaultsNeverManufacturePassOrTouchForeignObject(t *testing.T) {
	for _, tc := range []struct {
		name, class string
		cleanup     bool
	}{
		{"create", "outcome_unknown", false}, {"start", "outcome_unknown", true}, {"startup127", "infrastructure_failed", true}, {"inspect", "outcome_unknown", false}, {"inspect outcome", "outcome_unknown", false},
		{"wait", "outcome_unknown", true}, {"timeout", "timeout", true}, {"cancel", "interrupted", true}, {"oom", "oom", true},
		{"125", "infrastructure_failed", true}, {"126", "infrastructure_failed", true}, {"127", "infrastructure_failed", true},
		{"no tests", "infrastructure_failed", true}, {"malformed verifier", "infrastructure_failed", true}, {"test failure", "test_failed", true},
		{"logs", "outcome_unknown", true}, {"remove", "passed", false}, {"foreign", "outcome_unknown", false}, {"foreign cleanup", "passed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newC4Harness(t)
			h.driver.fail = tc.name
			switch tc.name {
			case "oom":
				h.driver.inspection.OOMKilled = true
			case "125":
				h.driver.inspection.ExitCode = 125
			case "126":
				h.driver.inspection.ExitCode = 126
			case "127":
				h.driver.inspection.ExitCode = 127
			case "no tests":
				h.driver.inspection.Verification.Tests = 0
			case "malformed verifier":
				h.driver.inspection.Verification = nil
			case "test failure":
				h.driver.inspection.ExitCode = 1
				h.driver.inspection.Verification = &c4verify.Result{Class: "test_failed", ExitCode: 1}
			case "foreign":
				h.driver.inspection.Labels["foundry.operation"] = "other"
			}
			o, err := h.run()
			if err == nil || o.Result == nil || o.Result.Class != tc.class || o.Passed() || (o.CleanupState() == "confirmed") != tc.cleanup {
				t.Fatalf("fault=%s result=%+v err=%v trace=%v", tc.name, o, err, h.trace)
			}
			if tc.name == "foreign" || tc.name == "inspect" || tc.name == "create" {
				for _, call := range h.trace {
					if call == "start" || call == "remove" {
						t.Fatal("touched unowned object", h.trace)
					}
				}
			}
			if tc.name == "foreign cleanup" {
				for _, call := range h.trace {
					if call == "remove" {
						t.Fatal("removed foreign object")
					}
				}
			}
		})
	}
}

func TestC4OutputBoundAndRecoveryOnly(t *testing.T) {
	h := newC4Harness(t)
	h.driver.logs = "password=private\x00\n" + strings.Repeat("x", 70000)
	h.driver.fail = "remove"
	o, err := h.run()
	if err == nil || !o.Result.Truncated || len(o.Result.Output) > 65536 || strings.Contains(o.Result.Output, "private") || strings.ContainsRune(o.Result.Output, 0) {
		t.Fatal("unsafe output", err)
	}
	h.driver.fail = ""
	h.trace = nil
	o, err = CleanupC4(context.Background(), h.p, o, h.trust, o.ContainerID, NewC4SyntheticDriver(h.driver), h.persist)
	if err != nil || !o.Passed() || !reflect.DeepEqual(h.trace, []string{"inspect", "remove", "persist:cleanup"}) {
		t.Fatal("recovery replayed", err, h.trace)
	}
}

func TestC4PreservesPreboundedCaptureTruncation(t *testing.T) {
	h := newC4Harness(t)
	h.driver.logs = "bounded captured output"
	h.driver.captureTruncated = true
	o, err := h.run()
	if err != nil || !o.Result.Truncated {
		t.Fatal("lost driver truncation", o.Result, err)
	}
}

func TestC4ProductionAndMissingCapabilityFailClosed(t *testing.T) {
	h := newC4Harness(t)
	if _, err := ExecuteC4(context.Background(), h.p, h.op, h.trust, h.candidate, nil, h.persist); err == nil {
		t.Fatal("missing capability accepted")
	}
	h.p.Mode = "docker"
	h.op.ProfileDigest = h.p.ProfileDigest()
	h.op.MaterialDigest = domain.C4MaterialDigest(h.op)
	h.op.Approval.Digest = h.op.MaterialDigest
	if _, err := h.run(); err == nil || len(h.trace) != 0 {
		t.Fatal("production driver opened", h.trace)
	}
}

func TestC4RecoveryUnknownNeverPassesAndRequiresExactOwnership(t *testing.T) {
	for _, field := range []string{"id", "name", "mission", "operation", "owner", "label name", "owned"} {
		t.Run(field, func(t *testing.T) {
			h := newC4Harness(t)
			h.driver.fail = "create"
			o, _ := h.run()
			h.driver.fail = ""
			h.trace = nil
			switch field {
			case "id":
				h.driver.inspection.ID = strings.Repeat("e", 64)
			case "name":
				h.driver.inspection.Name = "other"
			case "mission":
				h.driver.inspection.Labels["foundry.mission"] = "other"
			case "operation":
				h.driver.inspection.Labels["foundry.operation"] = "other"
			case "owner":
				h.driver.inspection.Labels["foundry.owner"] = "other"
			case "label name":
				h.driver.inspection.Labels["foundry.name"] = "other"
			}
			o, err := CleanupC4(context.Background(), h.p, o, h.trust, strings.Repeat("d", 64), NewC4SyntheticDriver(h.driver), h.persist)
			if o.Passed() || o.Result.Class != "outcome_unknown" || o.ContainerID != "" || o.Created != nil {
				t.Fatal("recovery manufactured execution", o)
			}
			if field == "owned" {
				if err != nil || o.CleanupState() != "confirmed" {
					t.Fatal(err, o)
				}
			} else {
				if err == nil || !reflect.DeepEqual(h.trace, []string{"inspect", "persist:cleanup"}) || len(o.Cleanup) != 1 || o.CleanupState() != "failed" {
					t.Fatal("foreign recovery", err, h.trace, o.Cleanup)
				}
				c := o.Cleanup[0]
				if c.Phase != "cleanup_precheck" || c.Reason != "protocol_failed" || c.ContainerID != strings.Repeat("d", 64) || c.Owner != o.Owner {
					t.Fatal("foreign observation became cleanup authority", c)
				}
			}
		})
	}
}
