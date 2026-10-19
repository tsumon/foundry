package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"sync"
	"testing"
	"time"
)

type checkpointWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

// Removing the second receipt/state/owner checks would turn a published
// checkpoint into authority after cancellation, a state change, or replacement.
func TestDecideResearchMCPCheckpointWaitRejectsChangedAuthority(t *testing.T) {
	for _, change := range []string{"cancel", "foreign-owner", "epoch", "revision", "stage", "replacement-owner"} {
		t.Run(change, func(t *testing.T) {
			e, s, m := researchFixture(t)
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = researchIdle(t, e, m.ID)
			proposals, err := s.ListMCPProposals(m.ID)
			if err != nil || len(proposals) != 1 {
				t.Fatal(proposals, err)
			}
			p := proposals[0]
			ownerCtx, ownerCancel := context.WithCancel(context.Background())
			defer ownerCancel()
			owner := &operation{id: m.ID, epoch: m.Epoch, ctx: ownerCtx, cancel: ownerCancel, done: make(chan struct{}), finished: make(chan struct{})}
			if change == "foreign-owner" {
				owner.id = "foreign"
			}
			e.mu.Lock()
			e.active = owner
			e.mu.Unlock()
			defer func() { e.mu.Lock(); e.active = nil; e.mu.Unlock() }()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiting := make(chan struct{})
			ctx := &checkpointWaitContext{Context: base, waiting: waiting}
			result := make(chan error, 1)
			go func() {
				_, err := e.DecideResearchMCP(ctx, m.ID, m.Revision, p.ID, p.Digest, "negative-checkpoint", true)
				result <- err
			}()
			if change != "foreign-owner" {
				select {
				case <-waiting:
				case <-time.After(5 * time.Second):
					t.Fatal("decision did not wait")
				}
				if change == "cancel" {
					cancel()
				} else {
					e.mu.Lock()
					if change == "replacement-owner" {
						e.active = &operation{id: "replacement", epoch: 1}
					} else {
						switch change {
						case "epoch":
							_, err = fixtureSQL(t, e).Exec(`UPDATE missions SET data=json_set(data,'$.epoch',?) WHERE id=?`, m.Epoch+1, m.ID)
						case "stage":
							_, err = fixtureSQL(t, e).Exec(`UPDATE missions SET data=json_set(data,'$.stage','mcp_observation_ready') WHERE id=?`, m.ID)
						default:
							_, err = s.UpdateMission(m.ID, m.Revision, "fixture.change", func(*domain.Mission) error { return nil })
						}
						e.active = nil
					}
					close(owner.done)
					close(owner.finished)
					e.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case err = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("decision failed to return")
			}
			if err == nil {
				t.Fatal("changed checkpoint authority accepted")
			}
			if change == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err = s.GetMCPDecision(m.ID, "negative-checkpoint"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("denied decision persisted", err)
			}
			calls, err := s.ListMCPInvocations(m.ID)
			if err != nil || len(calls) != 0 {
				t.Fatal("denied decision dispatched MCP", err)
			}
			after, _ := s.GetMission(m.ID)
			models, _ := s.ListInvocations(m.ID, 0, 100)
			if after.Budget.UsedCalls != m.Budget.UsedCalls || len(models) != 1 {
				t.Fatal("denied decision resumed model")
			}
			if change == "foreign-owner" || change == "cancel" {
				e.mu.Lock()
				unchanged := e.active == owner && owner.ctx.Err() == nil
				e.mu.Unlock()
				if !unchanged {
					t.Fatal("decision changed live owner")
				}
			}
		})
	}
}

func (c *checkpointWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestDecideResearchMCPWaitsForPublishedCheckpointOwner(t *testing.T) {
	e, s, m := researchFixture(t)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	proposals, err := s.ListMCPProposals(m.ID)
	if err != nil || len(proposals) != 1 {
		t.Fatal("pending proposal", err)
	}
	p := proposals[0]

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	owner := &operation{
		id:       m.ID,
		epoch:    m.Epoch,
		ctx:      ownerCtx,
		cancel:   cancelOwner,
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	e.mu.Lock()
	e.active = owner
	e.mu.Unlock()

	waiting := make(chan struct{})
	abort := make(chan struct{})
	cleanupErr := make(chan error, 1)
	go func() {
		finishOwner := func() error {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.active != owner {
				return errors.New("checkpoint owner changed while decision waited")
			}
			e.active = nil
			close(owner.done)
			close(owner.finished)
			return nil
		}
		select {
		case <-waiting:
			if _, err := s.GetMCPDecision(m.ID, "checkpoint-decision"); !errors.Is(err, store.ErrNotFound) {
				_ = finishOwner()
				cleanupErr <- errors.New("decision mutated before checkpoint owner finished")
				return
			}
			cleanupErr <- finishOwner()
		case <-abort:
			cleanupErr <- finishOwner()
		}
	}()

	ctx := &checkpointWaitContext{Context: context.Background(), waiting: waiting}
	_, err = e.DecideResearchMCP(ctx, m.ID, m.Revision, p.ID, p.Digest, "checkpoint-decision", true)
	close(abort)
	if cleanup := <-cleanupErr; cleanup != nil {
		t.Fatal(cleanup)
	}
	if err != nil {
		t.Fatal(err)
	}
	decision, err := s.GetMCPDecision(m.ID, "checkpoint-decision")
	if err != nil || !decision.Approved || decision.ProposalID != p.ID {
		t.Fatalf("approved decision not recorded once: %+v %v", decision, err)
	}
	if _, err = e.DecideResearchMCP(context.Background(), m.ID, m.Revision, p.ID, p.Digest, "checkpoint-decision", true); err != nil {
		t.Fatal("exact duplicate decision lost", err)
	}
	calls, err := s.ListMCPInvocations(m.ID)
	if err != nil || len(calls) != 1 {
		t.Fatalf("approval dispatched %d calls: %v", len(calls), err)
	}
}
