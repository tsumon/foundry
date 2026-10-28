package dependencybundle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"foundry/internal/platformfs"
	"foundry/internal/testutil"
)

func dirs(t *testing.T) (string, string) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(base, "store")
	source := filepath.Join(base, "source")
	for _, p := range []string{root, source} {
		if e := platformfs.PrivateDirectory(p); e != nil {
			t.Fatal(e)
		}
	}
	return root, source
}
func put(t *testing.T, base, p string, b []byte) {
	t.Helper()
	full := filepath.Join(base, filepath.FromSlash(p))
	if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, b, 0644); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (string, string, Manifest) {
	t.Helper()
	root, src := dirs(t)
	put(t, src, "lib/a", []byte("abc"))
	return root, src, manifest("node")
}
func requireAbsent(t *testing.T, root string) {
	t.Helper()
	es, e := os.ReadDir(root)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range es {
		if entry.Name() != "sentinel" {
			t.Fatalf("unexpected residual %q", entry.Name())
		}
	}
}

func TestImportVerifyReopenAndIdempotentBothEcosystems(t *testing.T) {
	for _, eco := range []string{"node", "python"} {
		t.Run(eco, func(t *testing.T) {
			root, src, m := fixture(t)
			m.Ecosystem = eco
			b, e := ImportFrozen(context.Background(), root, src, m)
			if e != nil {
				t.Fatal(e)
			}
			if e = Verify(context.Background(), root, b); e != nil {
				t.Fatal(e)
			}
			var got []byte
			e = ReadVerified(context.Background(), root, b, func(f File, data []byte) error {
				if f.Path != "lib/a" || f.Size != 3 {
					t.Fatalf("metadata %+v", f)
				}
				got = append(got, data...)
				return nil
			})
			if e != nil || !bytes.Equal(got, []byte("abc")) {
				t.Fatalf("read %q %v", got, e)
			}
			again, e := ImportFrozen(context.Background(), root, src, m)
			if e != nil || again != b {
				t.Fatalf("idempotency %+v %v", again, e)
			}
			info, e := os.Stat(filepath.Join(root, b.BundleDigest, "payload", "lib", "a"))
			if e != nil {
				t.Fatal(e)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0644 {
				t.Fatalf("payload mode %v", info.Mode())
			}
		})
	}
}
func TestImportEmpty(t *testing.T) {
	for _, eco := range []string{"node", "python"} {
		root, src := dirs(t)
		b, e := ImportFrozen(context.Background(), root, src, Empty(eco))
		if e != nil {
			t.Fatal(e)
		}
		if e = Verify(context.Background(), root, b); e != nil {
			t.Fatal(e)
		}
		calls := 0
		if e = ReadVerified(context.Background(), root, b, func(File, []byte) error { calls++; return nil }); e != nil || calls != 0 {
			t.Fatalf("empty read %d %v", calls, e)
		}
	}
}
func TestBindingMutationCannotVerify(t *testing.T) {
	root, src, m := fixture(t)
	b, e := ImportFrozen(context.Background(), root, src, m)
	if e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Binding){func(b *Binding) { b.SchemaVersion++ }, func(b *Binding) { b.Ecosystem = "python" }, func(b *Binding) { b.BundleDigest = digest(nil) }, func(b *Binding) { b.ManifestDigest = digest(nil) }, func(b *Binding) { b.FileCount++ }, func(b *Binding) { b.TotalBytes++ }} {
		bad := b
		change(&bad)
		if e := Verify(context.Background(), root, bad); e == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
func TestFailedImportsPreserveUnrelatedSentinel(t *testing.T) {
	for _, kind := range []string{"hash", "size", "extra file", "extra empty directory", "missing", "executable", "hardlink", "symlink", "directory symlink", "special", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			root, src, m := fixture(t)
			put(t, root, "sentinel", []byte("safe"))
			ctx := context.Background()
			switch kind {
			case "hash":
				m.Files[0].SHA256 = digest([]byte("secret-sentinel"))
			case "size":
				m.Files[0].Size++
			case "extra file":
				put(t, src, "unexpected", []byte("secret-sentinel"))
			case "extra empty directory":
				if e := os.Mkdir(filepath.Join(src, "empty"), 0700); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e := os.Remove(filepath.Join(src, "lib", "a")); e != nil {
					t.Fatal(e)
				}
			case "executable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix executable mode")
				}
				if e := os.Chmod(filepath.Join(src, "lib", "a"), 0755); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(filepath.Join(src, "lib", "a"), filepath.Join(t.TempDir(), "outside")); e != nil {
					t.Skip(e)
				}
			case "symlink":
				if e := os.Remove(filepath.Join(src, "lib", "a")); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Join(root, "sentinel"), filepath.Join(src, "lib", "a")); e != nil {
					t.Skip(e)
				}
			case "directory symlink":
				if e := os.RemoveAll(filepath.Join(src, "lib")); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(root, filepath.Join(src, "lib")); e != nil {
					t.Skip(e)
				}
			case "special":
				if e := os.Remove(filepath.Join(src, "lib", "a")); e != nil {
					t.Fatal(e)
				}
				if e := testutil.SpecialFile(filepath.Join(src, "lib", "a")); e != nil {
					t.Skip(e)
				}
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			_, e := ImportFrozen(ctx, root, src, m)
			if e == nil {
				t.Fatal("accepted invalid source")
			}
			if strings.Contains(e.Error(), "secret-sentinel") {
				t.Fatal("content leaked")
			}
			requireAbsent(t, root)
			data, e := os.ReadFile(filepath.Join(root, "sentinel"))
			if e != nil || string(data) != "safe" {
				t.Fatal("sentinel changed")
			}
		})
	}
}
func TestCorruptExistingBundleNeverReplaced(t *testing.T) {
	for _, kind := range []string{"payload", "extra file", "extra dir", "manifest", "unknown manifest key", "mode", "hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, src, m := fixture(t)
			b, e := ImportFrozen(context.Background(), root, src, m)
			if e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(root, b.BundleDigest)
			switch kind {
			case "payload":
				put(t, dir, "payload/lib/a", []byte("xyz"))
			case "extra file":
				put(t, dir, "payload/extra", []byte("bad"))
			case "extra dir":
				if e := os.Mkdir(filepath.Join(dir, "empty"), 0700); e != nil {
					t.Fatal(e)
				}
			case "manifest":
				put(t, dir, "manifest.json", []byte(`{}`))
			case "unknown manifest key":
				raw, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
				if e != nil {
					t.Fatal(e)
				}
				put(t, dir, "manifest.json", append([]byte(`{"unknown":0,`), raw[1:]...))
			case "mode":
				if runtime.GOOS == "windows" {
					t.Skip("Unix mode")
				}
				if e := os.Chmod(filepath.Join(dir, "payload/lib/a"), 0755); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(filepath.Join(dir, "payload/lib/a"), filepath.Join(t.TempDir(), "link")); e != nil {
					t.Skip(e)
				}
			case "symlink":
				if e := os.Remove(filepath.Join(dir, "payload/lib/a")); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(filepath.Join(src, "lib/a"), filepath.Join(dir, "payload/lib/a")); e != nil {
					t.Skip(e)
				}
			}
			if e = Verify(context.Background(), root, b); e == nil {
				t.Fatal("verified corruption")
			}
			before, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if _, e = ImportFrozen(context.Background(), root, src, m); e == nil {
				t.Fatal("replaced corrupt destination")
			}
			after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("existing data replaced")
			}
		})
	}
}
func TestConcurrentIdenticalImports(t *testing.T) {
	root, src, m := fixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, e := ImportFrozen(context.Background(), root, src, m)
			if e == nil {
				e = Verify(context.Background(), root, b)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 1 {
		t.Fatalf("residual %v %v", entries, e)
	}
}
func TestTransferRevalidatesAndPropagatesCallbackFailure(t *testing.T) {
	root, src, m := fixture(t)
	b, e := ImportFrozen(context.Background(), root, src, m)
	if e != nil {
		t.Fatal(e)
	}
	want := errors.New("sink failure")
	if e = ReadVerified(context.Background(), root, b, func(File, []byte) error { return want }); !errors.Is(e, want) {
		t.Fatalf("callback error %v", e)
	}
	put(t, root, b.BundleDigest+"/payload/lib/a", []byte("bad"))
	called := false
	if e = ReadVerified(context.Background(), root, b, func(File, []byte) error { called = true; return nil }); e == nil || called {
		t.Fatalf("unverified emission %v %v", called, e)
	}
}
func TestUnsafeRootSourceAndTraversalCannotEscape(t *testing.T) {
	root, src, m := fixture(t)
	outside := t.TempDir()
	put(t, outside, "sentinel", []byte("safe"))
	m.Files[0].Path = "../sentinel"
	if _, e := ImportFrozen(context.Background(), root, src, m); e == nil {
		t.Fatal("accepted traversal")
	}
	m = manifest("node")
	link := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(src, link); e != nil {
		t.Skip(e)
	}
	if _, e := ImportFrozen(context.Background(), root, link, m); e == nil {
		t.Fatal("accepted source symlink")
	}
	outlink := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(root, outlink); e != nil {
		t.Fatal(e)
	}
	if _, e := ImportFrozen(context.Background(), outlink, src, m); e == nil {
		t.Fatal("accepted store symlink")
	}
	data, e := os.ReadFile(filepath.Join(outside, "sentinel"))
	if e != nil || string(data) != "safe" {
		t.Fatal("outside changed")
	}
	requireAbsent(t, root)
}

// A context checkpoint is a deterministic scheduling point in the real copy
// loop; it lets these tests mutate real files without production-only hooks.
type checkpointContext struct {
	context.Context
	check func() error
}

func (c checkpointContext) Err() error { return c.check() }
func TestCancellationDuringCopyRemovesOnlyOwnedStage(t *testing.T) {
	root, src, m := fixture(t)
	put(t, root, "sentinel", []byte("safe"))
	large := bytes.Repeat([]byte{0, 1, 255}, 40000)
	put(t, src, "lib/a", large)
	m.Files[0].Size = int64(len(large))
	m.Files[0].SHA256 = digest(large)
	canceled := false
	ctx := checkpointContext{context.Background(), func() error {
		entries, e := os.ReadDir(root)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".partial-") {
				info, e := os.Stat(filepath.Join(root, entry.Name(), "payload/lib/a"))
				if e == nil && info.Size() > 0 {
					canceled = true
					return context.Canceled
				}
			}
		}
		return nil
	}}
	if _, e := ImportFrozen(ctx, root, src, m); !errors.Is(e, context.Canceled) || !canceled {
		t.Fatalf("canceled=%v error=%v", canceled, e)
	}
	requireAbsent(t, root)
}
func TestSourceMutationBeforePublicationRejectsSnapshot(t *testing.T) {
	for _, kind := range []string{"content", "replacement", "inventory"} {
		t.Run(kind, func(t *testing.T) {
			root, src, m := fixture(t)
			mutated := false
			ctx := checkpointContext{context.Background(), func() error {
				entries, e := os.ReadDir(root)
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".partial-") {
						info, e := os.Stat(filepath.Join(root, entry.Name(), "payload/lib/a"))
						if !mutated && e == nil && info.Size() == 3 {
							mutated = true
							switch kind {
							case "content":
								put(t, src, "lib/a", []byte("xyz"))
							case "replacement":
								if e := os.Remove(filepath.Join(src, "lib/a")); e != nil {
									t.Fatal(e)
								}
								put(t, src, "lib/a", []byte("abc"))
							case "inventory":
								put(t, src, "unlisted", nil)
							}
						}
					}
				}
				return nil
			}}
			if _, e := ImportFrozen(ctx, root, src, m); e == nil || !mutated {
				t.Fatalf("mutated=%v error=%v", mutated, e)
			}
			requireAbsent(t, root)
		})
	}
}
func TestTransferHoldsVerifiedSnapshotAndRequiresCompletion(t *testing.T) {
	root, src, m := fixture(t)
	put(t, src, "lib/b", []byte{0, 255, 1})
	m.Files = append(m.Files, File{Path: "lib/b", Size: 3, SHA256: digest([]byte{0, 255, 1})})
	b, e := ImportFrozen(context.Background(), root, src, m)
	if e != nil {
		t.Fatal(e)
	}
	var first []byte
	calls := 0
	e = ReadVerified(context.Background(), root, b, func(f File, data []byte) error {
		calls++
		if f.Path == "lib/a" {
			first = data
			put(t, root, b.BundleDigest+"/payload/lib/b", []byte("bad"))
			put(t, root, b.BundleDigest+"/payload/lib/a", []byte("bad"))
		}
		return nil
	})
	if e == nil || calls != 1 || string(first) != "abc" {
		t.Fatalf("calls=%d first=%q error=%v", calls, first, e)
	}
}
func TestExistingEmptyDestinationAndSymlinkCannotBeReplaced(t *testing.T) {
	for _, kind := range []string{"empty", "link"} {
		t.Run(kind, func(t *testing.T) {
			root, src, m := fixture(t)
			b, e := m.Binding()
			if e != nil {
				t.Fatal(e)
			}
			dest := filepath.Join(root, b.BundleDigest)
			outside := t.TempDir()
			put(t, outside, "sentinel", []byte("safe"))
			if kind == "empty" {
				if e = os.Mkdir(dest, 0700); e != nil {
					t.Fatal(e)
				}
			} else {
				if e = os.Symlink(outside, dest); e != nil {
					t.Skip(e)
				}
			}
			before, e := os.Lstat(dest)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ImportFrozen(context.Background(), root, src, m); e == nil {
				t.Fatal("replaced existing destination")
			}
			after, e := os.Lstat(dest)
			if e != nil || !os.SameFile(before, after) {
				t.Fatal("destination replaced")
			}
			data, e := os.ReadFile(filepath.Join(outside, "sentinel"))
			if e != nil || string(data) != "safe" {
				t.Fatal("outside changed")
			}
		})
	}
}
func TestSourceAndOutputAncestorSymlinksRejected(t *testing.T) {
	root, src, m := fixture(t)
	parent := t.TempDir()
	link := filepath.Join(parent, "parent")
	if e := os.Symlink(filepath.Dir(src), link); e != nil {
		t.Skip(e)
	}
	if _, e := ImportFrozen(context.Background(), root, filepath.Join(link, filepath.Base(src)), m); e == nil {
		t.Fatal("accepted source ancestor link")
	}
	if _, e := ImportFrozen(context.Background(), filepath.Join(link, filepath.Base(root)), src, m); e == nil {
		t.Fatal("accepted output ancestor link")
	}
	requireAbsent(t, root)
}
func TestStoredNoncanonicalPermissionsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions; Windows uses ACL validation")
	}
	for _, p := range []string{"manifest.json", "payload/lib/a", "payload", "payload/lib"} {
		t.Run(p, func(t *testing.T) {
			root, src, m := fixture(t)
			b, e := ImportFrozen(context.Background(), root, src, m)
			if e != nil {
				t.Fatal(e)
			}
			mode := os.FileMode(0600)
			if p == "payload" || p == "payload/lib" {
				mode = 0755
			}
			full := filepath.Join(root, b.BundleDigest, filepath.FromSlash(p))
			if e = os.Chmod(full, mode); e != nil {
				t.Fatal(e)
			}
			if mode == 0755 {
				defer os.Chmod(full, 0700)
			}
			if e = Verify(context.Background(), root, b); e == nil {
				t.Fatal("accepted noncanonical storage mode")
			}
		})
	}
}
func TestBinaryAndZeroLengthTransferAndModeNormalization(t *testing.T) {
	root, src := dirs(t)
	data := bytes.Repeat([]byte{0, 255, 1}, 40000)
	put(t, src, "a", data)
	put(t, src, "b", nil)
	if runtime.GOOS != "windows" {
		if e := os.Chmod(filepath.Join(src, "a"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	m := Empty("python")
	m.Files = []File{{Path: "a", Size: int64(len(data)), SHA256: digest(data)}, {Path: "b", SHA256: digest(nil)}}
	b, e := ImportFrozen(context.Background(), root, src, m)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	e = ReadVerified(context.Background(), root, b, func(f File, got []byte) error {
		calls++
		if f.Path == "a" && !bytes.Equal(got, data) || f.Path == "b" && len(got) != 0 {
			t.Fatal("wrong transfer bytes")
		}
		return nil
	})
	if e != nil || calls != 2 {
		t.Fatalf("transfer calls=%d %v", calls, e)
	}
}
func TestManifestBoundAndNoncanonicalJSONCannotVerify(t *testing.T) {
	for _, kind := range []string{"oversize", "duplicate", "alias", "null files", "trailing", "space"} {
		t.Run(kind, func(t *testing.T) {
			root, src, m := fixture(t)
			b, e := ImportFrozen(context.Background(), root, src, m)
			if e != nil {
				t.Fatal(e)
			}
			full := filepath.Join(root, b.BundleDigest, "manifest.json")
			raw, e := os.ReadFile(full)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "oversize":
				raw = bytes.Repeat([]byte{' '}, 4*1024*1024+1)
			case "duplicate":
				raw = append([]byte(`{"schema_version":1,`), raw[1:]...)
			case "alias":
				raw = bytes.Replace(raw, []byte(`"ecosystem"`), []byte(`"Ecosystem"`), 1)
			case "null files":
				raw = bytes.Replace(raw, []byte(`"files":[`), []byte(`"files":null,"unused":[`), 1)
			case "trailing":
				raw = append(raw, []byte(`{}`)...)
			case "space":
				raw = append(raw, '\n')
			}
			if e = os.WriteFile(full, raw, 0644); e != nil {
				t.Fatal(e)
			}
			if e = Verify(context.Background(), root, b); e == nil {
				t.Fatalf("accepted %s", kind)
			}
		})
	}
}
func TestCleanupFailureReportsOriginalAndPreservesUnrelatedData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions control cleanup")
	}
	root, src, m := fixture(t)
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	if e := os.WriteFile(sentinel, []byte("safe"), 0600); e != nil {
		t.Fatal(e)
	}
	var protected string
	changed := false
	ctx := checkpointContext{context.Background(), func() error {
		entries, e := os.ReadDir(root)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".partial-") {
				p := filepath.Join(root, entry.Name(), "payload/lib/a")
				if _, e := os.Stat(p); e == nil && !changed {
					protected = filepath.Join(root, entry.Name())
					if e = os.Chmod(protected, 0500); e != nil {
						t.Fatal(e)
					}
					changed = true
					return context.Canceled
				}
			}
		}
		return nil
	}}
	_, e := ImportFrozen(ctx, root, src, m)
	if protected != "" {
		defer os.Chmod(protected, 0700)
	}
	if !changed || !errors.Is(e, context.Canceled) || !errors.Is(e, ErrCleanup) {
		t.Fatalf("changed=%v error=%v", changed, e)
	}
	data, se := os.ReadFile(sentinel)
	if se != nil || string(data) != "safe" {
		t.Fatal("unrelated sentinel changed")
	}
	entries, se := os.ReadDir(root)
	if se != nil {
		t.Fatal(se)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".partial-") {
			t.Fatal("published failed import")
		}
	}
}

func TestManifestMutationAfterReadCannotVerify(t *testing.T) {
	root, src, m := fixture(t)
	b, e := ImportFrozen(context.Background(), root, src, m)
	if e != nil {
		t.Fatal(e)
	}
	changed := false
	calls := 0
	ctx := checkpointContext{context.Background(), func() error {
		calls++
		if calls == 2 {
			changed = true
			put(t, root, b.BundleDigest+"/manifest.json", []byte(`{}`))
		}
		return nil
	}}
	if e = Verify(ctx, root, b); e == nil || !changed {
		t.Fatalf("changed=%v error=%v", changed, e)
	}
}

func TestPostPublicationSyncFailurePreservesCompletedBundle(t *testing.T) {
	root, src, m := fixture(t)
	put(t, root, "sentinel", []byte("safe"))
	synced := false
	b, e := importFrozen(context.Background(), root, src, m, func(r *os.Root) error {
		synced = true
		entries, e := r.Open(".")
		if e != nil {
			t.Fatal(e)
		}
		defer entries.Close()
		names, e := entries.ReadDir(-1)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, entry := range names {
			if !strings.HasPrefix(entry.Name(), ".partial-") && entry.Name() != "sentinel" {
				found = true
			}
		}
		if !found {
			t.Fatal("sync was called before publication")
		}
		return errors.New("secret filesystem diagnostic")
	})
	if !synced || !errors.Is(e, ErrPublicationUnknown) || b.BundleDigest == "" || strings.Contains(e.Error(), "secret") {
		t.Fatalf("sync=%v binding=%+v error=%v", synced, b, e)
	}
	if e = Verify(context.Background(), root, b); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(root, "sentinel"))
	if e != nil || string(data) != "safe" {
		t.Fatal("sentinel changed")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 2 {
		t.Fatalf("residual %v %v", entries, e)
	}
}
