package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	LocalMCPTest *mcptest.Descriptor
	// Go-only experimental service; never configured by CLI, environment or HTTP.
	EnableLocalMCP                bool
	MCPSecrets                    model.Secrets
	DisableControlledReadCreation bool
	DataDir, DockerPath           string
	Runner                        sandbox.Runner
}
type CreateRequest struct {
	ImportVersion       int                         `json:"import_version,omitempty"`
	ImportSelection     *domain.ImportSelection     `json:"import_selection,omitempty"`
	CodingVersion       int                         `json:"coding_version,omitempty"`
	MCPSelection        *MCPSelection               `json:"mcp_selection,omitempty"`
	ToolMode            string                      `json:"tool_mode,omitempty"`
	CapabilitySelection *domain.CapabilitySelection `json:"capability_selection,omitempty"`
	WorkspaceMode       string                      `json:"workspace_mode,omitempty"`
	Title               string                      `json:"title"`
	Goal                string                      `json:"goal"`
	RepoPath            string                      `json:"repo_path"`
	Spec                domain.Spec                 `json:"spec"`
}
type operation struct {
	modelInvocationID string // Correlation only, never permission or a transcript.
	id                string
	epoch             int64
	ctx               context.Context
	cancel            context.CancelFunc
	done              chan struct{}
	sandbox           bool
	pausing           bool
	finished          chan struct{}
}
type Engine struct {
	s       *store.Store
	gateway model.Gateway
	options Options
	runner  sandbox.Runner
	mu      sync.Mutex
	active  *operation
	closed  bool
}

func New(s *store.Store, gateway model.Gateway, options Options) *Engine {
	if options.LocalMCPTest != nil {
		d := *options.LocalMCPTest
		d.Binding = cloneMCPBinding(d.Binding)
		options.LocalMCPTest = &d
	}
	abs, _ := filepath.Abs(options.DataDir)
	options.DataDir = abs
	owner := sha256.Sum256([]byte(abs))
	runner := options.Runner
	if runner == nil {
		runner = sandbox.New(options.DockerPath, hex.EncodeToString(owner[:12]))
	}
	return &Engine{s: s, gateway: gateway, options: options, runner: runner}
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func opaque() (string, error) {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func (e *Engine) dir(id string) string { return filepath.Join(e.options.DataDir, "missions", id) }
func (e *Engine) Create(ctx context.Context, r CreateRequest) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return domain.Mission{}, errors.New("engine closed")
	}
	if r.ImportVersion != 0 || r.ImportSelection != nil {
		return e.createRepositoryImport(ctx, r)
	}
	if err := domain.ValidateCodingVersion(r.CodingVersion, r.WorkspaceMode, r.ToolMode); err != nil {
		return domain.Mission{}, err
	}
	if _, bound, err := domain.BaseWorkspaceMode(r.WorkspaceMode); err != nil || bound {
		return domain.Mission{}, errors.New("unsupported workspace mode in creation request")
	}
	remote, err := e.resolveMCPSelection(r)
	if err != nil {
		return domain.Mission{}, err
	}
	bindings, err := e.resolveControlledBindings(ctx, r)
	if err != nil {
		return domain.Mission{}, err
	}
	if err := supportedMode(r.WorkspaceMode); err != nil {
		return domain.Mission{}, err
	}
	if len(r.Title) > 200 || strings.TrimSpace(r.Title) == "" || strings.TrimSpace(r.Goal) == "" || len(r.Goal) > 16000 {
		return domain.Mission{}, errors.New("title and bounded goal are required")
	}
	if r.Spec.MaxCalls == 0 {
		r.Spec.MaxCalls = 16
	}
	r.Spec.ImageID = ""
	if err := domain.ValidateSpec(r.Spec); err != nil {
		return domain.Mission{}, err
	}
	if err := workspace.ValidateRules(r.Spec.Scope); err != nil {
		return domain.Mission{}, err
	}
	if len(r.Spec.Protected) > 0 {
		if err := workspace.ValidateRules(r.Spec.Protected); err != nil {
			return domain.Mission{}, err
		}
	}
	if len(r.Spec.Acceptance) > 12 {
		return domain.Mission{}, errors.New("at most 12 acceptance commands")
	}
	for _, c := range r.Spec.Acceptance {
		if len(c) > 8192 {
			return domain.Mission{}, errors.New("acceptance command too long")
		}
	}
	resolvedDefault, frozen, modelSnapshot, modelPolicySnapshot, err := e.s.ResolveMissionModelSelection(r.Spec.ProfileID)
	if err != nil {
		return domain.Mission{}, err
	}
	r.Spec.ProfileID = resolvedDefault
	id, err := opaque()
	if err != nil {
		return domain.Mission{}, err
	}
	if err := workspace.CheckLocation(ctx, r.RepoPath, e.dir(id)); err != nil {
		return domain.Mission{}, err
	}
	if err := os.MkdirAll(filepath.Join(e.options.DataDir, "missions"), 0700); err != nil {
		return domain.Mission{}, err
	}
	commit, manifest, err := workspace.Snapshot(ctx, r.RepoPath, e.dir(id), r.Spec.Scope)
	if err != nil {
		return domain.Mission{}, err
	}
	digest, err := workspace.Digest(filepath.Join(e.dir(id), "candidate"))
	if err != nil {
		return domain.Mission{}, err
	}
	// Concrete protected paths supplement the default deny policy and remain visible.
	root := filepath.Join(e.dir(id), "base")
	inputs, err := workspace.AcceptanceInputs(root, r.Spec.Acceptance)
	if err != nil {
		return domain.Mission{}, err
	}
	r.Spec.Protected = append(r.Spec.Protected, inputs...)
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			if workspace.Protected(rel) {
				r.Spec.Protected = append(r.Spec.Protected, rel)
			}
		}
		return nil
	})
	if err != nil {
		return domain.Mission{}, err
	}
	if r.Spec.Protected == nil {
		r.Spec.Protected = []string{}
	}
	m := domain.Mission{ID: id, Title: r.Title, Goal: r.Goal, RepoPath: r.RepoPath, BaseCommit: commit, State: "draft", Stage: "brief", Spec: r.Spec, Profiles: frozen, ContextManifest: manifest, Budget: domain.Budget{MaxCalls: r.Spec.MaxCalls}, CandidateDigest: digest, Messages: []domain.Message{}, Changes: []domain.Change{}, Evidence: []domain.Verification{}, Reviews: []domain.Review{}, Approvals: []domain.Approval{}}
	m.WorkspaceMode = r.WorkspaceMode
	m.CodingVersion = r.CodingVersion
	m.ModelSnapshot = modelSnapshot
	m.ModelPolicySnapshot = modelPolicySnapshot
	if modelSnapshot != nil {
		if modelSnapshot.SchemaVersion == 2 {
			m.WorkspaceMode, err = domain.InferenceWorkspaceMode(m.WorkspaceMode)
		} else {
			m.WorkspaceMode, err = domain.AccountWorkspaceMode(m.WorkspaceMode)
		}
		if err != nil {
			return domain.Mission{}, err
		}
	}
	m.ToolMode, m.TaskBindings = r.ToolMode, bindings
	m.MCPBindings = remote
	if bindings != nil && bindings.Git.BaseCommit != commit {
		return domain.Mission{}, errors.New("registered observation HEAD changed during snapshot")
	}
	if domain.UsesGenerations(m.WorkspaceMode) {
		generationID, err := opaque()
		if err != nil {
			return m, err
		}
		ref, _, err := workspace.PrepareGeneration(e.dir(id), generationID, root, nil, nil, m.Spec.Scope, m.Spec.Protected)
		if err != nil {
			return m, err
		}
		m.CandidateGeneration = &ref
		m.CandidateDigest = ref.Digest
		// This exact copy was just created by Snapshot and is never selected in this mode.
		if err = os.RemoveAll(filepath.Join(e.dir(id), "candidate")); err != nil {
			return m, err
		}
	}
	seal, err := authority(m, root, false)
	if err != nil {
		return domain.Mission{}, err
	}
	m.Approvals = append(m.Approvals, domain.Approval{Kind: "snapshot", Digest: seal, CreatedAt: now()})
	return e.s.CreateMission(m)
}
func (e *Engine) checked(id string, revision int64) (domain.Mission, error) {
	if e.closed {
		return domain.Mission{}, errors.New("engine closed")
	}
	m, err := e.s.GetMission(id)
	if err != nil {
		return m, err
	}
	if m.Revision != revision {
		return m, store.ErrConflict
	}
	if err := e.ValidateRepositoryImport(m); err != nil {
		return m, err
	}
	if err := domain.ValidateCodingTrace(m); err != nil {
		return m, err
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return m, err
	}
	if err := supportedMode(m.WorkspaceMode); err != nil {
		return m, err
	}
	return m, nil
}
func (e *Engine) start(m domain.Mission, work func(*operation) error) {
	workflow := m.State == "planning" || m.State == "running"
	ctx, cancel := context.WithCancel(context.Background())
	op := &operation{id: m.ID, epoch: m.Epoch, ctx: ctx, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{}), sandbox: m.State == "running"}
	e.active = op
	go func() {
		err := work(op)
		e.mu.Lock()
		defer e.mu.Unlock()
		if err != nil {
			current, getErr := e.s.GetMission(op.id)
			if getErr == nil && current.Epoch == op.epoch {
				kind := "chat.failed"
				if workflow {
					kind = "mission.blocked"
				}
				_, _ = e.s.UpdateMission(current.ID, current.Revision, kind, func(m *domain.Mission) error {
					if workflow {
						m.State = "blocked"
					}
					m.Error = err.Error()
					return nil
				})
			}
		}
		if !op.pausing {
			if e.active == op {
				e.active = nil
			}
			close(op.finished)
		}
		cancel()
		close(op.done)
	}()
}
func (e *Engine) update(op *operation, kind string, f func(*domain.Mission) error) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.s.GetMission(op.id)
	if err != nil {
		return m, err
	}
	if op.ctx.Err() != nil || m.Epoch != op.epoch {
		return m, context.Canceled
	}
	var mutationErr error
	updated, err := e.s.UpdateMission(m.ID, m.Revision, kind, func(current *domain.Mission) error { mutationErr = f(current); return mutationErr })
	if mutationErr == nil {
		err = codingError("persistence_failed", err)
	}
	return updated, err
}
func (e *Engine) Pause(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	e.mu.Lock()
	m, err := e.checked(id, revision)
	if err != nil {
		e.mu.Unlock()
		return m, err
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) && (e.active == nil || e.active.id != id) && m.State == "planning" {
		defer e.mu.Unlock()
		if err = e.s.CheckMCPLedgerVersion(id); err != nil {
			return m, err
		}
		if err = ctx.Err(); err != nil {
			return m, err
		}
		return e.s.UpdateMission(id, revision, "research.paused", func(m *domain.Mission) error { m.Epoch++; m.State = "paused"; m.Stage = "mcp_paused"; return nil })
	}
	if e.active == nil || e.active.id != id || e.active.pausing {
		e.mu.Unlock()
		return m, errors.New("mission is not running")
	}
	op := e.active
	m, err = e.s.UpdateMission(id, revision, "mission.pausing", func(m *domain.Mission) error { interruptCoding(m); m.Epoch++; m.State = "pausing"; return nil })
	if err != nil {
		e.mu.Unlock()
		return m, err
	}
	op.pausing = true
	op.cancel()
	e.mu.Unlock()
	<-op.done
	// Retain the serial slot while cleanup runs, but do not hold the mutex:
	// other actions can promptly observe busy rather than race into this slot.
	var cleanupErr error
	if op.sandbox {
		cleanupErr = e.runner.Reconcile(context.Background())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() {
		if e.active == op {
			e.active = nil
		}
		close(op.finished)
	}()
	m, err = e.s.GetMission(id)
	if err != nil {
		return m, err
	}
	if cleanupErr != nil {
		m, err = e.s.UpdateMission(id, m.Revision, "pause.cleanup_failed", func(m *domain.Mission) error {
			m.State = "blocked"
			m.Error = "sandbox stop not confirmed; reconcile Docker before replanning"
			return nil
		})
		if err != nil {
			return m, err
		}
		return m, errors.New(m.Error)
	}
	return e.s.UpdateMission(id, m.Revision, "mission.paused", func(m *domain.Mission) error { m.State = "paused"; m.Error = ""; return nil })
}
func (e *Engine) Close() error {
	e.mu.Lock()
	e.closed = true
	op := e.active
	if op != nil {
		op.cancel()
	}
	e.mu.Unlock()
	if op != nil {
		<-op.finished
	}
	return nil
}
func (e *Engine) Recover(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active != nil {
		return errors.New("cannot recover active engine")
	}
	missions, err := e.s.ListMissions()
	if err != nil {
		return err
	}
	if err := validateRecoveryModelSnapshots(missions); err != nil {
		return err
	}
	// Read-only preflight: unsupported/corrupt trace must never be rewritten by
	// recovery, local-operation cleanup, or invocation reconciliation.
	for _, m := range missions {
		if err := domain.ValidateCodingTrace(m); err != nil {
			return err
		}
	}
	for _, m := range missions {
		if unfinishedCoding(m) {
			if _, err = e.s.UpdateMission(m.ID, m.Revision, "coding.interrupted", func(current *domain.Mission) error {
				interruptCoding(current)
				current.Epoch++
				current.State = "blocked"
				current.Error = "interrupted; replan and approve explicitly"
				return nil
			}); err != nil {
				return err
			}
		}
	}
	missions, err = e.s.ListMissions()
	if err != nil {
		return err
	}
	// MCP/research reconciliation is local. Unlike ABv1, v2 can also have
	// explicitly approved execution: retain owner-scoped cleanup without doing
	// ordinary generation/Git recovery or replaying any execution.
	ordinaryRecovery := len(missions) == 0
	executionCleanup := false
	for _, m := range missions {
		if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
			if err := e.s.CheckMCPLedgerVersion(m.ID); err != nil {
				if errors.Is(err, store.ErrMCPFutureVersion) {
					continue
				}
				return err
			}
			// Execute approvals survive epoch fencing and replanning. Current
			// state alone loses this obligation on the next startup.
			for _, approval := range m.Approvals {
				if approval.Kind == "execute" {
					executionCleanup = true
					break
				}
			}
			if err := e.recoverResearchFlow(m.ID); err != nil {
				return err
			}
		} else if m.WorkspaceMode == domain.WorkspaceModeControlledMCPReadV1 {
			if err := e.recoverLocalMCP(m.ID); err != nil {
				return err
			}
		} else if supportedMode(m.WorkspaceMode) == nil {
			ordinaryRecovery = true
		}
	}
	interrupted := false
	var controlledMissionIDs []string
	// Fence active epochs and unfinished idle Main calls before any decoding or
	// corruption handling can mask the original display state/SQL-owned intent.
	for _, m := range missions {
		if (m.ToolMode != domain.ToolModeControlledReadV1 && !(domain.IsInferenceSnapshot(m) && m.ToolMode == "")) || supportedMode(m.WorkspaceMode) != nil {
			continue
		}
		controlledMissionIDs = append(controlledMissionIDs, m.ID)
		unfinished, queryErr := e.s.HasUnfinishedInvocations(m.ID)
		if queryErr != nil {
			return queryErr
		}
		switch m.State {
		case "planning", "running", "verifying", "pausing":
			unfinished = true
		}
		if unfinished {
			interrupted = true
			if _, err = e.s.UpdateMission(m.ID, m.Revision, "mission.interrupted", func(current *domain.Mission) error {
				current.Epoch++
				current.State = "blocked"
				current.Error = "interrupted; replan and approve explicitly"
				return nil
			}); err != nil {
				return err
			}
		}
	}
	missions, err = e.s.ListMissions()
	if err != nil {
		return err
	}
	for _, m := range missions {
		// A future format is readable, but this binary must not rewrite it.
		if domain.IsControlledMCPRead(m.WorkspaceMode, m.ToolMode) {
			continue
		}
		if supportedMode(m.WorkspaceMode) != nil {
			continue
		}
		for _, a := range m.Approvals {
			if a.Kind == "execute" {
				interrupted = true
			}
		}
		switch m.State {
		case "planning", "running", "verifying", "pausing":
			interrupted = true
		}
		if domain.UsesGenerations(m.WorkspaceMode) {
			problem := e.checkSnapshot(m)
			if err = e.interruptUnfinished(m); err != nil {
				return err
			}
			if problem != nil {
				reason := "local operation corruption: " + problem.Error()
				if m.State != "blocked" || m.Error != reason {
					_, err = e.s.UpdateMission(m.ID, m.Revision, "mission.local_corruption", func(current *domain.Mission) error {
						current.State = "blocked"
						current.Error = reason
						current.Epoch++
						return nil
					})
					if err != nil {
						return err
					}
				}
				continue
			}
		}
		switch m.State {
		case "planning", "running", "verifying", "pausing":
			interrupted = true
			_, err = e.s.UpdateMission(m.ID, m.Revision, "mission.interrupted", func(m *domain.Mission) error {
				m.State = "blocked"
				m.Error = "interrupted; replan and approve explicitly"
				m.Epoch++
				return nil
			})
			if err != nil {
				return err
			}
		}
	}
	for _, id := range controlledMissionIDs {
		if err = e.s.RecoverInvocations(id); err != nil {
			return err
		}
	}
	missions, err = e.s.ListMissions()
	if err != nil {
		return err
	}
	for _, m := range missions {
		if (m.ToolMode != domain.ToolModeControlledReadV1 && !(domain.IsInferenceSnapshot(m) && m.ToolMode == "")) || supportedMode(m.WorkspaceMode) != nil {
			continue
		}
		if _, problem := e.verifiedInvocations(m.ID); problem != nil {
			if _, err = e.s.UpdateMission(m.ID, m.Revision, "invocation.integrity_failed", func(current *domain.Mission) error {
				current.State = "blocked"
				current.Error = "invocation evidence/ledger integrity failed; inspect recorded details before new work"
				return nil
			}); err != nil {
				return err
			}
		}
	}
	if !ordinaryRecovery && !executionCleanup {
		return nil
	}
	err = e.runner.Reconcile(ctx)
	if err != nil && (interrupted || executionCleanup) {
		return err
	}
	return nil
}
