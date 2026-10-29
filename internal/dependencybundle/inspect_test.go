package dependencybundle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInspectFrozenSnapshotAndDrift(t *testing.T) {
	_, src := dirs(t)
	put(t, src, "lib/a", []byte("abc"))
	var held []byte
	m, e := InspectFrozen(context.Background(), src, "node", sum([]byte("lock")), func(f File, b []byte) error {
		if f.Path != "lib/a" || string(b) != "abc" {
			t.Fatalf("snapshot %+v %q", f, b)
		}
		held = b
		return nil
	})
	if e != nil || len(m.Files) != 1 || m.Files[0].SHA256 != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("manifest %+v %v", m, e)
	}
	held[0] = 'z'
	if m.Files[0].SHA256 != sum([]byte("abc")) {
		t.Fatal("callback owns manifest bytes")
	}
	_, e = InspectFrozen(context.Background(), src, "node", sum(nil), func(File, []byte) error { put(t, src, "lib/a", []byte("xyz")); return nil })
	if e == nil {
		t.Fatal("accepted callback-time source mutation")
	}
}
func TestInspectFrozenRejectsBeforeCallback(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "executable", "oversize", "empty-directory", "alias"} {
		t.Run(kind, func(t *testing.T) {
			_, src := dirs(t)
			put(t, src, "a", []byte("abc"))
			switch kind {
			case "symlink":
				os.Remove(filepath.Join(src, "a"))
				if e := os.Symlink("missing", filepath.Join(src, "a")); e != nil {
					t.Skip(e)
				}
			case "hardlink":
				if e := os.Link(filepath.Join(src, "a"), filepath.Join(t.TempDir(), "other")); e != nil {
					t.Skip(e)
				}
			case "executable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix mode")
				}
				os.Chmod(filepath.Join(src, "a"), 0755)
			case "oversize":
				os.Truncate(filepath.Join(src, "a"), MaxFileBytes+1)
			case "empty-directory":
				os.Mkdir(filepath.Join(src, "empty"), 0700)
			case "alias":
				put(t, src, "A", []byte("abc"))
				if es, _ := os.ReadDir(src); len(es) != 2 {
					t.Skip("case folding filesystem")
				}
			}
			calls := 0
			_, e := InspectFrozen(context.Background(), src, "node", sum(nil), func(File, []byte) error { calls++; return nil })
			if e == nil {
				t.Fatal("accepted invalid source")
			}
			if kind != "empty-directory" && calls != 0 {
				t.Fatalf("callback before rejection: %d", calls)
			}
		})
	}
}
func TestInspectFrozenCancellationAndCallbackError(t *testing.T) {
	_, src := dirs(t)
	put(t, src, "a", []byte("abc"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := InspectFrozen(ctx, src, "node", sum(nil), nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	sentinel := errors.New("inspection rejected")
	if _, e := InspectFrozen(context.Background(), src, "node", sum(nil), func(File, []byte) error { return sentinel }); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
}

func TestInspectFrozenCountAndPayloadLimits(t *testing.T) {
	for _, kind := range []string{"files", "payload"} {
		t.Run(kind, func(t *testing.T) {
			_, src := dirs(t)
			if kind == "files" {
				for i := 0; i < MaxFiles+1; i++ {
					put(t, src, fmt.Sprint(i), nil)
				}
			} else {
				for i := 0; i < 9; i++ {
					p := fmt.Sprint(i)
					put(t, src, p, nil)
					if e := os.Truncate(filepath.Join(src, p), MaxFileBytes); e != nil {
						t.Fatal(e)
					}
				}
			}
			calls := 0
			if _, e := InspectFrozen(context.Background(), src, "node", sum(nil), func(File, []byte) error { calls++; return nil }); e == nil || calls != 0 {
				t.Fatalf("accepted limit %d %v", calls, e)
			}
		})
	}
}
func TestInspectFrozenDetectsAddedFileAndReplacedRoot(t *testing.T) {
	for _, kind := range []string{"added", "root", "same-inode-restored-time"} {
		t.Run(kind, func(t *testing.T) {
			_, src := dirs(t)
			put(t, src, "a", []byte("abc"))
			before, _ := os.Stat(filepath.Join(src, "a"))
			_, e := InspectFrozen(context.Background(), src, "node", sum(nil), func(File, []byte) error {
				switch kind {
				case "added":
					put(t, src, "b", []byte("x"))
				case "root":
					if e := os.Rename(src, src+"-old"); e != nil {
						t.Skip(e)
					}
					os.Mkdir(src, 0700)
					put(t, src, "a", []byte("abc"))
				case "same-inode-restored-time":
					put(t, src, "a", []byte("xyz"))
					os.Chtimes(filepath.Join(src, "a"), before.ModTime(), before.ModTime())
				}
				return nil
			})
			if e == nil {
				t.Fatal("accepted drift")
			}
		})
	}
}

func TestInspectFrozenPayloadExactBounds(t *testing.T) {
	_, src := dirs(t)
	for i := 0; i < 8; i++ {
		name := fmt.Sprint(i)
		put(t, src, name, nil)
		if e := os.Truncate(filepath.Join(src, name), MaxFileBytes); e != nil {
			t.Fatal(e)
		}
	}
	count := 0
	m, e := InspectFrozen(context.Background(), src, "node", sum(nil), func(f File, b []byte) error {
		if f.Size != MaxFileBytes || int64(len(b)) != MaxFileBytes {
			t.Fatal("file bound")
		}
		count++
		return nil
	})
	if e != nil || len(m.Files) != 8 || count != 8 {
		t.Fatalf("exact payload bound %d %v", count, e)
	}
}
func TestInspectFrozenRejectsPathBounds(t *testing.T) {
	paths := []string{strings.Repeat("a", 129), strings.Repeat("d/", 32) + "a"}
	if runtime.GOOS != "windows" {
		// Windows materializes these spellings as aliases/devices, not literal
		// directory entries. Manifest tests reject the spellings on every host;
		// native Windows tests separately check the actual enumerated namespace.
		paths = append(paths, "bad:name", "AUX.txt", "trailing.")
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			_, src := dirs(t)
			writePath := filepath.Join(src, filepath.FromSlash(p))
			if e := os.MkdirAll(filepath.Dir(writePath), 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(writePath, nil, 0644); e != nil {
				t.Fatal(e)
			}
			if _, e := InspectFrozen(context.Background(), src, "node", sum(nil), nil); e == nil {
				t.Fatalf("accepted path %q", p)
			}
		})
	}
}

func TestInspectFrozenManifestBoundBeforeSnapshots(t *testing.T) {
	_, src := dirs(t)
	prefix := strings.Repeat("a", 120) + "/" + strings.Repeat("b", 120) + "/" + strings.Repeat("c", 120) + "/"
	for i := 0; i < 10000; i++ {
		put(t, src, prefix+fmt.Sprint(i), nil)
	}
	calls := 0
	if _, e := InspectFrozen(context.Background(), src, "node", sum(nil), func(File, []byte) error { calls++; return nil }); e == nil || calls != 0 {
		t.Fatalf("manifest bound after %d snapshots: %v", calls, e)
	}
}

func TestInspectFrozenManifestRejectsLaterImportDrift(t *testing.T) {
	store, src := dirs(t)
	put(t, src, "a", []byte("abc"))
	m, e := InspectFrozen(context.Background(), src, "node", sum([]byte("exact lock")), nil)
	if e != nil {
		t.Fatal(e)
	}
	put(t, src, "a", []byte("xyz"))
	if _, e = ImportFrozen(context.Background(), store, src, m); e == nil {
		t.Fatal("published changed source with inspected manifest")
	}
	requireAbsent(t, store)
}
