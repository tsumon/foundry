package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
)

func testEnvelope(body []byte) domain.EvidenceEnvelope {
	sum := sha256.Sum256(body)
	return domain.EvidenceEnvelope{SchemaVersion: 1, Kind: "tool_observation", InvocationID: "inv", MissionID: "mission", Epoch: 1, Producer: "repo.read", CapturedAt: "2026-10-18T00:00:00Z", Digest: hex.EncodeToString(sum[:]), Bytes: int64(len(body)), ContentType: "application/json", BindingDigest: strings.Repeat("a", 64)}
}

// Removing the digest check would accept a same-size, well-formed replacement.
func TestEvidenceReadRejectsSameLengthValidJSONDigestTamper(t *testing.T) {
	s := New(t.TempDir())
	body := []byte(`{"a":1}`)
	tampered := []byte(`{"b":2}`)
	if len(tampered) != len(body) || !json.Valid(tampered) {
		t.Fatal("invalid same-length JSON fixture")
	}
	envelope, err := s.Put(testEnvelope(body), body)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.blobPath(envelope.Digest), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Read(envelope); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("same-length valid JSON tamper error = %v, want digest mismatch", err)
	}
}

// Dropping read-time verification would let a modified blob become trusted tool context.
func TestEvidencePutReadAndTamperChecks(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	body := []byte(`{"path":"src/a.ts","text":"hello"}`)
	envelope, err := s.Put(testEnvelope(body), body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(envelope)
	if err != nil || string(got) != string(body) {
		t.Fatalf("Read() = %q, %v", got, err)
	}

	wrongLength := envelope
	wrongLength.Bytes++
	if _, err := s.Read(wrongLength); err == nil {
		t.Fatal("wrong envelope length accepted")
	}
	wrongHash := envelope
	wrongHash.Digest = strings.Repeat("b", 64)
	if _, err := s.Read(wrongHash); err == nil {
		t.Fatal("wrong envelope digest accepted")
	}
	if err := os.WriteFile(s.blobPath(envelope.Digest), []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(envelope); err == nil {
		t.Fatal("tampered evidence accepted")
	}
}

// Missing files and symlinks must be errors, not empty successful evidence.
func TestEvidenceReadRejectsMissingFileAndSymlink(t *testing.T) {
	s := New(t.TempDir())
	body := []byte(`{"ok":true}`)
	envelope := testEnvelope(body)
	if _, err := s.Read(envelope); err == nil {
		t.Fatal("missing blob read as empty evidence")
	}
	path := s.blobPath(envelope.Digest)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(envelope); err == nil {
		t.Fatal("symlink evidence accepted")
	}
}

// A symlinked digest directory must not redirect controller evidence outside its root.
func TestEvidencePutRejectsSymlinkedBlobDirectory(t *testing.T) {
	root := t.TempDir()
	s := New(root)
	body := []byte(`{"ok":true}`)
	envelope := testEnvelope(body)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "blobs", envelope.Digest[:2])); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(envelope, body); err == nil {
		t.Fatal("symlinked blob directory accepted")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("evidence escaped root: entries=%v err=%v", entries, err)
	}
}

// Size and UTF-8 checks must happen before publication.
func TestEvidencePutRejectsOversizedAndInvalidText(t *testing.T) {
	s := New(t.TempDir())
	tooLarge := []byte(strings.Repeat("x", (64<<10)+1))
	if _, err := s.Put(testEnvelope(tooLarge), tooLarge); err == nil {
		t.Fatal("oversized repository evidence accepted")
	}
	invalid := []byte{0xff}
	if _, err := s.Put(testEnvelope(invalid), invalid); err == nil {
		t.Fatal("invalid UTF-8 evidence accepted")
	}
}

// A crash before rename may leave staging debris but must never publish authority.
func TestEvidencePublicationInterruptionLeavesNoPublishedBlob(t *testing.T) {
	s := New(t.TempDir())
	body := []byte(`{"ok":true}`)
	envelope := testEnvelope(body)
	want := errors.New("fixture interruption")
	s.beforePublish = func() error { return want }
	if _, err := s.Put(envelope, body); !errors.Is(err, want) {
		t.Fatalf("Put() error = %v, want interruption", err)
	}
	if _, err := os.Lstat(s.blobPath(envelope.Digest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted blob was published: %v", err)
	}
}

// Directory entries must be synchronized from each containing directory
// before Put reports an envelope that SQL may reference.
func TestEvidencePutSynchronizesDirectoryEntriesBeforePublication(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "evidence")
	s := New(root)
	var synced []string
	s.syncDir = func(path string) error {
		synced = append(synced, path)
		return nil
	}
	body := []byte(`{"ok":true}`)
	envelope := testEnvelope(body)
	if _, err := s.Put(envelope, body); err != nil {
		t.Fatal(err)
	}
	want := []string{
		parent,
		root,
		root,
		filepath.Join(root, "blobs"),
		filepath.Join(root, "blobs", envelope.Digest[:2]),
		parent,
	}
	if !reflect.DeepEqual(synced, want) {
		t.Fatalf("directory sync order = %v, want %v", synced, want)
	}
}

// A directory left behind by a failed parent sync is not durable evidence;
// the next explicit Put must retry that same sync before publication.
func TestEvidencePutRetriesSyncForExistingIntermediateDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "evidence")
	s := New(root)
	body := []byte(`{"ok":true}`)
	envelope := testEnvelope(body)
	failPath := filepath.Join(root, "blobs")
	failOnce := true
	s.syncDir = func(path string) error {
		if path == failPath && failOnce {
			failOnce = false
			return errors.New("fixture directory sync failure")
		}
		return nil
	}
	if _, err := s.Put(envelope, body); err == nil || !strings.Contains(err.Error(), "fixture directory sync failure") {
		t.Fatalf("first Put error = %v, want sync failure", err)
	}
	if _, err := os.Lstat(filepath.Join(failPath, envelope.Digest[:2])); err != nil {
		t.Fatalf("failed sync fixture did not leave the created directory: %v", err)
	}
	if _, err := os.Lstat(s.blobPath(envelope.Digest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("evidence published before directory sync retry: %v", err)
	}
	if _, err := s.Put(envelope, body); err != nil {
		t.Fatalf("retry Put() = %v", err)
	}
}

// Publication can create the final name before the leaf-directory sync
// fails. A retry must sync that leaf even though the valid blob now exists.
func TestEvidencePutRetriesLeafSyncForExistingPublishedBlob(t *testing.T) {
	root := filepath.Join(t.TempDir(), "evidence")
	s := New(root)
	body := []byte(`{"ok":true}`)
	envelope := testEnvelope(body)
	leaf := filepath.Join(root, "blobs", envelope.Digest[:2])
	failOnce := true
	s.syncDir = func(path string) error {
		if path == leaf && failOnce {
			failOnce = false
			return errors.New("fixture leaf sync failure")
		}
		return nil
	}
	if _, err := s.Put(envelope, body); err == nil || !strings.Contains(err.Error(), "fixture leaf sync failure") {
		t.Fatalf("first Put error = %v, want leaf sync failure", err)
	}
	if _, err := os.Lstat(s.blobPath(envelope.Digest)); err != nil {
		t.Fatalf("fixture needs an existing published blob: %v", err)
	}
	leafSyncs := 0
	s.syncDir = func(path string) error {
		if path == leaf {
			leafSyncs++
		}
		return nil
	}
	if _, err := s.Put(envelope, body); err != nil {
		t.Fatalf("retry Put() = %v", err)
	}
	if leafSyncs != 1 {
		t.Fatalf("retry leaf syncs = %d, want 1", leafSyncs)
	}
}
