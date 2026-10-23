package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

func importSpec(spec domain.Spec) domain.RepositoryImportSpec {
	return domain.RepositoryImportSpec{Scope: spec.Scope, Protected: spec.Protected, Acceptance: spec.Acceptance, Image: spec.Image}
}

// Caller holds the creation lock. The durable unique inspection key also closes
// duplicate creation across engines; unfinished rows never authorize replay.
func (e *Engine) createRepositoryImport(parent context.Context, r CreateRequest) (domain.Mission, error) {
	zero := domain.Mission{}
	if r.ImportVersion != 1 || r.ImportSelection == nil || r.CodingVersion != 1 || r.WorkspaceMode != "generations_v1" || r.ToolMode != "" || r.MCPSelection != nil || r.CapabilitySelection != nil {
		return zero, domain.ImportInvalidRequest
	}
	if strings.TrimSpace(r.Title) == "" || len(r.Title) > 200 || strings.TrimSpace(r.Goal) == "" || len(r.Goal) > 16000 {
		return zero, domain.ImportInvalidRequest
	}
	if r.Spec.MaxCalls == 0 {
		r.Spec.MaxCalls = 16
	}
	r.Spec.ImageID = ""
	if r.Spec.Protected == nil {
		r.Spec.Protected = []string{}
	}
	spec := importSpec(r.Spec)
	if domain.ValidateSpec(r.Spec) != nil || domain.ValidateRepositoryImportSpec(spec) != nil || workspace.ValidateRules(spec.Scope) != nil || len(spec.Protected) > 0 && workspace.ValidateRules(spec.Protected) != nil {
		return zero, domain.ImportInvalidRequest
	}
	raw, _ := json.Marshal(r)
	h := sha256.Sum256(append([]byte("FOUNDRY repository import intent v1\x00"), raw...))
	intent := hex.EncodeToString(h[:])
	previous, err := e.s.GetRepositoryImportByInspection(r.ImportSelection.InspectionID)
	if err == nil {
		if previous.IntentDigest != intent {
			return zero, domain.ImportIntentConflict
		}
		if previous.Status != "committed" {
			return zero, domain.ImportIncomplete
		}
		m, err := e.s.GetMission(previous.MissionID)
		if err != nil || e.s.ValidateMissionRepositoryImport(m) != nil {
			return zero, domain.ImportInvalidBinding
		}
		if err := e.c4Gate(m); err != nil {
			return zero, err
		}
		return m, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return zero, domain.ImportStorageFailed
	}
	receipt, err := e.s.GetRepositoryInspection(r.ImportSelection.InspectionID)
	if err != nil || domain.ValidateImportSelection(receipt, *r.ImportSelection) != nil {
		return zero, domain.ImportInvalidRecord
	}
	if domain.RepositoryImportSpecDigest(spec) != domain.RepositoryImportSpecDigest(receipt.Spec) {
		return zero, domain.ImportSourceChanged
	}
	resolved, frozen, modelSnapshot, modelPolicy, err := e.s.ResolveMissionModelSelection(r.Spec.ProfileID)
	if err != nil {
		return zero, err
	}
	if r.Spec.C4 != nil {
		if err := c4SelectedProfiles(r.Spec.C4, frozen, modelSnapshot); err != nil {
			return zero, err
		}
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	source, err := e.recheckImportSource(ctx, receipt, r.RepoPath)
	if err != nil {
		return zero, err
	}
	if workspace.CheckLocation(ctx, receipt.Root, e.options.DataDir) != nil {
		return zero, domain.ImportLocation
	}
	id, err := opaque()
	if err != nil {
		return zero, domain.ImportStorageFailed
	}
	opID, err := opaque()
	if err != nil {
		return zero, domain.ImportStorageFailed
	}
	generationID, err := opaque()
	if err != nil {
		return zero, domain.ImportStorageFailed
	}
	op, err := e.s.BeginRepositoryImport(ctx, domain.RepositoryImportOperation{ID: opID, InspectionID: receipt.ID, IntentDigest: intent, MissionID: id}, receipt, source)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return zero, domain.ImportIncomplete
		}
		if errors.Is(err, domain.ImportSourceChanged) {
			return zero, err
		}
		return zero, domain.ImportStorageFailed
	}
	fail := func(stage string, code domain.ImportError) (domain.Mission, error) {
		_, _ = e.s.FailRepositoryImport(op, stage, code)
		return zero, code
	}
	if err = workspace.CreateRepositoryImportDirectories(e.options.DataDir); err != nil {
		return fail("prepare", domain.ImportPrepareFailed)
	}
	stage := filepath.Join(e.options.DataDir, "repository-import-staging", op.ID)
	digest, ref, facts, err := workspace.PrepareRepositoryImport(ctx, receipt.Root, receipt.Commit, stage, generationID, spec)
	if err != nil {
		return fail("prepare", domain.ImportPrepareFailed)
	}
	if facts.FileCount != *receipt.FileCount || facts.TotalBytes != *receipt.TotalBytes || facts.ContextBytes != *receipt.ContextBytes || !reflect.DeepEqual(facts.ContextManifest, receipt.ContextManifest) || !reflect.DeepEqual(facts.ProtectedInputs, receipt.ProtectedInputs) {
		return fail("source", domain.ImportSourceChanged)
	}
	binding := domain.RepositoryImportBinding{SchemaVersion: 1, InspectionID: receipt.ID, InspectionDigest: receipt.Digest, ImportOperationID: op.ID, Root: receipt.Root, Commit: receipt.Commit, BaseDigest: digest, ImportSpecDigest: domain.RepositoryImportSpecDigest(receipt.Spec)}
	r.Spec.ProfileID = resolved
	r.Spec.Protected = append(append([]string{}, r.Spec.Protected...), receipt.ProtectedInputs...)
	m := domain.Mission{ID: id, ImportVersion: 1, RepositoryImport: &binding, CodingVersion: 1, WorkspaceMode: r.WorkspaceMode, Title: r.Title, Goal: r.Goal, RepoPath: receipt.Root, BaseCommit: receipt.Commit, State: "draft", Stage: "brief", Spec: r.Spec, Profiles: frozen, ModelSnapshot: modelSnapshot, ModelPolicySnapshot: modelPolicy, ContextManifest: receipt.ContextManifest, Budget: domain.Budget{MaxCalls: r.Spec.MaxCalls}, CandidateDigest: digest, CandidateGeneration: &ref, Messages: []domain.Message{}, Changes: []domain.Change{}, Evidence: []domain.Verification{}, Reviews: []domain.Review{}, Approvals: []domain.Approval{}}
	if e.options.C4Controlled != nil && !e.options.C4Controlled.modelBacked && digest != e.options.C4Controlled.config.BaseDigest {
		return fail("prepare", domain.ImportSourceChanged)
	}
	if modelSnapshot != nil {
		if modelSnapshot.SchemaVersion == 2 {
			m.WorkspaceMode, err = domain.InferenceWorkspaceMode(m.WorkspaceMode)
		} else {
			m.WorkspaceMode, err = domain.AccountWorkspaceMode(m.WorkspaceMode)
		}
		if err != nil {
			return fail("prepare", domain.ImportPrepareFailed)
		}
	}
	seal, err := authority(m, filepath.Join(stage, "base"), false)
	if err != nil {
		return fail("prepare", domain.ImportPrepareFailed)
	}
	m.Approvals = append(m.Approvals, domain.Approval{Kind: "snapshot", Digest: seal, CreatedAt: now()})
	prepared, err := e.s.PrepareRepositoryImport(op, binding, ref)
	if err != nil {
		return fail("prepare", domain.ImportStorageFailed)
	}
	op = prepared
	checkedSource, err := e.recheckImportSource(ctx, receipt, r.RepoPath)
	if err != nil || checkedSource != source {
		return fail("source", domain.ImportSourceChanged)
	}
	if err = workspace.PublishRepositoryImport(stage, e.dir(id), ref, digest); err != nil {
		return fail("publish", domain.ImportPublishFailed)
	}
	// A final identity check rejects drift during publication, but committed tasks
	// never consult the live source again.
	checkedSource, err = e.recheckImportSource(ctx, receipt, r.RepoPath)
	if err != nil || checkedSource != source {
		return fail("source", domain.ImportSourceChanged)
	}
	saved, err := e.s.CommitRepositoryImport(m, op, source)
	if err != nil {
		return fail("commit", domain.ImportStorageFailed)
	}
	return saved, nil
}

func (e *Engine) recheckImportSource(ctx context.Context, v domain.RepositoryInspection, requested string) (string, error) {
	source := requested
	if v.RepositoryID != "" {
		repo, err := e.s.GetGitRepositoryForInspection(ctx, v.RepositoryID)
		if err != nil || repo.Revision != v.RepositoryRevision {
			return "", domain.ImportSourceChanged
		}
		source = repo.Path
	}
	if requested != "" {
		root, err := filepath.EvalSymlinks(requested)
		if err != nil || root != v.Root {
			return "", domain.ImportSourceChanged
		}
	}
	observation, err := gitinspect.Inspect(ctx, source)
	if err != nil || observation.Root != v.Root || observation.HEAD != v.Commit || observation.Unborn {
		return "", domain.ImportSourceChanged
	}
	return source, nil
}

// ValidateRepositoryImport is a read-only source-authority check for diagnosis
// and all action boundaries. It deliberately has no source Git dependency.
func (e *Engine) ValidateRepositoryImport(m domain.Mission) error {
	if err := e.s.ValidateMissionRepositoryImport(m); err != nil {
		return err
	}
	return e.checkRepositoryImportBase(m)
}

// Pure filesystem check: also called inside Store.UpdateMission callbacks.
// Those transactions validate the committed relational binding themselves.
func (e *Engine) checkRepositoryImportBase(m domain.Mission) error {
	if err := domain.ValidateMissionRepositoryImport(m); err != nil {
		return err
	}
	if m.ImportVersion == 0 {
		return nil
	}
	digest, err := workspace.Digest(filepath.Join(e.dir(m.ID), "base"))
	if err != nil || digest != m.RepositoryImport.BaseDigest {
		return domain.ImportInvalidBinding
	}
	return nil
}
