package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"foundry/internal/workspace"
	"io"
	"path/filepath"
	"time"
)

func c4VMAllowed(p domain.C4Profile, r *vmruntime.Runtime) bool {
	return r != nil && r.Trusted() && p.Mode == "embedded_vm" && p.Validate() == nil && p.EmbeddedVM != nil && *p.EmbeddedVM == r.Identity()
}

func CheckC4VMReadiness(ctx context.Context, p domain.C4Profile, r *vmruntime.Runtime) (C4ReadinessResult, error) {
	if !c4VMAllowed(p, r) || ctx.Err() != nil {
		return C4ReadinessResult{FailureCode: "readiness_gate_failed"}, ErrC4Execution
	}
	probe := r.Probe
	prefix := "foundry:c4:vm-readiness:v1\n"
	if p.SchemaVersion == 2 {
		target, err := r.ProbeTargetV2(p.ID)
		if err != nil || target.RuntimeInventoryDigest != p.V2.RuntimeInventoryDigest {
			return C4ReadinessResult{FailureCode: "readiness_gate_failed"}, ErrC4Execution
		}
		probe = func(ctx context.Context) error { return r.ProbeV2(ctx, target) }
		prefix = "foundry:c4:vm-readiness:v2\n"
	}
	if err := probe(ctx); err != nil {
		return C4ReadinessResult{FailureCode: "failed"}, err
	}
	if ctx.Err() != nil {
		return C4ReadinessResult{FailureCode: "failed"}, ctx.Err()
	}
	sum := sha256.Sum256([]byte(prefix + p.ProfileDigest()))
	return C4ReadinessResult{Fingerprint: hex.EncodeToString(sum[:])}, nil
}

func vmRequest(o domain.C4Operation, path string) vmruntime.Request {
	digest := o.BaseDigest
	if o.Kind == "candidate" {
		digest = o.CandidateDigest
	}
	return vmruntime.Request{OperationID: o.ID, MissionID: o.MissionID, Owner: o.Owner, Name: o.ContainerName, CandidatePath: path, CandidateDigest: digest}
}

func validateVMOperation(p domain.C4Profile, o domain.C4Operation, r *vmruntime.Runtime, path string) error {
	if !c4VMAllowed(p, r) || domain.ValidateC4Operation(p, o) != nil {
		return ErrC4Execution
	}
	if err := validateC4Candidate(filepath.Dir(path), path); err != nil {
		return err
	}
	digest, err := workspace.Digest(path)
	if err != nil || digest != vmRequest(o, path).CandidateDigest {
		return ErrC4Execution
	}
	return nil
}

func ExecuteC4VM(ctx context.Context, p domain.C4Profile, o domain.C4Operation, r *vmruntime.Runtime, path string, persist C4Persist) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if persist == nil || ctx.Err() != nil || o.State != "approved" || o.Dispatch != nil || validateVMOperation(p, o, r, path) != nil {
		return o, ErrC4Execution
	}
	bounded, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	d := &c4VMLifecycle{runtime: r, request: vmRequest(o, path)}
	return executeC4Lifecycle(bounded, o, d, persist)
}

func CleanupC4VM(ctx context.Context, p domain.C4Profile, o domain.C4Operation, r *vmruntime.Runtime, path, id string, persist C4Persist) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if persist == nil || ctx.Err() != nil || o.Dispatch == nil || o.Result == nil || o.CleanupState() == "confirmed" || len(o.Cleanup) >= domain.C4OperationLimit || !c4ContainerID.MatchString(id) || o.ContainerID != id || validateVMOperation(p, o, r, path) != nil {
		return o, ErrC4Execution
	}
	s, ok := r.Lookup(id)
	if !ok || !s.Matches(vmRequest(o, path)) {
		return o, ErrC4Execution
	}
	return cleanupC4Lifecycle(o, id, &c4VMLifecycle{runtime: r, request: vmRequest(o, path), session: s}, persist)
}

// The session seam is private; no caller can inject synthetic production trust.
type c4VMSession interface {
	ID() string
	Start(context.Context) error
	Wait(context.Context) error
	Result() (c4verify.Result, bool)
	Running() bool
	Close(context.Context) error
	Matches(vmruntime.Request) bool
}
type c4VMLifecycle struct {
	runtime   *vmruntime.Runtime
	request   vmruntime.Request
	requestV2 *vmruntime.RequestV2
	session   c4VMSession
}

func (d *c4VMLifecycle) executionTimeout() time.Duration {
	if d.requestV2 != nil && d.requestV2.Profile.SchemaVersion == 2 {
		if policy, err := domain.FixedC4VMPolicyV2(d.requestV2.Profile.ID); err == nil {
			// The guest owns the unchanged test deadline. The host must retain
			// the existing VM envelope for boot, transfer, cleanup and response.
			return time.Duration(policy.LifetimeSeconds) * time.Second
		}
	}
	return time.Duration(domain.FixedC4Policy().TimeoutSeconds) * time.Second
}

func (d *c4VMLifecycle) create(ctx context.Context) (string, error) {
	if d.runtime == nil || d.session != nil {
		return "", ErrC4Execution
	}
	var s *vmruntime.Session
	var err error
	if d.requestV2 != nil {
		s, err = d.runtime.CreateV2(ctx, *d.requestV2)
	} else {
		s, err = d.runtime.Create(ctx, d.request)
	}
	if err != nil {
		return "", err
	}
	d.session = s
	return s.ID(), nil
}
func (d *c4VMLifecycle) owned(id string) bool {
	if d.session == nil || d.session.ID() != id {
		return false
	}
	if d.requestV2 != nil {
		v, ok := d.session.(interface {
			MatchesV2(vmruntime.RequestV2) bool
		})
		return ok && v.MatchesV2(*d.requestV2)
	}
	return d.session.Matches(d.request)
}
func (d *c4VMLifecycle) start(ctx context.Context, id string) error {
	if !d.owned(id) {
		return ErrC4Execution
	}
	return d.session.Start(ctx)
}
func (d *c4VMLifecycle) wait(ctx context.Context, id string) error {
	if !d.owned(id) {
		return ErrC4Execution
	}
	return d.session.Wait(ctx)
}
func (d *c4VMLifecycle) inspect(ctx context.Context, id string) (C4Inspection, error) {
	if ctx.Err() != nil || !d.owned(id) {
		return C4Inspection{}, ErrC4Execution
	}
	q := d.request
	v := C4Inspection{ID: id, Name: q.Name, Labels: map[string]string{"foundry.owner": q.Owner, "foundry.operation": q.OperationID, "foundry.mission": q.MissionID, "foundry.name": q.Name}, Running: d.session.Running(), ExitCode: -1}
	if r, ok := d.session.Result(); ok && (d.requestV2 != nil || r.Tests > 0 && !r.Truncated) {
		if d.requestV2 != nil && (r.Tests == 0 || r.Truncated) && (r.Class == "passed" || r.Class == "test_failed") {
			r.Class = "infrastructure_failed"
		}
		v.vmV2 = d.requestV2 != nil
		v.ExitCode = r.ExitCode
		v.Verification = &r
	}
	return v, nil
}
func (d *c4VMLifecycle) logs(ctx context.Context, id string, w io.Writer) (bool, error) {
	if ctx.Err() != nil || !d.owned(id) {
		return false, ErrC4Execution
	}
	r, ok := d.session.Result()
	if !ok {
		return false, ErrC4Execution
	}
	_, err := io.WriteString(w, r.Output)
	return r.Truncated, err
}
func (d *c4VMLifecycle) remove(ctx context.Context, id string) error {
	if !d.owned(id) {
		return ErrC4Execution
	}
	return d.session.Close(ctx)
}
