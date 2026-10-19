package accounts

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/store"
)

func TestRecoveryInterruptsPendingWithoutReplayAcrossReopen(t *testing.T) {
	for _, written := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-write", true: "after-write"}[written], func(t *testing.T) {
			s, db, secrets, path := setupAccounts(t)
			a := newAccount(t, s)
			first, err := s.AddCredential(context.Background(), a.ID, 1, "first", "SYNTHETIC-FIRST")
			if err != nil {
				t.Fatal(err)
			}
			current, _ := db.GetModelAccount(a.ID)
			pending, err := db.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "crash", SecretRef: "foundry-model-account-synthetic-crash"}, current.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if written {
				if err := secrets.Set(pending.SecretRef, "SYNTHETIC-CRASH"); err != nil {
					t.Fatal(err)
				}
			}
			setsBefore := secrets.sets
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			recovered := New(reopened, secrets)
			if err := recovered.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := reopened.GetModelCredentialOperation("crash")
			current, _ = reopened.GetModelAccount(a.ID)
			if got.State != "interrupted" || got.CleanupPending || current.CurrentCredentialVersion != 1 || current.Revision != 5 {
				t.Fatalf("bad recovery: %+v %+v", got, current)
			}
			if secrets.sets != setsBefore || secrets.gets != 0 || secrets.values[first.SecretRef] != "SYNTHETIC-FIRST" {
				t.Fatal("recovery replayed or touched available secret")
			}
			if _, ok := secrets.values[pending.SecretRef]; ok {
				t.Fatal("pending secret survived")
			}
			replay, err := recovered.AddCredential(context.Background(), a.ID, 1, "crash", "SYNTHETIC-REPLAY")
			if err != nil || replay.State != "interrupted" || secrets.sets != setsBefore {
				t.Fatalf("interrupted replay: %+v %v", replay, err)
			}
		})
	}
}

func TestRecoveryCleanupFailureBlocksReadinessAndRetriesOnlyDelete(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, s)
	pending, err := db.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "crash", SecretRef: "foundry-model-account-synthetic-crash"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	secrets.values[pending.SecretRef] = "SYNTHETIC-ONLY"
	secrets.deleteErr = errors.New("SYNTHETIC-RAW-ERROR " + pending.SecretRef)
	err = s.Recover(context.Background())
	if err == nil || strings.Contains(err.Error(), "SYNTHETIC") || strings.Contains(err.Error(), pending.SecretRef) {
		t.Fatalf("unsafe recovery success/error: %v", err)
	}
	got, _ := db.GetModelCredentialOperation("crash")
	if got.State != "interrupted" || !got.CleanupPending {
		t.Fatalf("cleanup intent lost: %+v", got)
	}
	secrets.deleteErr = nil
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetModelCredentialOperation("crash")
	if got.CleanupPending || secrets.deletes != 2 || secrets.sets != 0 || secrets.gets != 0 {
		t.Fatalf("wrong cleanup retry: %+v deletes=%d", got, secrets.deletes)
	}
}

func TestFinalCommitFailureKeepsOldCurrentAndCleansNewReference(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	a := newAccount(t, s)
	ctx := context.Background()
	first, err := s.AddCredential(ctx, a.ID, 1, "first", "SYNTHETIC-FIRST")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_, err = raw.Exec(`CREATE TRIGGER synthetic_finish_abort BEFORE UPDATE ON model_credential_versions WHEN NEW.state='available' BEGIN SELECT RAISE(ABORT,'SYNTHETIC-PRIVATE-SQL-ERROR'); END`)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := db.GetModelAccount(a.ID)
	if _, err := s.AddCredential(ctx, a.ID, current.Revision, "failed-commit", "SYNTHETIC-SECOND"); err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
		t.Fatalf("unsafe commit failure: %v", err)
	}
	current, _ = db.GetModelAccount(a.ID)
	second, _ := db.GetModelCredentialOperation("failed-commit")
	if current.CurrentCredentialVersion != 1 || second.State == "available" {
		t.Fatalf("failed commit published: %+v %+v", current, second)
	}
	if secrets.values[first.SecretRef] != "SYNTHETIC-FIRST" {
		t.Fatal("old secret lost")
	}
	if _, ok := secrets.values[second.SecretRef]; ok {
		t.Fatal("new secret was not cleaned")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := New(reopened, secrets).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if secrets.sets != 2 || secrets.gets != 0 {
		t.Fatal("recovery repeated write/read")
	}
}

func TestRevokePersistsBeforeDeleteAndRecoveryPreservesHistory(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	a := newAccount(t, s)
	ctx := context.Background()
	first, err := s.AddCredential(ctx, a.ID, 1, "one", "SYNTHETIC-FIRST")
	if err != nil {
		t.Fatal(err)
	}
	a, _ = db.GetModelAccount(a.ID)
	second, err := s.AddCredential(ctx, a.ID, a.Revision, "two", "SYNTHETIC-SECOND")
	if err != nil {
		t.Fatal(err)
	}
	a, _ = db.GetModelAccount(a.ID)
	secrets.deleteErr = errors.New("SYNTHETIC-DELETE-ERROR")
	if _, err := s.RevokeCredential(ctx, a.ID, second.Version, a.Revision); err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
		t.Fatalf("delete failure hidden/leaked: %v", err)
	}
	revoked, _ := db.GetModelCredential(a.ID, 2)
	a, _ = db.GetModelAccount(a.ID)
	if revoked.State != "revoked" || !revoked.CleanupPending || a.CurrentCredentialVersion != 0 || a.Revision != 6 {
		t.Fatalf("revocation not durable: %+v %+v", revoked, a)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	secrets.deleteErr = nil
	recovered := New(reopened, secrets)
	if err := recovered.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	revoked, _ = reopened.GetModelCredential(a.ID, 2)
	if revoked.CleanupPending || secrets.values[first.SecretRef] != "SYNTHETIC-FIRST" || secrets.sets != 2 || secrets.gets != 0 {
		t.Fatalf("bad revoke cleanup: %+v", revoked)
	}
	replay, err := recovered.AddCredential(ctx, a.ID, 1, "two", "SYNTHETIC-REPLAY")
	if err != nil || replay.State != "revoked" || secrets.sets != 2 {
		t.Fatalf("revoked operation resurrected: %+v %v", replay, err)
	}
	versions, err := reopened.ListModelCredentials(a.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("history lost: %+v %v", versions, err)
	}
}

func TestPendingReplayAndMutationsCannotStealDurableIntent(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, s)
	pending, err := db.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "pending", SecretRef: "foundry-model-account-synthetic-pending"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCredential(context.Background(), a.ID, 2, "pending", "SYNTHETIC-ONLY"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("pending stolen: %v", err)
	}
	a, _ = db.GetModelAccount(a.ID)
	a.Name = "Changed"
	if _, err := s.Save(a, a.Revision); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("metadata allowed with pending: %v", err)
	}
	if _, err := s.RevokeCredential(context.Background(), a.ID, pending.Version, a.Revision); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("pending revoke allowed: %v", err)
	}
	if secrets.sets != 0 || secrets.gets != 0 || secrets.deletes != 0 {
		t.Fatal("pending replay touched secrets")
	}
}

func TestClosedDatabaseAfterSecretWriteLeavesRecoverableIntent(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	a := newAccount(t, s)
	ctx := context.Background()
	first, err := s.AddCredential(ctx, a.ID, 1, "first", "SYNTHETIC-FIRST")
	if err != nil {
		t.Fatal(err)
	}
	a, _ = db.GetModelAccount(a.ID)
	secrets.afterSet = func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}
	if _, err := s.AddCredential(ctx, a.ID, a.Revision, "database-lost", "SYNTHETIC-SECOND"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed database: %v", err)
	}
	secrets.afterSet = nil
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	intent, err := reopened.GetModelCredentialOperation("database-lost")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := reopened.GetModelAccount(a.ID)
	if intent.State != "pending" || current.CurrentCredentialVersion != 1 || secrets.values[intent.SecretRef] != "SYNTHETIC-SECOND" {
		t.Fatalf("intent/current lost: %+v %+v", intent, current)
	}
	if err := New(reopened, secrets).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	intent, _ = reopened.GetModelCredentialOperation("database-lost")
	if intent.State != "interrupted" || intent.CleanupPending || secrets.sets != 2 || secrets.gets != 0 || secrets.values[first.SecretRef] != "SYNTHETIC-FIRST" {
		t.Fatalf("closed database recovery: %+v", intent)
	}
	if _, ok := secrets.values[intent.SecretRef]; ok {
		t.Fatal("abandoned secret survived recovery")
	}
}

func TestFailedWriteCleanupFailureRemainsDurable(t *testing.T) {
	s, db, secrets, _ := setupAccounts(t)
	a := newAccount(t, s)
	secrets.setErr = errors.New("SYNTHETIC-WRITE-ERROR")
	secrets.deleteErr = errors.New("SYNTHETIC-DELETE-ERROR")
	if _, err := s.AddCredential(context.Background(), a.ID, 1, "failed-cleanup", "SYNTHETIC-ONLY"); err == nil {
		t.Fatal("failed write reported success")
	}
	v, err := db.GetModelCredentialOperation("failed-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	a, _ = db.GetModelAccount(a.ID)
	if v.State != "failed" || !v.CleanupPending || a.CurrentCredentialVersion != 0 || a.Revision != 3 {
		t.Fatalf("failed cleanup not durable: %+v %+v", v, a)
	}
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("cleanup failure passed readiness")
	}
	secrets.deleteErr = nil
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if secrets.sets != 1 || secrets.gets != 0 || len(secrets.values) != 0 {
		t.Fatal("cleanup replayed or left secret behind")
	}
}

func TestRevokedBeforeDeleteReopensWithoutRestoringCurrent(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	a := newAccount(t, s)
	ctx := context.Background()
	first, err := s.AddCredential(ctx, a.ID, 1, "first", "SYNTHETIC-FIRST")
	if err != nil {
		t.Fatal(err)
	}
	a, _ = db.GetModelAccount(a.ID)
	if _, err := db.RevokeModelCredential(a.ID, 1, a.Revision); err != nil {
		t.Fatal(err)
	}
	if secrets.deletes != 0 {
		t.Fatal("store invoked external deletion")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := New(reopened, secrets).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = reopened.GetModelAccount(a.ID)
	if a.CurrentCredentialVersion != 0 || secrets.deletes != 1 || secrets.sets != 1 || secrets.gets != 0 {
		t.Fatalf("revoke boundary: %+v", a)
	}
	if _, ok := secrets.values[first.SecretRef]; ok {
		t.Fatal("revoked secret survived")
	}
}

func TestAvailableReopenDoesNotTouchSecretsOrPersistKeyBytes(t *testing.T) {
	s, db, secrets, path := setupAccounts(t)
	a := newAccount(t, s)
	ctx := context.Background()
	const key = "SYNTHETIC-KEY-MUST-NEVER-BE-IN-SQLITE"
	v, err := s.AddCredential(ctx, a.ID, 1, "available", key)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), key) {
		t.Fatal("raw key was persisted in SQLite")
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := New(reopened, secrets).Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetModelCredential(a.ID, 1)
	if err != nil || got != v || secrets.sets != 1 || secrets.gets != 0 || secrets.deletes != 0 {
		t.Fatalf("available recovery touched original: %+v %v", got, err)
	}
}
