package store

import (
	"errors"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
)

func TestSIWCRemoteRevocationGetterNeverCreatesOptionalSchema(t *testing.T) {
	database := openTestStore(t)
	var beforeVersion, afterVersion, beforeObjects, afterObjects int
	if err := database.db.QueryRow(`PRAGMA schema_version`).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'siwc_%'`).Scan(&beforeObjects); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetSIWCRemoteRevocation("authorization"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent optional SIWC schema result: %v", err)
	}
	if err := database.db.QueryRow(`PRAGMA schema_version`).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'siwc_%'`).Scan(&afterObjects); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion || afterObjects != beforeObjects {
		t.Fatalf("diagnostic getter mutated schema: version %d->%d objects %d->%d", beforeVersion, afterVersion, beforeObjects, afterObjects)
	}
}

func TestSIWCHostIDPersistsAcrossConcurrentOpenAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundry.db")
	one, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = one.Close() })
	two, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = two.Close() })

	stores := []*Store{one, two}
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := stores[i%len(stores)].GetOrCreateSIWCHostID()
			ids <- id
			errs <- err
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)

	var first string
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	pattern := regexp.MustCompile(`^urn:uuid:[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for id := range ids {
		if !pattern.MatchString(id) {
			t.Fatalf("invalid UUIDv4 host ID %q", id)
		}
		if first == "" {
			first = id
		} else if id != first {
			t.Fatalf("host identity split: %q != %q", id, first)
		}
	}

	if err := one.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetOrCreateSIWCHostID()
	if err != nil || got != first {
		t.Fatalf("host ID changed after restart: %q %v", got, err)
	}
}

func TestSIWCRemoteRevocationRecordsOnlyFiniteOutcome(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.RecordSIWCRemoteRevocation("authorization", "raw-provider-detail"); err == nil {
		t.Fatal("unsafe remote outcome accepted")
	}
	record, err := db.RecordSIWCRemoteRevocation("authorization", "unconfirmed")
	if err != nil || record.AuthorizationID != "authorization" || record.Outcome != "unconfirmed" || record.UpdatedAt == "" {
		t.Fatalf("bad unconfirmed record: %+v %v", record, err)
	}
	record, err = db.RecordSIWCRemoteRevocation("authorization", "confirmed")
	if err != nil || record.Outcome != "confirmed" {
		t.Fatalf("bad confirmed record: %+v %v", record, err)
	}
	got, err := db.GetSIWCRemoteRevocation("authorization")
	if err != nil || got != record {
		t.Fatalf("record did not persist: %+v %v", got, err)
	}
}

func TestSIWCRemoteRevocationConfirmedOutcomeCannotBeDowngraded(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	confirmed, err := db.RecordSIWCRemoteRevocation("authorization", "confirmed")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := db.RecordSIWCRemoteRevocation("authorization", "unconfirmed")
	if err != nil {
		t.Fatal(err)
	}
	if actual != confirmed {
		t.Fatalf("confirmed receipt was downgraded: confirmed=%+v actual=%+v", confirmed, actual)
	}
	stored, err := db.GetSIWCRemoteRevocation("authorization")
	if err != nil || stored != confirmed {
		t.Fatalf("confirmed receipt changed: stored=%+v err=%v", stored, err)
	}
}

func TestSIWCRemoteRevocationClaimHasOneDurableWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foundry.db")
	one, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()

	stores := []*Store{one, two}
	winners := make(chan bool, len(stores))
	errs := make(chan error, len(stores))
	var wait sync.WaitGroup
	for _, database := range stores {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			winner, claimErr := database.ClaimSIWCRemoteRevocation("authorization")
			winners <- winner
			errs <- claimErr
		}(database)
	}
	wait.Wait()
	close(winners)
	close(errs)
	count := 0
	for err = range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for winner := range winners {
		if winner {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("durable revoke claim winners=%d", count)
	}

	if err = one.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if winner, err := reopened.ClaimSIWCRemoteRevocation("authorization"); err != nil || winner {
		t.Fatalf("claim replay became a winner: winner=%v err=%v", winner, err)
	}
}

func TestSIWCRemoteClaimAndLocalRevokeCommitAtomically(t *testing.T) {
	database := openTestStore(t)
	binding := authorizationFixture(t, database)
	if _, err := database.BeginModelAuthorization(binding.AccountID, "sign-in", "sign_in", binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CommitModelAuthorization("sign-in", binding, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	authorization, winner, err := database.ClaimAndRevokeSIWCRemoteAuthorization(binding.ID)
	if err != nil || !winner || authorization.State != "revoked" {
		t.Fatalf("first claim/revoke: %+v winner=%v err=%v", authorization, winner, err)
	}
	authorization, winner, err = database.ClaimAndRevokeSIWCRemoteAuthorization(binding.ID)
	if err != nil || winner || authorization.State != "revoked" {
		t.Fatalf("replayed claim/revoke: %+v winner=%v err=%v", authorization, winner, err)
	}
	pending, err := database.PendingModelAuthorizationCleanup()
	if err != nil || len(pending) == 0 {
		t.Fatalf("atomic revoke omitted cleanup intent: %+v %v", pending, err)
	}
}
