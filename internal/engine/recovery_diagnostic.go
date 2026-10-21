package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"reflect"
)

// DiagnoseRecovery observes durable records and bounded managed material only.
// No recovery, dispatch, dependency resolution or live source read is reachable.
// The final revision/epoch fence detects concurrent mission transitions, not an
// atomic filesystem snapshot. Every subsequent mutation must revalidate.
func (e *Engine) DiagnoseRecovery(ctx context.Context, id string) (domain.RecoveryDiagnostic, error) {
	m, err := e.s.RecoveryMission(id)
	if err != nil {
		return domain.RecoveryDiagnostic{}, err
	}
	d := domain.RecoveryDiagnostic{SchemaVersion: 1, MissionID: id, Revision: m.Revision, Epoch: m.Epoch, CheckedAt: now(), Checks: []domain.RecoveryCheck{}, NextActions: []domain.RecoveryAction{domain.RecoveryInspectRecords}}
	local, localErr := e.s.LatestLocalOperation(id)
	invocations, invocationErr := e.s.RecoveryInvocations(id)
	add := func(c domain.RecoveryCheck) { d.Checks = append(d.Checks, c) }
	if isC4(m) {
		add(c4Diagnostic(m))
	}
	validMode := supportedMode(m.WorkspaceMode) == nil && m.ToolMode == "" && domain.ValidateCodingVersion(m.CodingVersion, m.WorkspaceMode, m.ToolMode) == nil
	if !validMode || id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		for _, check := range []string{"candidate_record", "candidate", "base_authority", "import_provenance", "coding_trace", "invocations", "verification", "reviews", "export", "local_operation"} {
			add(recoveryCheck(check, domain.RecoveryUnsupported, "unsupported_format", "This format is not supported by ordinary recovery diagnosis."))
		}
	} else if !e.diagnosticManagedRoot(m) {
		for _, check := range []string{"candidate_record", "candidate", "base_authority", "import_provenance", "coding_trace", "invocations", "verification", "reviews", "export", "local_operation"} {
			add(recoveryCheck(check, domain.RecoveryBlocked, "managed_root_unavailable", "The managed mission root is missing or unsafe; no artifact or source read was attempted."))
		}
		d.NextActions = append(d.NextActions, domain.RecoveryNewTask)
	} else {
		candidate := recoveryCheck("candidate_record", domain.RecoveryVerified, "selection_recorded", "A candidate selection is recorded; this is not execution approval.")
		if m.CandidateDigest == "" || (domain.UsesGenerations(m.WorkspaceMode) && (m.CandidateGeneration == nil || m.CandidateGeneration.Digest != m.CandidateDigest)) {
			candidate = recoveryCheck("candidate_record", domain.RecoveryBlocked, "selection_missing_or_invalid", "The candidate selection is missing or inconsistent.")
		}
		if m.CandidateGeneration != nil {
			candidate.Records = recoveryRefs("generation", m.CandidateGeneration.ID)
		}
		if candidate.Status == domain.RecoveryVerified && domain.UsesGenerations(m.WorkspaceMode) && !e.diagnosticSelection(m) {
			candidate.Status, candidate.Code, candidate.Message = domain.RecoveryBlocked, "selection_operation_invalid", "Selected generation does not match its committed operation or initial import record."
		}
		add(candidate)
		integrity := recoveryCheck("candidate", domain.RecoveryVerified, "candidate_intact", "Selected candidate files match their recorded digest.")
		if _, err := e.selectedCandidate(m); err != nil {
			integrity = recoveryCheck("candidate", domain.RecoveryBlocked, "candidate_unavailable_or_changed", "Selected candidate files are missing, unreadable or inconsistent; no repair was attempted.")
		}
		integrity.Records = candidate.Records
		add(integrity)
		base := recoveryCheck("base_authority", domain.RecoveryVerified, "frozen_authority_intact", "Frozen base and recorded authority match; live dependencies were not checked.")
		if e.checkFrozenAuthority(m) != nil || e.checkRepositoryImportBase(m) != nil || e.checkReplanMaterial(m) != nil || domain.ValidateMissionModelSnapshot(m) != nil {
			base = recoveryCheck("base_authority", domain.RecoveryBlocked, "frozen_authority_invalid", "Frozen base or authority is missing, unreadable or inconsistent.")
		}
		// Validate present current execution seals without requiring one before
		// execution. Historical approvals for an earlier plan are not current.
		executionRecorded := false
		for i := len(m.Approvals) - 1; i >= 0; i-- {
			a := m.Approvals[i]
			if a.Kind != "execute" {
				continue
			}
			if m.Plan != nil && a.PlanRevision == m.Plan.Revision {
				executionRecorded = true
				seal, err := authority(m, filepath.Join(e.dir(m.ID), "base"), true)
				if err != nil || seal != a.Digest {
					base = recoveryCheck("base_authority", domain.RecoveryBlocked, "execution_authority_invalid", "Current execution authority does not match frozen inputs.")
				}
			}
			break
		}
		executionRequired := m.State == "running" || m.State == "verifying" || m.State == "awaiting_delivery" || m.State == "delivered" || len(m.Evidence) > 0 || len(m.Reviews) > 0
		for _, attempt := range m.CodingAttempts {
			if m.Plan != nil && attempt.PlanRevision == m.Plan.Revision {
				executionRequired = true
			}
		}
		if executionRequired && !executionRecorded {
			base = recoveryCheck("base_authority", domain.RecoveryBlocked, "execution_authority_missing", "Execution material has no current execution approval.")
		}
		add(base)
		add(e.diagnoseImport(m))
		add(diagnoseCodingTrace(m, invocations, invocationErr))
		add(diagnoseInvocationRecords(invocations, invocationErr))
		verification, reviews := e.diagnoseReviewBindings(m)
		add(verification)
		add(reviews)
		export := recoveryCheck("export", domain.RecoveryNotApplicable, "no_delivery_record", "No committed delivery is recorded.")
		if m.Delivery != nil {
			export = recoveryCheck("export", domain.RecoveryUnsupported, "legacy_export", "Historical exports do not carry the versioned export-operation contract.")
			if domain.UsesGenerations(m.WorkspaceMode) {
				export = recoveryCheck("export", domain.RecoveryVerified, "export_intact", "Committed export bindings and artifact bytes match.")
				if e.verifyDeliveryWith(m, e.s.RecoveryLocalOperation) != nil {
					export = recoveryCheck("export", domain.RecoveryBlocked, "export_unavailable_or_changed", "Delivery binding or artifact bytes are missing, unreadable or inconsistent.")
				}
			}
			export.Records = recoveryRefs("local_operation", m.Delivery.OperationID)
		}
		add(export)
		add(diagnoseLocalOperation(local, localErr))
		blocked, materialBlocked := false, false
		for _, c := range d.Checks {
			bad := c.Status == domain.RecoveryBlocked || c.Status == domain.RecoveryUnsupported
			blocked = blocked || bad
			if c.ID != "verification" && c.ID != "reviews" {
				materialBlocked = materialBlocked || bad
			}
		}
		if materialBlocked {
			d.NextActions = append(d.NextActions, domain.RecoveryNewTask)
		} else {
			// Only a brief active-state observation owns the engine lock. These
			// are decision links, never availability or authorization promises.
			e.mu.Lock()
			idle := e.active == nil && !e.closed
			e.mu.Unlock()
			if idle && (m.State == "paused" || m.State == "blocked") && m.Budget.UsedCalls < m.Budget.MaxCalls {
				if _, _, err := e.freezeReplanWith(m, e.s.RecoveryLocalOperation); err == nil {
					d.NextActions = append(d.NextActions, domain.RecoveryReplan)
				}
			}
			if !blocked && idle && m.State == "awaiting_delivery" && domain.CanDeliver(m, m.CandidateDigest) == nil && e.checkCodingReviewMaterial(m) == nil {
				d.NextActions = append(d.NextActions, domain.RecoveryDeliveryDecision)
			}
		}
	}
	// Do not discard any blocked observations when the final read fails.
	endLocal, endLocalErr := e.s.LatestLocalOperation(id)
	endInvocations, endInvocationErr := e.s.RecoveryInvocations(id)
	final, err := e.s.RecoveryMission(id)
	if err != nil || ctx.Err() != nil || final.Revision != m.Revision || final.Epoch != m.Epoch ||
		(localErr == nil) != (endLocalErr == nil) || !reflect.DeepEqual(local, endLocal) ||
		(invocationErr == nil) != (endInvocationErr == nil) || !reflect.DeepEqual(invocations, endInvocations) {
		d.Stale = true
		d.NextActions = []domain.RecoveryAction{}
	}
	return d, nil
}

func (e *Engine) diagnosticManagedRoot(m domain.Mission) bool {
	for _, path := range []string{e.options.DataDir, filepath.Join(e.options.DataDir, "missions"), e.dir(m.ID)} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

func (e *Engine) diagnosticSelection(m domain.Mission) bool {
	op, err := e.s.RecoveryLocalOperation(m.CandidateGeneration.ID)
	if errors.Is(err, store.ErrNotFound) {
		if m.ImportVersion == 1 {
			initial, err := e.s.GetRepositoryImportByMission(m.ID)
			return err == nil && initial.Status == "committed" && reflect.DeepEqual(initial.Generation, m.CandidateGeneration)
		}
		// Historical creation predates the import ledger. Only its initial
		// untouched draft can lack a local selection operation.
		return m.ImportVersion == 0 && m.State == "draft" && m.Epoch == 0 && len(m.Changes) == 0
	}
	return err == nil && op.MissionID == m.ID && op.Status == "committed" && (op.Kind == "apply" || op.Kind == "reset") && op.Result != nil && reflect.DeepEqual(op.Result.Generation, m.CandidateGeneration) && reflect.DeepEqual(op.Result.Changes, m.Changes) && op.ExpectedRevision < m.Revision && op.Epoch <= m.Epoch
}

func recoveryCheck(id string, status domain.RecoveryCheckStatus, code, message string) domain.RecoveryCheck {
	return domain.RecoveryCheck{ID: id, Status: status, Code: code, Message: message, Records: []domain.RecoveryRecordRef{}}
}

// References contain short opaque local IDs, never paths, raw error text or
// model/provider content. Invalid persisted references are omitted, not echoed.
func recoveryRefs(kind, id string) []domain.RecoveryRecordRef {
	refs := []domain.RecoveryRecordRef{}
	if len(id) == 0 || len(id) > 128 {
		return refs
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return refs
		}
	}
	return append(refs, domain.RecoveryRecordRef{Kind: kind, ID: id})
}

func (e *Engine) diagnoseImport(m domain.Mission) domain.RecoveryCheck {
	c := recoveryCheck("import_provenance", domain.RecoveryBlocked, "import_binding_invalid", "Committed import provenance is missing, unreadable or inconsistent.")
	op, err := e.s.GetRepositoryImportByMission(m.ID)
	if err == nil {
		c.Records = recoveryRefs("repository_import_operation", op.ID)
	}
	if m.RepositoryImportInvalid {
		return c
	}
	if m.ImportVersion != 0 && m.ImportVersion != 1 {
		return recoveryCheck(c.ID, domain.RecoveryUnsupported, "unsupported_import_version", "The import version is not supported.")
	}
	if m.ImportVersion == 0 && m.RepositoryImport == nil && errors.Is(err, store.ErrNotFound) {
		return recoveryCheck(c.ID, domain.RecoveryNotApplicable, "historical_import_absent", "Historical task has no frozen import provenance; none was inferred.")
	}
	if err != nil || e.ValidateRepositoryImport(m) != nil {
		return c
	}
	// The import row binds the initial generation, not the current selection.
	c.Status, c.Code, c.Message = domain.RecoveryVerified, "import_binding_intact", "Committed inspection/import binding and frozen base match; live source was not read."
	return c
}

func diagnoseCodingTrace(m domain.Mission, invocations []domain.Invocation, invocationErr error) domain.RecoveryCheck {
	c := recoveryCheck("coding_trace", domain.RecoveryVerified, "trace_recorded", "Recorded coding attempts are structurally valid; outcomes are not replayed.")
	if domain.ValidateCodingTrace(m) != nil {
		return recoveryCheck(c.ID, domain.RecoveryBlocked, "trace_invalid", "Coding trace is malformed or inconsistent.")
	}
	if m.CodingVersion == 0 {
		return recoveryCheck(c.ID, domain.RecoveryNotApplicable, "historical_trace_absent", "Historical task has no versioned coding trace.")
	}
	if len(m.CodingAttempts) == 0 {
		return recoveryCheck(c.ID, domain.RecoveryNotApplicable, "no_coding_attempt", "No coding attempt has been recorded.")
	}
	var last *domain.GenerationRef
	uncertainSelection := false
	for _, a := range m.CodingAttempts {
		// Recovery fences the epoch without rewriting the interrupted attempt.
		// The still-current plan, not epoch equality, identifies that run.
		if m.Plan == nil || a.PlanRevision != m.Plan.Revision {
			continue
		}
		if last != nil && !reflect.DeepEqual(last, a.Before) {
			return recoveryCheck(c.ID, domain.RecoveryBlocked, "trace_generation_binding_invalid", "The recorded attempt generation chain is inconsistent.")
		}
		last = a.After
		if last == nil {
			last = a.Before
		}
		uncertainSelection = a.After == nil && (a.Status == "running" || a.Status == "interrupted" || a.Status == "outcome_unknown")
		if a.InvocationID != "" {
			found := false
			for _, v := range invocations {
				if v.ID != a.InvocationID {
					continue
				}
				found = true
				if v.MissionID != m.ID || v.Epoch != a.Epoch || v.Role != a.Role {
					return recoveryCheck(c.ID, domain.RecoveryBlocked, "trace_invocation_binding_invalid", "An attempt references a foreign invocation.")
				}
			}
			if !found && invocationErr == nil && len(invocations) < 100 {
				return recoveryCheck(c.ID, domain.RecoveryBlocked, "trace_invocation_missing", "An attempt references a missing invocation record.")
			}
			if !found {
				c.Status, c.Code, c.Message = domain.RecoveryUnknown, "trace_invocation_unknown", "A referenced invocation is outside the readable bounded history."
			}
		}
		if a.Status == "running" || a.Status == "interrupted" || a.Status == "outcome_unknown" {
			c.Status, c.Code, c.Message = domain.RecoveryUnknown, "attempt_outcome_unknown", "An attempt is unfinished or interrupted; a selected candidate does not prove its outcome."
			c.Records = recoveryRefs("coding_attempt", a.ID)
		}
	}
	if last != nil && !uncertainSelection && !reflect.DeepEqual(last, m.CandidateGeneration) {
		return recoveryCheck(c.ID, domain.RecoveryBlocked, "trace_generation_binding_invalid", "The terminal attempt does not bind the selected candidate.")
	}
	return c
}

func diagnoseInvocationRecords(rows []domain.Invocation, err error) domain.RecoveryCheck {
	c := recoveryCheck("invocations", domain.RecoveryNotApplicable, "no_invocations", "No invocation records are present.")
	if err != nil {
		return recoveryCheck(c.ID, domain.RecoveryBlocked, "invocation_records_unreadable", "Invocation records cannot be validated.")
	}
	if len(rows) == 0 {
		return c
	}
	c = recoveryCheck(c.ID, domain.RecoveryVerified, "invocation_records_terminal", "Recent invocation records have recorded terminal outcomes; no dispatch occurred.")
	if len(rows) == 100 {
		c = recoveryCheck(c.ID, domain.RecoveryUnknown, "invocation_history_bounded", "Only the latest 100 invocations were inspected; earlier outcomes are not asserted.")
	}
	for _, v := range rows {
		if v.Status == "authorized_pending" || v.Status == "dispatching" || v.Status == "outcome_unknown" || v.Status == "interrupted_before_dispatch" {
			c = recoveryCheck(c.ID, domain.RecoveryUnknown, "invocation_outcome_unknown", "An invocation has an unfinished or uncertain outcome; it was not replayed.")
			c.Records = recoveryRefs("invocation", v.ID)
			break
		}
	}
	return c
}

func (e *Engine) diagnoseReviewBindings(m domain.Mission) (domain.RecoveryCheck, domain.RecoveryCheck) {
	v := recoveryCheck("verification", domain.RecoveryNotApplicable, "no_verification", "No verification evidence is recorded.")
	r := recoveryCheck("reviews", domain.RecoveryNotApplicable, "no_reviews", "No review decisions are recorded.")
	if len(m.Evidence) > 0 {
		latest := m.Evidence[len(m.Evidence)-1]
		v = recoveryCheck(v.ID, domain.RecoveryVerified, "verification_bound", "Latest passing verification binds the current candidate and plan; this does not establish Docker readiness.")
		if m.Plan == nil || latest.CandidateDigest != m.CandidateDigest || latest.AcceptanceRevision != m.Plan.Revision || latest.ID == "" || latest.ImageID == "" || latest.ImageID != m.Spec.ImageID || !latest.Passed || len(latest.Results) != len(m.Spec.Acceptance) {
			v = recoveryCheck(v.ID, domain.RecoveryBlocked, "verification_invalid_or_failed", "Latest verification is failed, incomplete or not bound to current inputs.")
		} else {
			for i, result := range latest.Results {
				if result.Command != m.Spec.Acceptance[i] || result.ExitCode != 0 || result.TimedOut {
					v = recoveryCheck(v.ID, domain.RecoveryBlocked, "verification_invalid_or_failed", "Latest verification is failed, incomplete or not bound to current inputs.")
					break
				}
			}
			if m.CodingVersion == 1 {
				if _, err := domain.NewCodingReviewContract(m, domain.QA); err != nil {
					v = recoveryCheck(v.ID, domain.RecoveryBlocked, "verification_trace_invalid", "Verification does not bind the current controller trace and approval.")
				}
			}
			if v.Status == domain.RecoveryVerified && latest.EffectiveSource() == "unknown" {
				v = recoveryCheck(v.ID, domain.RecoveryUnknown, "verification_source_unknown", "Verification provenance is absent or unsupported; execution is not inferred.")
			}
		}
		v.Records = recoveryRefs("verification", latest.ID)
	}
	if len(m.Reviews) > 0 {
		current := m
		current.State = "awaiting_delivery"
		r = recoveryCheck(r.ID, domain.RecoveryVerified, "reviews_bound", "Current passing QA and Auditor reviews bind the recorded evidence and candidate.")
		if domain.CanDeliver(current, m.CandidateDigest) != nil || (m.CodingVersion == 1 && e.checkCodingReviewMaterial(m) != nil) {
			r = recoveryCheck(r.ID, domain.RecoveryBlocked, "reviews_invalid_or_incomplete", "Current independent review bindings are incomplete, failed or inconsistent.")
		}
	}
	if m.State == "awaiting_delivery" || m.State == "delivered" {
		if len(m.Evidence) == 0 {
			v = recoveryCheck(v.ID, domain.RecoveryBlocked, "verification_missing", "Delivery state lacks required verification evidence.")
		}
		if len(m.Reviews) == 0 {
			r = recoveryCheck(r.ID, domain.RecoveryBlocked, "reviews_missing", "Delivery state lacks required independent reviews.")
		}
	}
	return v, r
}

func diagnoseLocalOperation(op domain.LocalOperation, err error) domain.RecoveryCheck {
	c := recoveryCheck("local_operation", domain.RecoveryNotApplicable, "no_local_operation", "No local operation is recorded.")
	if errors.Is(err, store.ErrNotFound) {
		return c
	}
	if err != nil {
		return recoveryCheck(c.ID, domain.RecoveryBlocked, "operation_unreadable", "The latest local operation cannot be validated.")
	}
	c.Records = recoveryRefs("local_operation", op.ID)
	switch op.Status {
	case "pending", "prepared", "interrupted":
		c.Status, c.Code, c.Message = domain.RecoveryUnknown, "operation_uncommitted", "The latest local operation is not committed; prepared artifacts are not selected or replayed."
		if op.Kind == "export" {
			c.Code, c.Message = "export_uncommitted", "An export attempt is unfinished; use the existing explicit delivery decision after material checks."
		}
	case "committed":
		c.Status, c.Code, c.Message = domain.RecoveryVerified, "operation_committed", "The latest local operation has a committed record; this alone does not establish current artifact integrity."
	default:
		c.Status, c.Code, c.Message = domain.RecoveryBlocked, "operation_status_invalid", "The latest local operation has an unsupported status."
	}
	return c
}
