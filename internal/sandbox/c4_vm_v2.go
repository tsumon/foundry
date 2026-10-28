package sandbox

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"time"
)

func ExecuteC4VMV2(ctx context.Context, p domain.C4Profile, o domain.C4Operation, r *vmruntime.Runtime, path string, inputs domain.C4InputContractV2, store string, persist C4Persist) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if persist == nil || ctx.Err() != nil || o.State != "approved" || o.Dispatch != nil || p.SchemaVersion != 2 || inputs.ValidateProfileBinding(p) != nil || validateVMOperation(p, o, r, path) != nil {
		return o, ErrC4Execution
	}
	bounded, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	owner := vmRequest(o, path)
	q := vmruntime.RequestV2{Owner: owner, Profile: p, Inputs: inputs, DependencyStoreRoot: store}
	d := &c4VMLifecycle{runtime: r, request: owner, requestV2: &q}
	return executeC4Lifecycle(bounded, o, d, persist)
}
func CleanupC4VMV2(ctx context.Context, p domain.C4Profile, o domain.C4Operation, r *vmruntime.Runtime, path, id string, inputs domain.C4InputContractV2, store string, persist C4Persist) (domain.C4Operation, error) {
	o = cloneC4Operation(o)
	if persist == nil || ctx.Err() != nil || o.Dispatch == nil || o.Result == nil || o.CleanupState() == "confirmed" || len(o.Cleanup) >= domain.C4OperationLimit || !c4ContainerID.MatchString(id) || o.ContainerID != id || p.SchemaVersion != 2 || inputs.ValidateProfileBinding(p) != nil || validateVMOperation(p, o, r, path) != nil {
		return o, ErrC4Execution
	}
	owner := vmRequest(o, path)
	q := vmruntime.RequestV2{Owner: owner, Profile: p, Inputs: inputs, DependencyStoreRoot: store}
	s, ok := r.Lookup(id)
	if !ok || !s.MatchesV2(q) {
		return o, ErrC4Execution
	}
	return cleanupC4Lifecycle(o, id, &c4VMLifecycle{runtime: r, request: owner, requestV2: &q, session: s}, persist)
}
