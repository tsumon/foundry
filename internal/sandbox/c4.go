package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"io"
	"reflect"
	"time"
)

var ErrC4Persistence = errors.New("C4 persistence unconfirmed; stop and recover explicitly")
var ErrC4Execution = errors.New("C4 execution did not produce a clean verified pass")

// C4StartupError is a known transport/entrypoint failure, not a test result.
// All other start errors are ambiguous and classified outcome_unknown.
type C4StartupError struct{ ExitCode int }

func (C4StartupError) Error() string { return "C4 startup failed" }

// C4Driver is an injected typed effect seam, not an executable selector.
// Implementations must honor context cancellation. All inspection facts and
// verifier metadata come from this trusted process-side seam, never client JSON
// or model/log instructions. Production A/B remains synthetic-only.
// Start must confirm an exact-owned container start fact, not merely launch an
// attach subprocess. It must continuously drain attached output into bounded
// capture (retaining truncation); the dormant Docker driver joins attach first.
// Logs drains that capture only: --log-driver=none rules out Docker logs.
type C4Driver interface {
	Create(context.Context, C4Command) (string, error)
	Start(context.Context, C4Command) error
	Wait(context.Context, C4Command) error
	Inspect(context.Context, C4Command) (C4Inspection, error)
	Logs(context.Context, C4Command, io.Writer) (truncated bool, err error)
	Remove(context.Context, C4Command) error
}

type C4Inspection struct {
	ID, Name           string
	Labels             map[string]string
	Running, OOMKilled bool
	ExitCode           int
	Verification       *c4verify.Result
}

// C4SyntheticDriver seals synthetic provenance in process, not in request data.
// Construction is for a disposable acceptance runtime/test fixture only. It is
// deliberately not serializable or inferred from C4Profile.Mode.
type C4SyntheticDriver struct{ driver C4Driver }

func NewC4SyntheticDriver(d C4Driver) *C4SyntheticDriver {
	if _, real := d.(*c4DockerDriver); real {
		return nil
	}
	return &C4SyntheticDriver{driver: d}
}
func (*C4SyntheticDriver) VerificationSource() string { return "synthetic" }

// C4Change contains one append-only durable stage. Apply supplies transaction
// revision/time; it does not grant authorization or replace store validation.
type C4Change struct {
	Stage, ContainerID string
	Result             *domain.C4Result
	Cleanup            *domain.C4Cleanup
}

func (c C4Change) Apply(o *domain.C4Operation, f domain.C4Fact) error {
	if o == nil || f.Revision < 1 || f.At == "" {
		return errors.New("invalid C4 durable fact")
	}
	switch c.Stage {
	case "dispatch":
		if o.State != "approved" || o.Approval == nil || o.Dispatch != nil {
			return errors.New("C4 dispatch already consumed or unapproved")
		}
		o.Dispatch = &f
		o.State = "dispatched"
	case "created":
		if o.State != "dispatched" || !c4ContainerID.MatchString(c.ContainerID) {
			return errors.New("invalid created fact")
		}
		o.ContainerID = c.ContainerID
		o.Created = &f
		o.State = "created"
	case "started":
		if o.State != "created" {
			return errors.New("invalid started fact")
		}
		o.Started = &f
		o.State = "started"
	case "result":
		if o.Dispatch == nil || o.Result != nil || c.Result == nil {
			return errors.New("invalid terminal fact")
		}
		r := *c.Result
		r.Revision = f.Revision
		r.At = f.At
		o.Result = &r
		o.State = "finished"
	case "cleanup":
		if o.Result == nil || o.CleanupState() == "confirmed" || c.Cleanup == nil {
			return errors.New("invalid cleanup fact")
		}
		v := *c.Cleanup
		v.Revision = f.Revision
		v.At = f.At
		o.Cleanup = append(append([]domain.C4Cleanup{}, o.Cleanup...), v)
	default:
		return errors.New("unsupported C4 persistence stage")
	}
	return nil
}

// C4Persist must compare the entire expected operation within the mission CAS,
// validate the current epoch/material/approval and domain transition, call
// change.Apply with the transaction revision/time, and return the committed
// operation. An error (including uncertain commit) stops ALL subsequent effects.
// The returned operation is checked, but CAS/durability are the store's duty.
type C4Persist func(context.Context, domain.C4Operation, C4Change) (domain.C4Operation, error)

func cloneC4Operation(o domain.C4Operation) domain.C4Operation {
	b, _ := json.Marshal(o)
	var n domain.C4Operation
	_ = json.Unmarshal(b, &n)
	return n
}

func persistC4(ctx context.Context, p C4Persist, o domain.C4Operation, c C4Change) (domain.C4Operation, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, err := p(ctx, cloneC4Operation(o), c)
	if err != nil {
		return o, ErrC4Persistence
	}
	f := domain.C4Fact{}
	switch c.Stage {
	case "dispatch":
		if n.Dispatch != nil {
			f = *n.Dispatch
		}
	case "created":
		if n.Created != nil {
			f = *n.Created
		}
	case "started":
		if n.Started != nil {
			f = *n.Started
		}
	case "result":
		if n.Result != nil {
			f = domain.C4Fact{Revision: n.Result.Revision, At: n.Result.At}
		}
	case "cleanup":
		if len(n.Cleanup) > 0 {
			last := n.Cleanup[len(n.Cleanup)-1]
			f = domain.C4Fact{Revision: last.Revision, At: last.At}
		}
	}
	expected := cloneC4Operation(o)
	if c.Apply(&expected, f) != nil || !reflect.DeepEqual(expected, n) {
		return o, ErrC4Persistence
	}
	return cloneC4Operation(n), nil
}

func c4Enabled(p domain.C4Profile, d *C4SyntheticDriver, persist C4Persist) bool {
	return p.Mode == "synthetic" && d != nil && d.driver != nil && persist != nil
}

func ownedC4(o domain.C4Operation, id string, v C4Inspection) bool {
	return c4ContainerID.MatchString(id) && v.ID == id && v.Name == o.ContainerName && v.Labels["foundry.mission"] == o.MissionID && v.Labels["foundry.operation"] == o.ID && v.Labels["foundry.owner"] == o.Owner && v.Labels["foundry.name"] == o.ContainerName
}

func inspectC4(plan C4Plan, d C4Driver, id string) (C4Inspection, error) {
	c, err := plan.Command("inspect", id)
	if err != nil {
		return C4Inspection{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := d.Inspect(ctx, c)
	if err != nil {
		return C4Inspection{}, err
	}
	if !ownedC4(plan.operation, id, v) {
		return C4Inspection{}, errors.New("C4 exact ownership not established")
	}
	return v, nil
}

// ExecuteC4 consumes a durable approved operation exactly once. All external
// effects are injected synthetic calls. Production docker mode stays closed.
func ExecuteC4(ctx context.Context, p domain.C4Profile, o domain.C4Operation, trust C4Trust, candidate string, d *C4SyntheticDriver, persist C4Persist) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if !c4Enabled(p, d, persist) || o.State != "approved" || o.Dispatch != nil || ctx.Err() != nil {
		return o, ErrC4Execution
	}
	plan, err := BuildC4Plan(p, o, trust, candidate)
	if err != nil {
		return o, err
	}
	return executeC4(ctx, plan, o, d.driver, persist)
}

// Shared durable core. Wrappers establish distinct authority before entering;
// a driver is never converted into synthetic authority to reuse the lifecycle.
func executeC4(ctx context.Context, plan C4Plan, o domain.C4Operation, d C4Driver, persist C4Persist) (domain.C4Operation, error) {
	var err error
	o, err = persistC4(ctx, persist, o, C4Change{Stage: "dispatch"})
	if err != nil {
		return o, err
	}
	finish := func(result domain.C4Result, cleanupID string) (domain.C4Operation, error) {
		var err error
		o, err = persistC4(context.Background(), persist, o, C4Change{Stage: "result", Result: &result})
		if err != nil {
			return o, err
		}
		if cleanupID != "" {
			o, err = cleanupC4(plan, o, cleanupID, d, persist)
			if err != nil {
				return o, err
			}
		}
		if !o.Passed() {
			return o, ErrC4Execution
		}
		return o, nil
	}
	if ctx.Err() != nil {
		return finish(domain.C4Result{Class: "interrupted"}, "")
	}
	createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	id, err := d.Create(createCtx, plan.Create())
	cancel()
	// An ambiguous create never invents an ID or discovers/removes by name.
	if err != nil || !c4ContainerID.MatchString(id) {
		return finish(c4PrestartFailure("create", err, ""), "")
	}
	if _, err = inspectC4(plan, d, id); err != nil {
		return finish(c4PrestartFailure("initial_inspect", err, id), "")
	}
	o, err = persistC4(context.Background(), persist, o, C4Change{Stage: "created", ContainerID: id})
	if err != nil {
		return o, err
	}
	if ctx.Err() != nil {
		return finish(domain.C4Result{Class: "interrupted"}, id)
	}
	execCtx, cancelExec := context.WithTimeout(ctx, 120*time.Second)
	defer cancelExec()
	command, _ := plan.Command("start", id)
	if err = d.Start(execCtx, command); err != nil {
		r := c4StartFailure(err)
		var startup C4StartupError
		// No Start acknowledgment means no proven Started fact. Preserve the
		// bounded cause without manufacturing timeout/interrupted execution.
		switch {
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded):
			r.Output += " C4 start acknowledgment deadline exceeded; execution outcome unknown."
		case errors.Is(err, context.Canceled) || errors.Is(execCtx.Err(), context.Canceled):
			r.Output += " C4 start acknowledgment canceled; execution outcome unknown."
		case errors.As(err, &startup) && (startup.ExitCode == 125 || startup.ExitCode == 126 || startup.ExitCode == 127):
			r.Class = "infrastructure_failed"
			r.ExitCode = &startup.ExitCode
		}
		return finish(r, id)
	}
	o, err = persistC4(context.Background(), persist, o, C4Change{Stage: "started"})
	if err != nil {
		return o, err
	}
	command, _ = plan.Command("wait", id)
	waitErr := d.Wait(execCtx, command)
	v, err := inspectC4(plan, d, id)
	if err != nil {
		return finish(domain.C4Result{Class: "outcome_unknown"}, "")
	}
	r := classifyC4(v, waitErr, execCtx.Err())
	output := c4verify.NewOutput()
	logCtx, cancelLogs := context.WithTimeout(context.Background(), 10*time.Second)
	command, _ = plan.Command("logs", id)
	captureTruncated, err := d.Logs(logCtx, command, output)
	cancelLogs()
	r.Output, r.Truncated = output.Text()
	r.Truncated = r.Truncated || captureTruncated
	if err != nil {
		r.Class = "outcome_unknown"
	}
	return finish(r, id)
}

func classifyC4(v C4Inspection, waitErr, ctxErr error) domain.C4Result {
	exit := v.ExitCode
	r := domain.C4Result{Class: "outcome_unknown", ExitCode: &exit}
	if v.OOMKilled {
		r.Class = "oom"
		return r
	}
	if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) {
		r.Class = "timeout"
		return r
	}
	if errors.Is(waitErr, context.Canceled) || errors.Is(ctxErr, context.Canceled) {
		r.Class = "interrupted"
		return r
	}
	if waitErr != nil || v.Running {
		return r
	}
	r.Class = "infrastructure_failed"
	if exit == 125 || exit == 126 || exit == 127 || v.Verification == nil {
		return r
	}
	verified := v.Verification
	if verified.ExitCode != exit || verified.Tests < 0 {
		return r
	}
	r.Tests = verified.Tests
	if exit == 0 && verified.Class == "passed" && verified.Tests > 0 {
		r.Class = "passed"
	}
	if exit != 0 && verified.Class == "test_failed" {
		r.Class = "test_failed"
	}
	return r
}

func cleanupC4(plan C4Plan, o domain.C4Operation, id string, d C4Driver, persist C4Persist) (domain.C4Operation, error) {
	if _, err := inspectC4(plan, d, id); err != nil {
		// A failed precheck is diagnostic only, not ownership confirmation or
		// deletion authority. Preserve the failure without changing the result.
		var saveErr error
		o, saveErr = persistC4(context.Background(), persist, o, C4Change{Stage: "cleanup", Cleanup: c4CleanupFailure(o, id, "cleanup_precheck", err)})
		if saveErr != nil {
			return o, saveErr
		}
		return o, ErrC4Execution
	}
	c, _ := plan.Command("remove", id)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := d.Remove(ctx, c)
	cancel()
	cleanup := &domain.C4Cleanup{State: "confirmed", ContainerID: id, Owner: o.Owner}
	if err != nil {
		cleanup = c4CleanupFailure(o, id, "cleanup_remove", err)
	}
	o, saveErr := persistC4(context.Background(), persist, o, C4Change{Stage: "cleanup", Cleanup: cleanup})
	if saveErr != nil {
		return o, saveErr
	}
	if err != nil {
		return o, ErrC4Execution
	}
	return o, nil
}

// CleanupC4 is explicit recovery only. It never creates, starts, waits, scans
// names or sweeps owners. An unknown terminal result remains unknown forever.
// If creation was not recorded, the caller must supply a separately known exact
// ID; inspect must establish all ownership fields before any removal.
func CleanupC4(ctx context.Context, p domain.C4Profile, o domain.C4Operation, trust C4Trust, id string, d *C4SyntheticDriver, persist C4Persist) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if !c4Enabled(p, d, persist) || ctx.Err() != nil || o.Dispatch == nil || o.Result == nil || o.CleanupState() == "confirmed" || len(o.Cleanup) >= domain.C4OperationLimit || !c4ContainerID.MatchString(id) || (o.ContainerID != "" && o.ContainerID != id) {
		return o, ErrC4Execution
	}
	if len(o.Cleanup) > 0 && o.Cleanup[0].ContainerID != id {
		return o, ErrC4Execution
	}
	if err := validateC4Binding(p, o, trust); err != nil {
		return o, err
	}
	return cleanupC4(C4Plan{profile: p, operation: o}, o, id, d.driver, persist)
}
