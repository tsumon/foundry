package engine

import (
	"context"
	"database/sql"
	"errors"
	"foundry/internal/c4fixture"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Losing the durable approval check would dispatch the owned readiness seam.
func TestC4ControlIndependentReadiness(t *testing.T) {
	e, _, m, d, f, _ := c4FixtureMode(t, true)
	calls := 0
	e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
		calls++
		return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("c", 64)}, nil
	}
	if _, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("baseline without readiness")
	}
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[0]
	if _, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || calls != 0 {
		t.Fatal("unapproved readiness dispatched")
	}
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || d.creates != 0 {
		t.Fatal("approval had effects")
	}
	if _, err = e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || calls != 0 {
		t.Fatal("wrong control kind dispatched")
	}
	m, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || d.creates != 0 || f.calls != 0 || m.C4Controls[0].Result.Status != "ready" {
		t.Fatal("wrong readiness result/effects")
	}
	if _, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || calls != 1 {
		t.Fatal("replayed readiness")
	}
	m = c4Baseline(t, e, m)
	if m.C4Operations[0].ReadinessID != c.ID || !m.C4Operations[0].Passed() {
		t.Fatal("baseline unbound")
	}
}

func TestC4ControlledCleanupRejectsChangedOriginalGeneration(t *testing.T) {
	e, s, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	e.options.C4Controlled.models = &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`, `{"summary":"changed","edits":[{"path":"main.go","content":"package demo\nfunc Value() int { return 2 }\n"}]}`}}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	d.cleanupFail = true
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if len(m.C4Operations) != 2 {
		t.Fatal(m.State, m.Error)
	}
	o := m.C4Operations[1]
	path, err := workspace.GenerationPath(e.dir(m.ID), *o.CandidateGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path, "main.go"), []byte("package demo // unexpected drift\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inspects, removes := d.inspects, d.removes
	if _, err = e.PrepareC4Cleanup(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, o.ContainerID); err == nil || d.inspects != inspects || d.removes != removes {
		t.Fatal("changed original generation authorized cleanup")
	}
}

func TestC4ControlledConstructorProvenance(t *testing.T) {
	e, s, _, d, _, _ := c4FixtureMode(t, true)
	c := e.options.C4Controlled.config
	c.Profile.Mode = "docker"
	if _, err := NewC4ControlledSyntheticCapability(c, sandbox.NewC4SyntheticDriver(d), &scripted{}, func(context.Context) (sandbox.C4ReadinessResult, error) { return sandbox.C4ReadinessResult{}, nil }); err == nil {
		t.Fatal("synthetic Docker provenance")
	}
	// These declared paths do not exist: construction must not probe them.
	c.Profile.DockerPath = "/owned-nonexistent-fixture/docker"
	c.Profile.Endpoint = "unix:///owned-nonexistent-fixture/docker.sock"
	cap, err := NewC4DockerCapability(c, c4fixture.New(s))
	if err != nil || cap == nil {
		t.Fatal("constructor performed runtime probes", err)
	}
	c.Enabled = false
	if _, err = NewC4DockerCapability(c, c4fixture.New(s)); err == nil {
		t.Fatal("disabled activation constructed")
	}
}

func TestC4ActivationRejectsMalformedSourceDeclaration(t *testing.T) {
	e, _, _, _, _, _ := c4FixtureMode(t, true)
	for _, root := range []string{"relative", "/owned/source\x00", "/owned/source\n", "/owned/../source", "/" + strings.Repeat("a", 4096)} {
		c := e.options.C4Controlled.config
		c.SourceRoot = root
		if ValidateC4Activation(c, time.Now()) == nil {
			t.Fatal("invalid source declaration accepted")
		}
	}
}

func TestC4ControlledStatusIsNotLegacySimulationConsent(t *testing.T) {
	e, _, m, _, _, _ := c4FixtureMode(t, true)
	status := e.C4ControlledStatus(m)
	if !status.Available || !status.StructurallyAvailable || status.SourceKind != "synthetic_c4_control" || status.Expired || e.C4SimulationAvailable(m) {
		t.Fatal("controlled capability confused with legacy simulation", status)
	}
	m = controlReady(t, e, m)
	receipt := m.C4Controls[0]
	if status.ActivationDigest != receipt.ActivationDigest || status.ExpiresAt != receipt.ActivationExpiresAt {
		t.Fatal("status does not bind current activation")
	}
	e.options.C4Controlled.config.ExpiresAt = time.Now().Add(90 * time.Minute).Format(time.RFC3339Nano)
	status = e.C4ControlledStatus(m)
	if !status.Available || !status.StructurallyAvailable || status.ActivationDigest == receipt.ActivationDigest || status.ExpiresAt == receipt.ActivationExpiresAt {
		t.Fatal("renewed activation not distinguishable from old receipt", status)
	}
	if _, err := e.currentC4Readiness(m); err == nil {
		t.Fatal("renewal reused old readiness")
	}
	e.c4Clock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	status = e.C4ControlledStatus(m)
	if status.Available || !status.StructurallyAvailable || !status.Expired {
		t.Fatal("expired status appears available")
	}
	m.BaseCommit = strings.Repeat("f", 40)
	if e.C4ControlledStatus(m).StructurallyAvailable {
		t.Fatal("mismatched history available")
	}
}

func TestC4ControlledDiagnosticRetainsUncertainControl(t *testing.T) {
	e, _, m, _, _, _ := c4FixtureMode(t, true)
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := c4Diagnostic(m)
	if diagnostic.Code == "synthetic_records_only" || len(diagnostic.Records) != 1 || diagnostic.Status != domain.RecoveryBlocked {
		t.Fatal("control absent from diagnostic", diagnostic)
	}
}

func TestC4VerificationNeverPromotesIncompleteOrSyntheticReceipt(t *testing.T) {
	e, _, m, _, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	o := m.C4Operations[0]
	m.Spec.C4.Mode = "docker"
	if c4Verification(m, o).EffectiveSource() == "isolated_execution" {
		t.Fatal("synthetic receipt promoted by mode declaration")
	}
	m.C4Controls[0].SourceKind = "docker_c4_control"
	o.Result = &domain.C4Result{Class: "outcome_unknown"}
	if c4Verification(m, o).EffectiveSource() == "isolated_execution" {
		t.Fatal("incomplete operation promoted")
	}
}

func TestC4ControlFailurePersistenceAndRecovery(t *testing.T) {
	for _, dispatchFailure := range []bool{true, false} {
		t.Run(map[bool]string{true: "dispatch", false: "result"}[dispatchFailure], func(t *testing.T) {
			e, s, m, d, f, _ := c4FixtureMode(t, true)
			db, err := sql.Open("sqlite3", d.dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fail := func() {
				if _, err := db.Exec(`CREATE TRIGGER fail_control BEFORE UPDATE ON missions BEGIN SELECT RAISE(ABORT,'owned persistence fault'); END`); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
				calls++
				fail()
				return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("c", 64)}, nil
			}
			m, err = e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			c := m.C4Controls[0]
			m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
			if err != nil {
				t.Fatal(err)
			}
			if dispatchFailure {
				fail()
			}
			if _, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil {
				t.Fatal("persistence fault ignored")
			}
			if dispatchFailure && calls != 0 || !dispatchFailure && calls != 1 || d.creates != 0 || f.calls != 0 {
				t.Fatal("transport escaped CAS")
			}
			if _, err = db.Exec(`DROP TRIGGER fail_control`); err != nil {
				t.Fatal(err)
			}
			if !dispatchFailure {
				if err = e.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
				m, err = s.GetMission(m.ID)
				if err != nil {
					t.Fatal(err)
				}
				if m.C4Controls[0].Result == nil || m.C4Controls[0].Result.Status != "outcome_unknown" || calls != 1 || f.calls != 0 {
					t.Fatal("restart replayed or lost uncertainty")
				}
			}
		})
	}
}

func TestC4ControlReadinessCannotStrandPendingBaseline(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared", true: "approved"}[approved], func(t *testing.T) {
			e, s, m, d, f, _ := c4FixtureMode(t, true)
			m = controlReady(t, e, m)
			m, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			o := m.C4Operations[0]
			if approved {
				m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
				if err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
				calls++
				return sandbox.C4ReadinessResult{}, nil
			}
			if _, err = e.PrepareC4Readiness(context.Background(), m.ID, m.Revision); err == nil {
				t.Fatal("readiness preparation stranded pending baseline")
			}
			stored, err := s.GetMission(m.ID)
			if err != nil || !reflect.DeepEqual(stored, m) || calls != 0 || d.creates != 0 || d.starts != 0 || d.inspects != 0 || d.removes != 0 || f.calls != 0 {
				t.Fatal("rejected preparation changed durable facts or invoked effects", err)
			}
			if !approved {
				m, err = e.ApproveC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
				if err != nil {
					t.Fatal(err)
				}
			}
			m, err = e.ExecuteC4Baseline(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest)
			if err != nil || !m.C4Operations[0].Passed() || d.creates != 1 {
				t.Fatal("original baseline no longer executable", err)
			}
		})
	}
}

func TestC4ControlStaleAuthorityZeroEffects(t *testing.T) {
	for _, mutation := range []string{"expired", "activation", "source", "base", "profile", "overlap", "canceled", "revision", "digest", "epoch"} {
		t.Run(mutation, func(t *testing.T) {
			e, s, m, d, _, _ := c4FixtureMode(t, true)
			calls := 0
			e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
				calls++
				return sandbox.C4ReadinessResult{}, errors.New("fixture")
			}
			m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			c := m.C4Controls[0]
			m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			rev := m.Revision
			digest := c.MaterialDigest
			switch mutation {
			case "expired":
				e.c4Clock = func() time.Time { return time.Now().Add(11 * time.Minute) }
			case "activation":
				e.options.C4Controlled.config.ExpiresAt = time.Now().Add(2 * time.Hour).Format(time.RFC3339Nano)
			case "source":
				e.options.C4Controlled.source = "docker_c4_control"
			case "base":
				e.options.C4Controlled.config.BaseDigest = strings.Repeat("0", 64)
			case "profile":
				e.options.C4Controlled.config.Profile.ImageID = "sha256:" + strings.Repeat("0", 64)
			case "overlap":
				e.options.C4 = NewC4SyntheticCapability(sandbox.NewC4SyntheticDriver(d), &scripted{}, "/synthetic/docker", "unix:///synthetic/docker.sock")
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "revision":
				rev--
			case "digest":
				digest = strings.Repeat("0", 64)
			case "epoch":
				m, err = s.UpdateMission(m.ID, m.Revision, "fixture.epoch", func(x *domain.Mission) error { x.Epoch++; return nil })
				if err != nil {
					t.Fatal(err)
				}
				rev = m.Revision
			}
			if _, err = e.ExecuteC4Readiness(ctx, m.ID, rev, c.ID, digest); err == nil || calls != 0 || d.creates != 0 {
				t.Fatal("stale authority dispatched")
			}
		})
	}
}

func TestC4ControlReadinessFailureNoRetry(t *testing.T) {
	e, _, m, d, _, _ := c4FixtureMode(t, true)
	calls := 0
	e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
		calls++
		return sandbox.C4ReadinessResult{}, errors.New("sensitive transport output")
	}
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[0]
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err == nil || m.C4Controls[0].Result.FailureCode != "failed" {
		t.Fatal("failure not bounded")
	}
	if _, err = e.PrepareC4Baseline(context.Background(), m.ID, m.Revision); err == nil || calls != 1 || d.creates != 0 {
		t.Fatal("failure authorized execution")
	}
}

func TestC4ControlledExpiryAfterDispatchDoesNotStrandLifecycle(t *testing.T) {
	e, _, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	initial := time.Now()
	e.c4Clock = func() time.Time { return initial }
	d.afterCreate = func() { initial = initial.Add(2 * time.Hour) }
	m = c4Baseline(t, e, m)
	if !m.C4Operations[0].Passed() || d.removes != 1 {
		t.Fatal("expiry stranded dispatched lifecycle")
	}
	if _, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("expired activation renewed authority")
	}
}

func TestC4ControlledCleanupRenewedActivationAndCAS(t *testing.T) {
	e, s, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	d.cleanupFail = true
	m = c4Baseline(t, e, m)
	o := m.C4Operations[0]
	initial := time.Now().Add(2 * time.Hour)
	e.c4Clock = func() time.Time { return initial }
	if _, err := e.PrepareC4Cleanup(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, o.ContainerID); err == nil {
		t.Fatal("expired config authorized recovery")
	}
	e.options.C4Controlled.config.ExpiresAt = initial.Add(time.Hour).Format(time.RFC3339Nano)
	m, err := e.PrepareC4Cleanup(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, o.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[len(m.C4Controls)-1]
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", d.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`CREATE TRIGGER fail_cleanup BEFORE UPDATE ON missions BEGIN SELECT RAISE(ABORT,'owned fault'); END`)
	before := d.removes
	if _, err = e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || d.removes != before {
		t.Fatal("cleanup effects before dispatch CAS")
	}
	db.Exec(`DROP TRIGGER fail_cleanup`)
	// A failed dispatch CAS did not consume authority; successful dispatch is one-shot.
	d.cleanupFail = false
	m, err = e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if d.removes != before+1 || m.C4Operations[0].Result.Class != o.Result.Class {
		t.Fatal("renewal replayed execution")
	}
	if _, err = e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || d.removes != before+1 {
		t.Fatal("cleanup replay")
	}
	stored, _ := s.GetMission(m.ID)
	if stored.C4Controls[len(stored.C4Controls)-1].ActivationDigest == stored.C4Controls[0].ActivationDigest {
		t.Fatal("old activation reused")
	}
}

func TestC4ControlledReadinessSerializesPauseAndDuplicate(t *testing.T) {
	e, _, m, _, _, _ := c4FixtureMode(t, true)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
		calls.Add(1)
		close(entered)
		<-release
		return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("c", 64)}, nil
	}
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[0]
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
		done <- err
	}()
	<-entered
	paused := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); paused <- err }()
	duplicate := make(chan error, 1)
	go func() {
		_, err := e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
		duplicate <- err
	}()
	select {
	case <-paused:
		t.Fatal("pause passed serialization during dispatch")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if <-paused == nil || <-duplicate == nil || calls.Load() != 1 {
		t.Fatal("stale concurrent mutation escaped")
	}
}

func TestC4ControlledCleanupSerializesPauseAndExactTarget(t *testing.T) {
	e, _, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	d.cleanupFail = true
	m = c4Baseline(t, e, m)
	o := m.C4Operations[0]
	inspections, removes := d.inspects, d.removes
	if _, err := e.PrepareC4Cleanup(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, strings.Repeat("e", 64)); err == nil || d.inspects != inspections || d.removes != removes {
		t.Fatal("foreign target inspected")
	}
	m, err := e.PrepareC4Cleanup(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, o.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[len(m.C4Controls)-1]
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	d.cleanupFail = false
	d.beforeRemove = func() { close(entered); <-release }
	done := make(chan error, 1)
	go func() {
		_, err := e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
		done <- err
	}()
	<-entered
	paused := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); paused <- err }()
	select {
	case <-paused:
		t.Fatal("cleanup lost engine serialization")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if <-paused == nil || d.removes != removes+1 {
		t.Fatal("pause mutated cleanup authority")
	}
}

func TestC4ControlledNewReceiptInvalidatesPlanApproval(t *testing.T) {
	e, s, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	e.options.C4Controlled.models = &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`}}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error)
	}
	m = controlReady(t, e, m)
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil || d.creates != 1 {
		t.Fatal("new readiness silently renewed old plan approval")
	}
}

func TestC4ControlledExpiredReceiptRejectsExecutionApproval(t *testing.T) {
	e, s, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	e.options.C4Controlled.models = &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`}}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	e.c4Clock = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		finishCoding(e)
		t.Fatal("expired receipt allowed execution approval")
	}
	if d.creates != 1 {
		t.Fatal("expired receipt dispatched")
	}
}

// Local planning consumes its own context consent, never new transport authority.
// A passing historical baseline survives expiry, but Worker dispatch does not.
func TestC4ControlledPlanningAfterExpiryRemainsLocal(t *testing.T) {
	for _, delay := range []time.Duration{11 * time.Minute, 2 * time.Hour} {
		t.Run(delay.String(), func(t *testing.T) {
			e, s, m, d, forbidden, _ := c4FixtureMode(t, true)
			m = controlReady(t, e, m)
			m = c4Baseline(t, e, m)
			creates, starts, inspects, removes := d.creates, d.starts, d.inspects, d.removes
			readyCalls := 0
			e.options.C4Controlled.readiness = func(context.Context) (sandbox.C4ReadinessResult, error) {
				readyCalls++
				return sandbox.C4ReadinessResult{}, errors.New("unexpected readiness")
			}
			gateway := &scripted{outputs: []string{`{"summary":"change","steps":[{"id":"one","title":"change","description":"update","depends_on":[],"files":["main.go"]}]}`}}
			e.options.C4Controlled.models = gateway
			e.c4Clock = func() time.Time { return time.Now().Add(delay) }
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, err := s.GetMission(m.ID)
			if err != nil || m.State != "awaiting_approval" {
				t.Fatal("local planning incorrectly needs fresh transport authority", m.State, m.Error, err)
			}
			if len(gateway.roles) != 1 || gateway.roles[0] != domain.Planner {
				t.Fatal("unexpected role dispatch", gateway.roles)
			}
			if _, err = e.Approve(context.Background(), m.ID, m.Revision); err == nil {
				finishCoding(e)
				t.Fatal("expired authority dispatched Worker")
			}
			if readyCalls != 0 || forbidden.calls != 0 || len(gateway.roles) != 1 || d.creates != creates || d.starts != starts || d.inspects != inspects || d.removes != removes {
				t.Fatal("local planning or rejected approval escaped local authority")
			}
		})
	}
}

func controlReady(t *testing.T, e *Engine, m domain.Mission) domain.Mission {
	t.Helper()
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[len(m.C4Controls)-1]
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.ExecuteC4Readiness(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestC4ControlExpiredMaterialAndReceipt(t *testing.T) {
	e, _, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	future := time.Now().Add(11 * time.Minute)
	e.c4Clock = func() time.Time { return future }
	if _, err := e.PrepareC4Baseline(context.Background(), m.ID, m.Revision); err == nil || d.creates != 0 {
		t.Fatal("expired receipt admitted")
	}
	m, err := e.PrepareC4Readiness(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[len(m.C4Controls)-1]
	future = future.Add(11 * time.Minute)
	if _, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil {
		t.Fatal("expired material approved")
	}
}

func TestC4ControlCleanupIndependent(t *testing.T) {
	e, _, m, d, _, _ := c4FixtureMode(t, true)
	m = controlReady(t, e, m)
	d.cleanupFail = true
	m = c4Baseline(t, e, m)
	o := m.C4Operations[0]
	before := d.removes
	if _, err := e.CleanupC4Operation(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, o.ContainerID); err == nil || d.removes != before {
		t.Fatal("legacy cleanup bypass")
	}
	m, err := e.PrepareC4Cleanup(context.Background(), m.ID, m.Revision, o.ID, o.MaterialDigest, o.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	c := m.C4Controls[len(m.C4Controls)-1]
	if _, err = e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest); err == nil || d.removes != before {
		t.Fatal("cleanup without approval")
	}
	m, err = e.ApproveC4Control(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	d.cleanupFail = false
	m, err = e.ExecuteC4Cleanup(context.Background(), m.ID, m.Revision, c.ID, c.MaterialDigest)
	if err != nil {
		t.Fatal(err)
	}
	if d.removes != before+1 || m.C4Controls[len(m.C4Controls)-1].Result.Status != "cleaned" || m.C4Operations[0].Result.Class != o.Result.Class {
		t.Fatal("cleanup changed run")
	}
}
