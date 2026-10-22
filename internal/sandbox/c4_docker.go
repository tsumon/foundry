package sandbox

import (
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// C4DockerConfig is a trusted in-process library boundary, not a request schema
// or application enable flag. Its zero value performs no trust/process effects.
// Production startup, engine, HTTP and UI never construct an enabled adapter.
type C4DockerConfig struct {
	Enabled bool
	Trust   C4Trust
}

var errC4Docker = errors.New("C4 Docker adapter unavailable or unconfirmed")
var errC4DockerCommand = errors.New("C4 Docker command failed")
var errC4DockerOutputLimit = errors.New("C4 Docker output limit exceeded")

// One instance owns one immutable plan and one exact ID. Serial methods join
// every process before returning. Reservations are consumed even on failure.
type c4DockerDriver struct {
	mu                   sync.Mutex
	plan                 C4Plan
	process              c4DockerProcess
	trust                c4DockerTrustCheck
	id                   string
	recovery             bool
	createReserved       bool
	startReserved        bool
	waitReserved         bool
	removeReserved       bool
	started, waitOK      bool
	startedAt            time.Time
	attachExit, waitExit int
	stdout, stderr       *c4DockerCapture
}

func newC4DockerDriver(p C4Plan, process c4DockerProcess, trust c4DockerTrustCheck, id string) *c4DockerDriver {
	p.operation = cloneC4Operation(p.operation)
	return &c4DockerDriver{plan: p, process: process, trust: trust, id: id, recovery: id != "", stdout: &c4DockerCapture{limit: c4DockerWireLimit}, stderr: &c4DockerCapture{limit: c4verify.OutputLimit}}
}
func (d *c4DockerDriver) matches(stage string, c C4Command) bool {
	if d.process == nil || d.trust == nil || d.removeReserved {
		return false
	}
	if stage == "create" {
		return !d.recovery && reflect.DeepEqual(c, d.plan.Create())
	}
	expected, err := d.plan.Command(stage, d.id)
	return err == nil && reflect.DeepEqual(c, expected)
}
func (d *c4DockerDriver) run(ctx context.Context, c C4Command, out, stderr io.Writer) (int, error) {
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if d.trust.Check() != nil {
		return -1, errors.Join(errC4Docker, errC4DockerTrust)
	}
	exit, err := d.process.Run(ctx, c, out, stderr)
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return -1, context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return -1, context.DeadlineExceeded
	}
	if err != nil || exit < 0 || exit > 255 {
		return -1, errors.Join(errC4Docker, errC4DockerProcess)
	}
	return exit, nil
}
func (d *c4DockerDriver) control(ctx context.Context, c C4Command, limit int) ([]byte, error) {
	out, stderr := &c4DockerCapture{limit: limit}, &c4DockerCapture{limit: c4verify.OutputLimit}
	exit, err := d.run(ctx, c, out, stderr)
	if err != nil {
		return nil, err
	}
	raw, truncated := out.snapshot()
	if exit != 0 {
		return nil, errors.Join(errC4Docker, errC4DockerCommand)
	}
	if truncated {
		return nil, errors.Join(errC4Docker, errC4DockerOutputLimit)
	}
	// stderr is human diagnostic data, including benign Docker config warnings.
	return raw, nil
}
func (d *c4DockerDriver) Create(ctx context.Context, c C4Command) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.createReserved || !d.matches("create", c) {
		return "", errC4Docker
	}
	d.createReserved = true
	image, err := d.control(ctx, d.plan.imageInspection(), c4DockerInspectLimit)
	if err != nil {
		return "", c4ImageCheckError{err}
	}
	if validateC4DockerImage(image, d.plan.profile) != nil {
		return "", c4ImageCheckError{errC4DockerProtocol}
	}
	raw, err := d.control(ctx, c, 128)
	if err != nil {
		return "", err
	}
	id := strings.TrimSuffix(string(raw), "\n")
	if !c4ContainerID.MatchString(id) {
		return "", errC4Docker
	}
	d.id = id
	return id, nil
}
func (d *c4DockerDriver) inspect(ctx context.Context) (c4DockerInspection, error) {
	c, err := d.plan.Command("inspect", d.id)
	if err != nil {
		return c4DockerInspection{}, errC4Docker
	}
	raw, err := d.control(ctx, c, c4DockerInspectLimit)
	if err != nil {
		return c4DockerInspection{}, err
	}
	raw, err = normalizeC4ContainerEmptyFields(raw)
	if err != nil {
		return c4DockerInspection{}, errC4DockerProtocol
	}
	v, err := decodeC4DockerInspection(raw, d.plan, d.id)
	if err != nil {
		return c4DockerInspection{}, errC4DockerProtocol
	}
	return v, nil
}
func (d *c4DockerDriver) Start(ctx context.Context, c C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recovery || d.startReserved || !d.matches("start", c) {
		return errC4Docker
	}
	d.startReserved = true
	before, err := d.inspect(ctx)
	if err != nil {
		return c4StageError{"start_precheck", err}
	}
	if !before.StartedAt.IsZero() || before.Running {
		return c4StageError{"start_precheck", errC4DockerProtocol}
	}
	d.attachExit, err = d.run(ctx, c, d.stdout, d.stderr)
	if err != nil {
		return c4StageError{"start_attach", err}
	}
	after, err := d.inspect(ctx)
	if err != nil {
		return c4StageError{"start_postcheck", err}
	}
	if after.StartedAt.IsZero() {
		if !after.Running && after.ExitCode == d.attachExit && (d.attachExit == 125 || d.attachExit == 126 || d.attachExit == 127) {
			return c4StageError{"start_postcheck", C4StartupError{ExitCode: d.attachExit}}
		}
		return c4StageError{"start_postcheck", errC4DockerProtocol}
	}
	d.started, d.startedAt = true, after.StartedAt
	return nil
}
func (d *c4DockerDriver) Wait(ctx context.Context, c C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recovery || !d.started || d.waitReserved || !d.matches("wait", c) {
		return errC4Docker
	}
	d.waitReserved = true
	raw, err := d.control(ctx, c, 16)
	if err != nil {
		return err
	}
	text := strings.TrimSuffix(string(raw), "\n")
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 || n > 255 || strconv.Itoa(n) != text {
		return errC4Docker
	}
	d.waitExit, d.waitOK = n, true
	return nil
}
func (d *c4DockerDriver) verification(v c4DockerInspection) *c4verify.Result {
	if !d.started || !d.waitOK || v.Running || v.OOMKilled || !v.StartedAt.Equal(d.startedAt) || d.waitExit != v.ExitCode || d.attachExit != v.ExitCode {
		return nil
	}
	raw, truncated := d.stdout.snapshot()
	if truncated {
		return nil
	}
	r, err := decodeC4Verifier(raw, v.ExitCode)
	if err != nil {
		return nil
	}
	return r
}
func (d *c4DockerDriver) Inspect(ctx context.Context, c C4Command) (C4Inspection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.matches("inspect", c) {
		return C4Inspection{}, errC4Docker
	}
	v, err := d.inspect(ctx)
	if err != nil {
		return C4Inspection{}, err
	}
	v.Verification = d.verification(v)
	return v.C4Inspection, nil
}
func (d *c4DockerDriver) Logs(ctx context.Context, c C4Command, w io.Writer) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if !d.started || !d.matches("logs", c) {
		return false, errC4Docker
	}
	raw, truncated := d.stdout.snapshot()
	stderr, stderrTruncated := d.stderr.snapshot()
	output := c4verify.NewOutput()
	// Metadata decoding is independent of logs; nested text and stderr never
	// become verification. Invalid/partial wire is not human output: JSON escapes
	// can conceal credential-shaped text from a plain-text redactor.
	if result, err := decodeC4Verifier(raw, d.attachExit); err == nil && !truncated {
		raw = []byte(result.Output)
		truncated = result.Truncated
	} else {
		raw = []byte("C4 verifier output unavailable.\n")
	}
	_, _ = output.Write(raw)
	_, _ = output.Write(stderr)
	text, clipped := output.Text()
	_, err := io.WriteString(w, text)
	if err != nil {
		return truncated || stderrTruncated || clipped, errC4Docker
	}
	return truncated || stderrTruncated || clipped, nil
}
func (d *c4DockerDriver) Remove(ctx context.Context, c C4Command) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.matches("remove", c) {
		return errC4Docker
	}
	d.removeReserved = true
	if _, err := d.inspect(ctx); err != nil {
		return c4StageError{"cleanup_remove_precheck", err}
	}
	raw, err := d.control(ctx, c, 128)
	if err != nil {
		return err
	}
	if strings.TrimSuffix(string(raw), "\n") != d.id {
		return errC4Docker
	}
	return nil
}

// ExecuteC4Docker is dormant until separately authorized startup integration.
// Construction below is effect-free. Current whole-mission approval/epoch/CAS
// authority stays in C4Persist, whose dispatch must commit before trust probes.
func ExecuteC4Docker(ctx context.Context, p domain.C4Profile, o domain.C4Operation, c C4DockerConfig, candidate string, persist C4Persist) (domain.C4Operation, error) {
	return executeC4Docker(ctx, p, o, c, candidate, persist, c4OSProcess{}, &c4DockerTrust{trust: c.Trust, fs: c4DockerOSFS{}, uid: uint32(os.Getuid())})
}

// CleanupC4Docker is explicit exact-ID recovery, never discovery or execution.
// The caller must supply the original admitted candidate binding as well as ID.
// Under the existing mission serialization, that trusted caller must authorize
// the current mission/epoch/material/known ID BEFORE invoking this function.
// C4Persist records cleanup AFTER removal; it is not a pre-removal authority
// check. Future activation must wire that caller gate; production stays closed.
func CleanupC4Docker(ctx context.Context, p domain.C4Profile, o domain.C4Operation, c C4DockerConfig, candidate, id string, persist C4Persist) (domain.C4Operation, error) {
	return cleanupC4Docker(ctx, p, o, c, candidate, id, persist, c4OSProcess{}, &c4DockerTrust{trust: c.Trust, fs: c4DockerOSFS{}, uid: uint32(os.Getuid())})
}
func c4DockerApproved(ctx context.Context, p domain.C4Profile, o domain.C4Operation, c C4DockerConfig, persist C4Persist) bool {
	return c.Enabled && p.Mode == "docker" && persist != nil && ctx.Err() == nil && o.Approval != nil && validateC4Binding(p, o, c.Trust) == nil
}
func executeC4Docker(ctx context.Context, p domain.C4Profile, o domain.C4Operation, c C4DockerConfig, candidate string, persist C4Persist, process c4DockerProcess, trust c4DockerTrustCheck) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if o.State != "approved" || o.Dispatch != nil || !c4DockerApproved(ctx, p, o, c, persist) || process == nil || trust == nil {
		return o, errC4Docker
	}
	// Managed candidate read-only preflight can precede the durable dispatch CAS;
	// executable/socket trust and all Docker calls cannot. Never return raw paths.
	plan, err := BuildC4Plan(p, o, c.Trust, candidate)
	if err != nil {
		return o, errC4Docker
	}
	return executeC4(ctx, plan, o, newC4DockerDriver(plan, process, trust, ""), persist)
}
func cleanupC4Docker(ctx context.Context, p domain.C4Profile, o domain.C4Operation, c C4DockerConfig, candidate, id string, persist C4Persist, process c4DockerProcess, trust c4DockerTrustCheck) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if !c4DockerApproved(ctx, p, o, c, persist) || process == nil || trust == nil || o.Dispatch == nil || o.Result == nil || o.CleanupState() == "confirmed" || len(o.Cleanup) >= domain.C4OperationLimit || !c4ContainerID.MatchString(id) || (o.ContainerID != "" && o.ContainerID != id) || (len(o.Cleanup) > 0 && o.Cleanup[0].ContainerID != id) {
		return o, errC4Docker
	}
	plan, err := BuildC4Plan(p, o, c.Trust, candidate)
	if err != nil {
		return o, errC4Docker
	}
	return cleanupC4(plan, o, id, newC4DockerDriver(plan, process, trust, id), persist)
}
