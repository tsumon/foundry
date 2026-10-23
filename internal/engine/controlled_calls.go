package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/evidence"
	"foundry/internal/jsontext"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

type controlledInput struct {
	SnapshotSchema  int                        `json:"snapshot_schema,omitempty"`
	Selection       *domain.RoleModelSelection `json:"selection,omitempty"`
	Profile         domain.Profile             `json:"profile"`
	Role            domain.Role                `json:"role"`
	System          string                     `json:"system"`
	Prompt          string                     `json:"prompt"`
	MaxOutputTokens int                        `json:"max_output_tokens"`
	Question        string                     `json:"question,omitempty"`
}

func decodeControlled(text string, out any) error {
	if !jsontext.Lossless([]byte(text)) {
		return errors.New("model output is not lossless JSON")
	}
	if err := decodeStrict(text, out); err != nil {
		return err
	}
	return exactJSONFields(json.RawMessage(text), reflect.TypeOf(out))
}

// encoding/json otherwise accepts case-insensitive aliases for struct fields.
// New protocols require exact keys; legacy decodeStrict remains unchanged.
func exactJSONFields(raw json.RawMessage, typ reflect.Type) error {
	if typ == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return errors.New("null is not a role schema value")
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		allowed := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			if name != "-" {
				allowed[name] = field.Type
			}
		}
		for key, value := range fields {
			field, ok := allowed[key]
			if !ok {
				return errors.New("output contains an unknown or case-aliased field")
			}
			if err := exactJSONFields(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactJSONFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) evidenceStore() *evidence.Store {
	return evidence.New(filepath.Join(e.options.DataDir, "evidence"))
}

// Callers hold Engine.mu from authority checks through the durable claim.
func (e *Engine) controlledAuthority(op *operation, role domain.Role) (domain.Mission, string, string, error) {
	m, err := e.s.GetMission(op.id)
	if err != nil {
		return m, "", "", err
	}
	if op.ctx.Err() != nil || m.Epoch != op.epoch {
		return m, "", "", context.Canceled
	}
	ordinaryInference := domain.IsInferenceSnapshot(m) && m.ToolMode == ""
	if err = e.ValidateRepositoryImport(m); err != nil {
		return m, "", "", err
	}
	if !ordinaryInference && ((m.ToolMode != domain.ToolModeControlledReadV1 && !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode)) || m.TaskBindings == nil) {
		return m, "", "", errors.New("controlled binding missing")
	}
	if err = e.checkSnapshot(m); err != nil {
		return m, "", "", err
	}
	var binding string
	if ordinaryInference {
		if _, err = e.selectedCandidate(m); err != nil {
			return m, "", "", err
		}
		binding = m.ModelSnapshot.Digest
	} else {
		binding, err = controlledBindingDigest(m)
	}
	if err != nil {
		return m, "", "", err
	}
	execute := role == domain.Worker || role == domain.Debugger || role == domain.QA || role == domain.Auditor
	auth, err := authority(m, filepath.Join(e.dir(m.ID), "base"), execute)
	if err != nil {
		return m, "", "", err
	}
	kind := "context"
	if execute {
		kind = "execute"
	}
	if !hasSeal(m, kind, auth) {
		return m, "", "", errors.New("current " + kind + " authority required")
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		if err = e.localMCPCurrent(m); err != nil {
			return m, "", "", err
		}
	}
	return m, binding, auth, nil
}

func (e *Engine) invocationIntent(m domain.Mission, role domain.Role, kind, name, binding, auth string) (domain.Invocation, error) {
	id, err := opaque()
	if err != nil {
		return domain.Invocation{}, err
	}
	v := domain.Invocation{ID: id, MissionID: m.ID, Epoch: m.Epoch, SessionID: id, Role: role, Kind: kind, Name: name, BindingDigest: binding, AuthorityDigest: auth}
	if role == domain.Researcher && m.ToolMode != "" {
		grants, err := e.s.ListReadGrants(m.ID)
		if err != nil {
			return v, err
		}
		for _, g := range grants {
			if g.Epoch == m.Epoch {
				exact, err := e.s.GetReadGrant(g.ID)
				if err != nil {
					return v, err
				}
				if exact.RequiredState != m.State || exact.Role != role || exact.BindingDigest != binding || exact.AuthorityDigest != auth {
					return v, errors.New("read grant no longer applies")
				}
				v.SessionID, v.GrantID = exact.SessionID, exact.ID
				return v, nil
			}
		}
		return v, errors.New("planning read grant required")
	}
	return v, nil
}

func (e *Engine) seal(v domain.Invocation, kind string, body []byte, contentType string, truncated bool) (domain.EvidenceEnvelope, error) {
	return e.evidenceStore().Put(domain.EvidenceEnvelope{Kind: kind, InvocationID: v.ID, MissionID: v.MissionID, Epoch: v.Epoch, Producer: string(v.Role), ContentType: contentType, BindingDigest: v.BindingDigest, Truncated: truncated}, body)
}

func (e *Engine) reserveClaim(m domain.Mission, v domain.Invocation, body []byte) (domain.Invocation, error) {
	input, err := e.seal(v, "model_input", body, "application/json", false)
	if err != nil {
		return v, err
	}
	v.Input = input
	_, v, err = e.s.BeginInvocation(v, m.Revision)
	if err != nil {
		return v, err
	}
	return e.s.ClaimInvocation(v.ID, v.Epoch)
}

func (e *Engine) controlledModel(op *operation, role domain.Role, extra any) (domain.Invocation, string, error) {
	e.mu.Lock()
	m, binding, auth, err := e.controlledAuthority(op, role)
	var v domain.Invocation
	var input controlledInput
	if err == nil {
		err = e.preflightModelRoles(op.ctx, m, role)
	}
	if err == nil {
		v, err = e.invocationIntent(m, role, "model", "model.complete", binding, auth)
	}
	if err == nil {
		input, err = e.controlledModelInput(m, role, extra)
	}
	if err == nil {
		var body []byte
		body, err = json.Marshal(input)
		if err == nil {
			v, err = e.reserveClaim(m, v, body)
		}
	}
	e.mu.Unlock()
	if err != nil {
		return v, "", err
	}
	return e.completeControlledModel(op, v, input)
}

// Shared transport for ordinary reservations and atomic research checkpoints.
// The caller has already claimed exactly once; this function never reserves.
func (e *Engine) completeControlledModel(op *operation, v domain.Invocation, input controlledInput) (domain.Invocation, string, error) {
	role, auth := v.Role, v.AuthorityDigest
	var err error
	ctx, cancel := context.WithTimeout(op.ctx, 90*time.Second)
	defer cancel()
	var result model.Result
	if e.gateway == nil {
		err = errors.New("gateway unavailable")
	} else {
		result, err = e.gateway.Complete(ctx, model.Request{InvocationID: v.ID, SnapshotSchema: input.SnapshotSchema, Selection: input.Selection, Profile: input.Profile, Role: role, System: input.System, Prompt: input.Prompt, MaxOutputTokens: input.MaxOutputTokens})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	current, _, currentAuth, checkErr := e.controlledAuthority(op, role)
	if checkErr != nil || currentAuth != auth || ctx.Err() != nil {
		code := "dispatch_interrupted"
		if current.Epoch != v.Epoch {
			code = "stale_dispatch"
		}
		_, finishErr := e.s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, code, unknownModelUsage(input, result, err))
		if finishErr != nil {
			return v, "", fmt.Errorf("invocation %s finalization failed: %w", v.ID, finishErr)
		}
		return v, "", fmt.Errorf("invocation %s interrupted; outcome unknown", v.ID)
	}
	if err != nil {
		status, code := "outcome_unknown", "model_transport_unknown"
		var receipt *domain.ModelCallReceipt
		if input.SnapshotSchema == 2 {
			status, code, receipt = modelFailureReceipt(result, err)
		}
		_, finishErr := e.s.FinishInvocation(v.ID, v.Epoch, status, nil, code, domain.CallUsage{ModelReceipt: receipt})
		if finishErr != nil {
			return v, "", finishErr
		}
		return v, "", fmt.Errorf("model invocation %s: %s; reservation consumed", v.ID, code)
	}
	// Seal raw text before interpreting even malformed/denied output.
	envelope, err := e.seal(v, "model_output", []byte(result.Text), "text/plain; charset=utf-8", false)
	if err != nil {
		_, finishErr := e.s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "result_seal_failed", unknownModelUsage(input, result, nil))
		if finishErr != nil {
			return v, "", finishErr
		}
		return v, "", fmt.Errorf("invocation %s result sealing failed", v.ID)
	}
	usage := domain.CallUsage{}
	if result.UsageKnown && result.InputTokens >= 0 && result.OutputTokens >= 0 {
		usage = domain.CallUsage{UsageKnown: true, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens}
	}
	if input.SnapshotSchema == 2 {
		receipt := domain.SanitizeModelCallReceipt(domain.ModelCallReceipt{RequestID: result.RequestID, ResolvedModel: result.ResolvedModel, CredentialGeneration: result.CredentialGeneration, Certainty: "completed"})
		usage.ModelReceipt = &receipt
	}
	v, err = e.s.FinishInvocation(v.ID, v.Epoch, "succeeded", &envelope, "", usage)
	if err != nil {
		return v, "", err
	}
	raw, err := e.evidenceStore().Read(envelope)
	return v, string(raw), err
}

func (e *Engine) controlledModelInput(m domain.Mission, role domain.Role, extra any) (controlledInput, error) {
	var bound []domain.SkillRevision
	var err error
	if m.ToolMode != "" {
		bound, err = e.boundSkills(m.TaskBindings, role)
		if err != nil {
			return controlledInput{}, err
		}
	}
	records, err := e.verifiedInvocations(m.ID)
	if err != nil {
		return controlledInput{}, err
	}
	files := map[string]string{}
	if role != domain.Researcher || m.ToolMode == "" {
		files, err = workspace.Context(filepath.Join(e.dir(m.ID), "base"), m.ContextManifest)
		if err != nil {
			return controlledInput{}, err
		}
	}
	payload := map[string]any{"goal": m.Goal, "spec": m.Spec, "context": files, "manifest": m.ContextManifest, "skills": bound, "research": m.Research, "plan": m.Plan, "changes": m.Changes, "evidence": m.Evidence, "extra": extra}
	if isModelC4(m) {
		path, err := e.selectedCandidate(m)
		if err != nil {
			return controlledInput{}, err
		}
		manifest := append([]domain.FileMeta(nil), m.ContextManifest...)
		for _, change := range m.Changes {
			hash := sha256.Sum256([]byte(change.After))
			meta := domain.FileMeta{Path: change.Path, Bytes: int64(len(change.After)), SHA256: hex.EncodeToString(hash[:])}
			found := false
			for i := range manifest {
				if manifest[i].Path == change.Path {
					manifest[i] = meta
					found = true
					break
				}
			}
			if !found {
				manifest = append(manifest, meta)
			}
		}
		current, err := workspace.Context(path, manifest)
		if err != nil {
			return controlledInput{}, err
		}
		payload["baseline_context"] = files
		payload["context"] = current
		payload["current_context_manifest"] = manifest
		payload["candidate_generation"] = m.CandidateGeneration
		payload["candidate_digest"] = m.CandidateDigest
	}
	if m.ReplanContext != nil {
		payload["replan_context"] = m.ReplanContext
	}
	input := controlledInput{Profile: m.Profiles[role], Role: role, System: missionRolePrompt(m, role), MaxOutputTokens: 8192}
	if domain.IsInferenceSnapshot(m) {
		selection := m.ModelSnapshot.Roles[role]
		input.SnapshotSchema = 2
		input.Selection = &selection
	}
	if role == domain.Researcher && m.ToolMode != "" {
		input.System = controlledResearchPrompt
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
			input.System = researchV2Prompt
			payload["mcp_tools"] = m.MCPBindings.Tools
		}
	}
	if role == domain.Main {
		history, err := e.mainHistory(records)
		if err != nil {
			return input, err
		}
		payload["history"] = history
		if question, ok := extra.(map[string]string); ok {
			input.Question = question["question"]
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 2<<20 {
		return input, errors.New("controlled role prompt exceeds bounds")
	}
	input.Prompt = string(raw)
	return input, nil
}

// All envelopes are verified before their projected content can enter another prompt.
func (e *Engine) verifiedInvocations(id string) ([]domain.Invocation, error) {
	all := []domain.Invocation{}
	before := int64(0)
	for {
		rows, err := e.s.ListInvocations(id, before, 100)
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			if _, err = e.evidenceStore().Read(v.Input); err != nil {
				return nil, fmt.Errorf("invocation %s input integrity: %w", v.ID, err)
			}
			if v.Result != nil {
				if _, err = e.evidenceStore().Read(*v.Result); err != nil {
					return nil, fmt.Errorf("invocation %s result integrity: %w", v.ID, err)
				}
			}
			all = append(all, v)
		}
		if len(rows) < 100 {
			return all, nil
		}
		before = rows[len(rows)-1].Ordinal
	}
}

func (e *Engine) mainHistory(records []domain.Invocation) ([]domain.Message, error) {
	newest := []domain.Message{}
	size := 2 // JSON array brackets count toward the history byte bound.
	for _, v := range records {
		if v.Kind != "model" || v.Role != domain.Main || v.Status != "succeeded" || v.Result == nil {
			continue
		}
		raw, err := e.evidenceStore().Read(v.Input)
		if err != nil {
			return nil, err
		}
		var input controlledInput
		if err = decodeControlled(string(raw), &input); err != nil {
			return nil, err
		}
		raw, err = e.evidenceStore().Read(*v.Result)
		if err != nil {
			return nil, err
		}
		var output struct {
			Answer string `json:"answer"`
		}
		if err = decodeControlled(string(raw), &output); err != nil {
			continue
		}
		for _, message := range []domain.Message{{Role: "main", Content: output.Answer}, {Role: "user", Content: input.Question}} {
			encoded, err := json.Marshal(message)
			if err != nil {
				return nil, err
			}
			addition := len(encoded)
			if len(newest) > 0 {
				addition++
			}
			if len(newest) == 8 || size+addition > 32<<10 {
				goto complete
			}
			newest = append(newest, message)
			size += addition
		}
	}
complete:
	history := make([]domain.Message, len(newest))
	for i := range newest {
		history[len(newest)-1-i] = newest[i]
	}
	return history, nil
}

func (e *Engine) InvocationDetail(id, invocationID string) (domain.InvocationDetail, error) {
	if _, err := e.s.GetMission(id); err != nil {
		return domain.InvocationDetail{}, err
	}
	v, err := e.s.GetInvocation(invocationID)
	if err != nil {
		return domain.InvocationDetail{}, err
	}
	if v.MissionID != id {
		return domain.InvocationDetail{}, store.ErrNotFound
	}
	detail := domain.InvocationDetail{Invocation: v}
	raw, err := e.evidenceStore().Read(v.Input)
	if err != nil {
		return detail, err
	}
	detail.InputPreview, detail.InputTruncated = preview(raw)
	if v.Result != nil {
		raw, err = e.evidenceStore().Read(*v.Result)
		if err != nil {
			return detail, err
		}
		detail.ResultPreview, detail.ResultTruncated = preview(raw)
	}
	return detail, nil
}

func preview(raw []byte) (string, bool) {
	const limit = 64 << 10
	if len(raw) <= limit {
		return string(raw), false
	}
	raw = raw[:limit]
	for !utf8.Valid(raw) {
		raw = raw[:len(raw)-1]
	}
	return string(raw), true
}
