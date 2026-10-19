package hooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"foundry/internal/domain"
	"foundry/internal/store"
)

const (
	pollInterval = 500 * time.Millisecond
	batchLimit   = 100
)

type HookRuntimeStatus struct {
	State         string `json:"state"`
	LastAttemptAt string `json:"last_attempt_at"`
	LastSuccessAt string `json:"last_success_at"`
	ErrorCode     string `json:"error_code"`
}

type Snapshot struct {
	HookPoints []domain.HookPoint        `json:"hook_points"`
	Rules      []domain.HookRule         `json:"rules"`
	Processor  domain.HookProcessorState `json:"processor"`
	Runtime    HookRuntimeStatus         `json:"runtime"`
}

type RuleInput struct {
	ID               string   `json:"id"`
	ExpectedRevision int64    `json:"expected_revision"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	EventKinds       []string `json:"event_kinds"`
	MissionID        string   `json:"mission_id"`
	Action           string   `json:"action"`
}

type RecordResponse struct {
	Records    []domain.HookRecord       `json:"records"`
	NextBefore int64                     `json:"next_before"`
	Processor  domain.HookProcessorState `json:"processor"`
	Runtime    HookRuntimeStatus         `json:"runtime"`
}

// Service is a bounded local observer. Its only dependency is the durable
// Store; it has no authority to call models, tools, processes, or networks.
type Service struct {
	store *store.Store

	mu      sync.Mutex
	runtime HookRuntimeStatus
	cancel  context.CancelFunc
	done    chan struct{}
}

func New(persistent *store.Store) *Service {
	return &Service{store: persistent, runtime: HookRuntimeStatus{State: "not_started"}}
}

func (s *Service) Start() {
	s.mu.Lock()
	if s.runtime.State != "not_started" {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	s.runtime.State = "running"
	s.runtime.ErrorCode = ""
	done := s.done
	s.mu.Unlock()

	go s.run(ctx, done)
}

func (s *Service) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	s.attempt(ctx)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.attempt(ctx)
		}
	}
}

func (s *Service) attempt(ctx context.Context) {
	// Never hold the runtime mutex over a SQLite transaction.
	_, err := s.store.ProcessHookEvents(ctx, batchLimit)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime.LastAttemptAt = now
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		s.runtime.State = "degraded"
		s.runtime.ErrorCode = "observation_storage_error"
		return
	}
	s.runtime.State = "running"
	s.runtime.LastSuccessAt = now
	s.runtime.ErrorCode = ""
}

func (s *Service) Close() {
	s.mu.Lock()
	if s.runtime.State == "stopped" {
		s.mu.Unlock()
		return
	}
	if s.runtime.State == "not_started" {
		s.runtime.State = "stopped"
		s.mu.Unlock()
		return
	}
	cancel, done := s.cancel, s.done
	s.mu.Unlock()

	cancel()
	<-done

	s.mu.Lock()
	s.runtime.State = "stopped"
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
}

func (s *Service) Snapshot() (Snapshot, error) {
	var result Snapshot
	rules, err := s.store.HookRules()
	if err != nil {
		return result, err
	}
	processor, err := s.store.HookState()
	if err != nil {
		return result, err
	}
	result.HookPoints = domain.HookPoints()
	result.Rules = rules
	result.Processor = processor
	result.Runtime = s.runtimeStatus()
	return result, nil
}

func (s *Service) SaveRule(input RuleInput) (domain.HookRule, error) {
	if input.ExpectedRevision < 0 || (input.ExpectedRevision == 0 && input.ID != "") || (input.ExpectedRevision > 0 && input.ID == "") {
		return domain.HookRule{}, domain.ErrInvalidHook
	}
	id := input.ID
	if input.ExpectedRevision == 0 {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return domain.HookRule{}, err
		}
		id = hex.EncodeToString(bytes)
	}
	return s.store.SaveHookRule(domain.HookRule{
		ID: id, Name: input.Name, Enabled: input.Enabled,
		EventKinds: input.EventKinds, MissionID: input.MissionID, Action: input.Action,
	}, input.ExpectedRevision)
}

func (s *Service) Records(missionID string, before int64, limit int) (RecordResponse, error) {
	var result RecordResponse
	page, err := s.store.HookRecords(missionID, before, limit)
	if err != nil {
		return result, err
	}
	processor, err := s.store.HookState()
	if err != nil {
		return result, err
	}
	result.Records = page.Records
	result.NextBefore = page.NextBefore
	result.Processor = processor
	result.Runtime = s.runtimeStatus()
	return result, nil
}

func (s *Service) runtimeStatus() HookRuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime
}
