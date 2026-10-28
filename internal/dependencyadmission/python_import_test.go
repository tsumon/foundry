package dependencyadmission

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"foundry/internal/dependencybundle"
)

func wheelFiles(name, version, requires string) map[string][]byte {
	d := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/"
	return map[string][]byte{
		d + "WHEEL":            []byte("Wheel-Version: 1.0\nGenerator: fixture\nRoot-Is-Purelib: true\nTag: py3-none-any\n"),
		d + "METADATA":         []byte("Metadata-Version: 2.4\nName: " + name + "\nVersion: " + version + "\n" + requires + "\nDescription\n"),
		d + "licenses/LICENSE": []byte("fixture license"),
		"namespace/" + strings.ReplaceAll(name, "-", "_") + ".py": []byte("value = 1\n"),
		"namespace/data," + name + ".txt":                         []byte("resource"),
	}
}
func makeWheel(t *testing.T, name, version string, files map[string][]byte, mutateRecord func([][]string) [][]string) []byte {
	t.Helper()
	record := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/RECORD"
	keys := []string{}
	for p := range files {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	rows := [][]string{}
	for _, p := range keys {
		h := sha256.Sum256(files[p])
		rows = append(rows, []string{p, "sha256=" + base64.RawURLEncoding.EncodeToString(h[:]), fmt.Sprint(len(files[p]))})
	}
	rows = append(rows, []string{record, "", ""})
	if mutateRecord != nil {
		rows = mutateRecord(rows)
	}
	var rec bytes.Buffer
	cw := csv.NewWriter(&rec)
	cw.WriteAll(rows)
	if e := cw.Error(); e != nil {
		t.Fatal(e)
	}
	files[record] = rec.Bytes()
	keys = append(keys, record)
	sort.Strings(keys)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range keys {
		h := &zip.FileHeader{Name: p, Method: zip.Deflate}
		h.SetMode(0644)
		w, e := zw.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(files[p]); e != nil {
			t.Fatal(e)
		}
	}
	if e := zw.Close(); e != nil {
		t.Fatal(e)
	}
	return buf.Bytes()
}
func pythonFixture(t *testing.T, wheels map[string][]byte, roots []pythonRoot) (string, string, SourceBytes) {
	t.Helper()
	store, tree := setup(t)
	l := pythonLock{SchemaVersion: 1, Python: "3.13.5", SelectedExtras: []string{}, Roots: roots, Wheels: []pythonWheel{}}
	for name, b := range wheels {
		filename := strings.ReplaceAll(name, "-", "_") + "-1.0-py3-none-any.whl"
		write(t, tree, filename, b)
		l.Wheels = append(l.Wheels, pythonWheel{Name: name, Version: "1.0", Filename: filename, SHA256: sum(b), Size: int64(len(b))})
	}
	sort.Slice(l.Wheels, func(i, j int) bool { return l.Wheels[i].Name < l.Wheels[j].Name })
	return store, tree, SourceBytes{Path: "foundry.python.lock.json", Data: encode(l)}
}
func assertNoPythonStage(t *testing.T, store string) {
	t.Helper()
	entries, e := os.ReadDir(store)
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range entries {
		if strings.HasPrefix(f.Name(), ".") {
			t.Fatalf("retained stage %s", f.Name())
		}
	}
}
func TestImportPythonVerifiedClosure(t *testing.T) {
	a := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", "Requires-Dist: beta (>=1,!=1.2.*,~=1.0)\nProvides-Extra: tests\nRequires-Dist: absent >=2rc1; extra == 'tests'\nRequires-Python: >=3.13,<3.14\n"), nil)
	b := makeWheel(t, "beta", "1.0", wheelFiles("beta", "1.0", ""), nil)
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": a, "beta": b}, []pythonRoot{{"alpha", "1.0"}})
	r, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Validate(); e != nil {
		t.Fatal(e)
	}
	if r.Provenance != "user_supplied_wheels" || len(r.Packages) != 2 || len(r.Edges) != 3 || r.SourceFiles[0].SHA256 != sum(lock.Data) {
		t.Fatalf("receipt: %+v", r)
	}
	if r.Packages[0].ArtifactSHA256 != sum(a) || r.Packages[0].ArtifactSize != int64(len(a)) {
		t.Fatal("archive provenance lost")
	}
	got := map[string][]byte{}
	if e = dependencybundle.ReadVerified(context.Background(), store, r.Bundle, func(f dependencybundle.File, b []byte) error { got[f.Path] = b; return nil }); e != nil {
		t.Fatal(e)
	}
	if string(got["namespace/data,alpha.txt"]) != "resource" || string(got["beta-1.0.dist-info/licenses/LICENSE"]) != "fixture license" {
		t.Fatal("resource/license lost")
	}
	assertNoPythonStage(t, store)
	again, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil || again.Digest != r.Digest {
		t.Fatalf("repeat: %v", e)
	}
}
func TestImportPythonEmptyBindsExactLock(t *testing.T) {
	store, tree, lock := pythonFixture(t, map[string][]byte{}, []pythonRoot{})
	r, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil {
		t.Fatal(e)
	}
	m := dependencybundle.Empty("python")
	m.LockSHA256 = sum(lock.Data)
	want, _ := m.Binding()
	if r.Bundle != want || r.Packages == nil || r.Edges == nil {
		t.Fatalf("empty: %+v", r)
	}
	assertNoPythonStage(t, store)
}
func TestImportPythonRejectsUnverifiedWheel(t *testing.T) {
	cases := map[string]func(map[string][]byte){
		"traversal":     func(f map[string][]byte) { f["../escape.py"] = []byte("x") },
		"case alias":    func(f map[string][]byte) { f["Namespace/other.py"] = []byte("x") },
		"native suffix": func(f map[string][]byte) { f["module.pyd"] = []byte("x") },
		"native magic":  func(f map[string][]byte) { f["data"] = []byte("\x7fELFpayload") },
		"pth":           func(f map[string][]byte) { f["evil.pth"] = []byte("x") },
		"entry points":  func(f map[string][]byte) { f["alpha-1.0.dist-info/entry_points.txt"] = []byte("x") },
		"data tree":     func(f map[string][]byte) { f["alpha-1.0.data/purelib/a.py"] = []byte("x") },
		"bytecode":      func(f map[string][]byte) { f["a.pyc"] = []byte("x") },
		"customize":     func(f map[string][]byte) { f["sitecustomize.py"] = []byte("x") },
		"archive":       func(f map[string][]byte) { f["payload.zip"] = []byte("x") },
		"metadata identity": func(f map[string][]byte) {
			f["alpha-1.0.dist-info/METADATA"] = []byte("Metadata-Version: 2.4\nName: other\nVersion: 1.0\n")
		},
		"metadata case duplicate": func(f map[string][]byte) {
			f["alpha-1.0.dist-info/METADATA"] = []byte("Metadata-Version: 2.4\nName: alpha\nname: alpha\nVersion: 1.0\n")
		},
		"missing dependency": func(f map[string][]byte) {
			f["alpha-1.0.dist-info/METADATA"] = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nRequires-Dist: missing\n")
		},
		"marker": func(f map[string][]byte) {
			f["alpha-1.0.dist-info/METADATA"] = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nRequires-Dist: missing; python_version > '3'\n")
		},
		"python mismatch": func(f map[string][]byte) {
			f["alpha-1.0.dist-info/METADATA"] = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nRequires-Python: <3\n")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := wheelFiles("alpha", "1.0", "")
			mutate(f)
			b := makeWheel(t, "alpha", "1.0", f, nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			r, e := ImportPython(context.Background(), store, tree, lock)
			if e == nil || r.Digest != "" {
				t.Fatalf("accepted %s", name)
			}
			assertNoPythonStage(t, store)
		})
	}
	for name, mutate := range map[string]func([][]string) [][]string{
		"missing":          func(r [][]string) [][]string { return r[1:] },
		"duplicate":        func(r [][]string) [][]string { return append(r, r[0]) },
		"wrong hash":       func(r [][]string) [][]string { r[0][1] = "sha256=" + strings.Repeat("A", 43); return r },
		"wrong size":       func(r [][]string) [][]string { r[0][2] = "0001"; return r },
		"record self hash": func(r [][]string) [][]string { r[len(r)-1][2] = "0"; return r },
	} {
		t.Run("record "+name, func(t *testing.T) {
			b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), mutate)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("accepted RECORD mismatch")
			}
			assertNoPythonStage(t, store)
		})
	}
}
func TestImportPythonFlatDirectory(t *testing.T) {
	for _, kind := range []string{"extra", "directory", "link", "hash", "size", "unreachable", "root version"} {
		t.Run(kind, func(t *testing.T) {
			b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			switch kind {
			case "extra":
				write(t, tree, "other", nil)
			case "directory":
				if e := os.Mkdir(filepath.Join(tree, "other"), 0700); e != nil {
					t.Fatal(e)
				}
			case "link":
				p := filepath.Join(tree, "alpha-1.0-py3-none-any.whl")
				if e := os.Rename(p, p+".orig"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(p+".orig", p); e != nil {
					t.Skip(e)
				}
			case "hash":
				lock.Data = bytes.Replace(lock.Data, []byte(sum(b)), []byte(strings.Repeat("0", 64)), 1)
			case "size":
				write(t, tree, "alpha-1.0-py3-none-any.whl", append(b, 0))
			case "unreachable":
				lock.Data = bytes.Replace(lock.Data, []byte(`"roots":[{"name":"alpha","version":"1.0"}]`), []byte(`"roots":[]`), 1)
			case "root version":
				lock.Data = bytes.Replace(lock.Data, []byte(`"roots":[{"name":"alpha","version":"1.0"}]`), []byte(`"roots":[{"name":"alpha","version":"2.0"}]`), 1)
			}
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("accepted invalid inputs")
			}
			assertNoPythonStage(t, store)
		})
	}
}

func TestPythonZIPLocalHeaderAgreement(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	// archive/zip uses central names for extraction. Our admitted subset also
	// rejects inconsistent local names, flags and methods in the supplied archive.
	for _, kind := range []string{"name", "flags", "method"} {
		t.Run(kind, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			switch kind {
			case "name":
				copy(bad[30:35], "../xx")
			case "flags":
				bad[6] |= 1
			case "method":
				bad[8] = 99
			}
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": bad}, []pythonRoot{{"alpha", "1.0"}})
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("accepted inconsistent local ZIP header")
			}
		})
	}
}
func TestPythonBytecodeMagic(t *testing.T) {
	f := wheelFiles("alpha", "1.0", "")
	f["data.bin"] = []byte{0xf3, 0x0d, 0x0d, 0x0a, 0, 0, 0, 0}
	b := makeWheel(t, "alpha", "1.0", f, nil)
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
		t.Fatal("accepted disguised bytecode")
	}
}

type pythonCheckpointContext struct {
	context.Context
	checkpoint func() error
}

func (c pythonCheckpointContext) Err() error { return c.checkpoint() }
func TestPythonLateInputDriftAndCancelCleanup(t *testing.T) {
	for _, kind := range []string{"drift", "extra", "cancel", "staging drift"} {
		t.Run(kind, func(t *testing.T) {
			b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			changed := false
			ctx := pythonCheckpointContext{context.Background(), func() error {
				if changed {
					return nil
				}
				entries, e := os.ReadDir(store)
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".python-admission-") {
						stage := filepath.Join(store, entry.Name())
						content, e := os.ReadDir(stage)
						if e != nil {
							t.Fatal(e)
						}
						if len(content) == 0 {
							continue
						}
						changed = true
						switch kind {
						case "drift":
							write(t, tree, "alpha-1.0-py3-none-any.whl", append([]byte("x"), b[1:]...))
						case "extra":
							write(t, tree, "extra", nil)
						case "cancel":
							return context.Canceled
						case "staging drift":
							write(t, stage, "unexpected", []byte("x"))
						}
						break
					}
				}
				return nil
			}}
			r, e := ImportPython(ctx, store, tree, lock)
			if !changed || e == nil || r.Digest != "" {
				t.Fatalf("changed=%v error=%v receipt=%+v", changed, e, r)
			}
			assertNoPythonStage(t, store)
			entries, e := os.ReadDir(store)
			if e != nil || len(entries) != 0 {
				t.Fatalf("published on failure: %v %v", entries, e)
			}
		})
	}
}
func TestPythonCleanupFailureIsNonAuthorizing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix cleanup permission fault")
	}
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	retained := ""
	defer func() {
		if retained != "" {
			os.Chmod(retained, 0700)
			os.RemoveAll(retained)
		}
	}()
	ctx := pythonCheckpointContext{context.Background(), func() error {
		if retained != "" {
			return nil
		}
		entries, e := os.ReadDir(store)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".python-admission-") {
				p := filepath.Join(store, entry.Name())
				children, e := os.ReadDir(p)
				if e != nil {
					t.Fatal(e)
				}
				if len(children) > 0 {
					if e = os.Chmod(p, 0500); e != nil {
						t.Fatal(e)
					}
					retained = p
					return context.Canceled
				}
			}
		}
		return nil
	}}
	r, e := ImportPython(ctx, store, tree, lock)
	if retained == "" || !errors.Is(e, dependencybundle.ErrCleanup) || !errors.Is(e, context.Canceled) || r.Digest != "" {
		t.Fatalf("retained=%q error=%v receipt=%+v", retained, e, r)
	}
	var cleanup *pythonCleanupError
	if !errors.As(e, &cleanup) || cleanup.retainedPath != retained || strings.Contains(e.Error(), retained) {
		t.Fatal("cleanup diagnostic must be internal only")
	}
}
func TestPythonReceiptTampering(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", "Provides-Extra: docs\nRequires-Dist: absent >=1rc1; extra == 'docs'\n"), nil)
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	r, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil {
		t.Fatal(e)
	}
	var expectedRecord string
	if e = dependencybundle.ReadVerified(context.Background(), store, r.Bundle, func(f dependencybundle.File, b []byte) error {
		if strings.HasSuffix(f.Path, "/RECORD") {
			expectedRecord = sum(b)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if r.Packages[0].RecordSHA256 != expectedRecord {
		t.Fatal("RECORD provenance absent")
	}
	for _, mutate := range []func(*AdmissionReceipt){
		func(r *AdmissionReceipt) { r.Packages[0].RecordSHA256 = "" },
		func(r *AdmissionReceipt) { r.Packages[0].RecordSHA256 = strings.Repeat("0", 64) },
		func(r *AdmissionReceipt) { r.Packages[0].ArtifactClaim = "publisher_verified" },
		func(r *AdmissionReceipt) { r.Packages[0].ArtifactSHA256 = "" },
		func(r *AdmissionReceipt) { r.Edges[1].To = r.Packages[0].Location },
		func(r *AdmissionReceipt) { r.Edges = r.Edges[1:] },
		func(r *AdmissionReceipt) { r.SourceFiles[0].Path = "elsewhere" },
		func(r *AdmissionReceipt) { r.Bundle.Ecosystem = "node" },
	} {
		var bad AdmissionReceipt
		if e = json.Unmarshal(encode(r), &bad); e != nil {
			t.Fatal(e)
		}
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted receipt tamper")
		}
	}
	pending, e := finishReceipt(r, r.Bundle, dependencybundle.ErrPublicationUnknown)
	if !errors.Is(e, dependencybundle.ErrPublicationUnknown) || pending.Bundle != r.Bundle || pending.Digest != "" || pending.Validate() == nil {
		t.Fatal("publication unknown authorized")
	}
}
func TestPythonMetadataFoldingAndRepeatedRequirements(t *testing.T) {
	f := wheelFiles("alpha", "1.0", "requires-dist: beta (>=1,\n <2)\nRequires-Dist: beta !=1.5\n")
	f["alpha-1.0.dist-info/METADATA"] = bytes.Replace(f["alpha-1.0.dist-info/METADATA"], []byte("Name: alpha"), []byte("nAmE: ALPHA"), 1)
	a := makeWheel(t, "alpha", "1.0", f, nil)
	b := makeWheel(t, "beta", "1.0", wheelFiles("beta", "1.0", ""), nil)
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": a, "beta": b}, []pythonRoot{{"alpha", "1.0"}})
	r, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Edges) != 3 {
		t.Fatal("repeated constraints lost")
	}
	f = wheelFiles("alpha", "1.0", "Requires-Dist: beta >=1\nRequires-Dist: beta >1\n")
	a = makeWheel(t, "alpha", "1.0", f, nil)
	store, tree, lock = pythonFixture(t, map[string][]byte{"alpha": a, "beta": b}, []pythonRoot{{"alpha", "1.0"}})
	if _, e = ImportPython(context.Background(), store, tree, lock); e == nil || e.Error() != "dependency_version_mismatch" {
		t.Fatalf("did not enforce conjunction: %v", e)
	}
}

func rewriteZIP(t *testing.T, b []byte, change func(int, *zip.FileHeader, []byte) (*zip.FileHeader, []byte), extra *zip.FileHeader) []byte {
	t.Helper()
	zr, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for i, f := range zr.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		h := f.FileHeader
		h.CRC32 = 0
		h.CompressedSize = 0
		h.CompressedSize64 = 0
		h.UncompressedSize = 0
		h.UncompressedSize64 = 0
		header, data := change(i, &h, data)
		if header == nil {
			continue
		}
		w, e := zw.CreateHeader(header)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if extra != nil {
		w, e := zw.CreateHeader(extra)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.HasSuffix(extra.Name, "/") {
			w.Write([]byte("extra"))
		}
	}
	if e = zw.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func TestPythonZIPAdversarial(t *testing.T) {
	base := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	for _, kind := range []string{"CRC", "encrypted", "unknown method", "duplicate", "empty directory", "symlink", "executable", "extra file", "missing file", "declared bomb", "expanded bomb", "central count", "file ancestor", "second dist info", "signature", "record extra row", "record padded hash", "metadata too large", "active extras", "dynamic", "requires external", "requires python unsupported"} {
		t.Run(kind, func(t *testing.T) {
			b := append([]byte(nil), base...)
			switch kind {
			case "CRC":
				b = rewriteZIP(t, b, func(i int, h *zip.FileHeader, d []byte) (*zip.FileHeader, []byte) { h.Method = zip.Store; return h, d }, nil)
				zr, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
				if e != nil {
					t.Fatal(e)
				}
				off, e := zr.File[0].DataOffset()
				if e != nil {
					t.Fatal(e)
				}
				b[int(off)] ^= 1
			case "encrypted", "unknown method":
				for i := 0; i+46 < len(b); i++ {
					switch binary.LittleEndian.Uint32(b[i:]) {
					case 0x04034b50:
						if kind == "encrypted" {
							b[i+6] |= 1
						} else {
							b[i+8] = 99
						}
					case 0x02014b50:
						if kind == "encrypted" {
							b[i+8] |= 1
						} else {
							b[i+10] = 99
						}
					}
				}
			case "declared bomb":
				for i := 0; i+46 < len(b); i++ {
					if binary.LittleEndian.Uint32(b[i:]) == 0x02014b50 {
						binary.LittleEndian.PutUint32(b[i+24:], uint32(dependencybundle.MaxFileBytes+1))
						break
					}
				}
			case "central count":
				binary.LittleEndian.PutUint16(b[len(b)-14:], 10001)
				binary.LittleEndian.PutUint16(b[len(b)-12:], 10001)
			default:
				var extra *zip.FileHeader
				if kind == "duplicate" {
					extra = &zip.FileHeader{Name: "namespace/alpha.py", Method: zip.Deflate}
					extra.SetMode(0644)
				}
				if kind == "empty directory" {
					extra = &zip.FileHeader{Name: "empty/", Method: zip.Store}
					extra.SetMode(os.ModeDir | 0755)
				}
				if kind == "extra file" {
					extra = &zip.FileHeader{Name: "unlisted.txt", Method: zip.Deflate}
					extra.SetMode(0644)
				}
				if kind == "file ancestor" {
					extra = &zip.FileHeader{Name: "namespace", Method: zip.Deflate}
					extra.SetMode(0644)
				}
				if kind == "second dist info" {
					extra = &zip.FileHeader{Name: "other-1.0.dist-info/METADATA", Method: zip.Deflate}
					extra.SetMode(0644)
				}
				if kind == "signature" {
					extra = &zip.FileHeader{Name: "alpha-1.0.dist-info/RECORD.jws", Method: zip.Deflate}
					extra.SetMode(0644)
				}
				b = rewriteZIP(t, b, func(i int, h *zip.FileHeader, d []byte) (*zip.FileHeader, []byte) {
					if i == 0 {
						switch kind {
						case "symlink":
							h.SetMode(os.ModeSymlink | 0777)
						case "executable":
							h.SetMode(0755)
						case "missing file":
							return nil, nil
						case "expanded bomb":
							d = bytes.Repeat([]byte("x"), int(dependencybundle.MaxFileBytes+1))
						}
					}
					if strings.HasSuffix(h.Name, "/RECORD") {
						switch kind {
						case "record extra row":
							d = append(d, []byte("missing,,\n")...)
						case "record padded hash":
							lines := strings.Split(string(d), "\n")
							parts := strings.Split(lines[0], ",")
							parts[1] += "="
							lines[0] = strings.Join(parts, ",")
							d = []byte(strings.Join(lines, "\n"))
						}
					}
					if strings.HasSuffix(h.Name, "/METADATA") {
						switch kind {
						case "metadata too large":
							d = bytes.Repeat([]byte("x"), maxPackageJSON+1)
						case "active extras":
							d = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nRequires-Dist: beta[docs]\n")
						case "dynamic":
							d = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nDynamic: Requires-Dist\n")
						case "requires external":
							d = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nRequires-External: something\n")
						case "requires python unsupported":
							d = []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\nRequires-Python: >=3rc1\n")
						}
					}
					return h, d
				}, extra)
			}
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatalf("accepted %s", kind)
			}
			assertNoPythonStage(t, store)
		})
	}
}
func TestPythonArchiveLinksAndAncestors(t *testing.T) {
	for _, kind := range []string{"hardlink", "ancestor link", "store link"} {
		t.Run(kind, func(t *testing.T) {
			b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			switch kind {
			case "hardlink":
				if e := os.Link(filepath.Join(tree, "alpha-1.0-py3-none-any.whl"), filepath.Join(filepath.Dir(tree), "outside.whl")); e != nil {
					t.Skip(e)
				}
			case "ancestor link":
				link := filepath.Join(filepath.Dir(tree), "linked")
				if e := os.Symlink(filepath.Dir(tree), link); e != nil {
					t.Skip(e)
				}
				tree = filepath.Join(link, filepath.Base(tree))
			case "store link":
				link := filepath.Join(filepath.Dir(store), "linked-store")
				if e := os.Symlink(store, link); e != nil {
					t.Skip(e)
				}
				store = link
			}
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("accepted linked path")
			}
		})
	}
}

func TestPythonMetadataRequirementCountBeforeGrowth(t *testing.T) {
	raw := []byte("Metadata-Version: 2.4\nName: alpha\nVersion: 1.0\n" + strings.Repeat("Requires-Dist: a\n", maxEdges+1))
	if _, e := pythonHeaders(raw); e == nil {
		t.Fatal("unbounded requirement header growth")
	}
}
func TestPythonLargeArchiveIsNotPayloadFile(t *testing.T) {
	f := wheelFiles("alpha", "1.0", "")
	rng := rand.New(rand.NewSource(7))
	for _, p := range []string{"resource-a.bin", "resource-b.bin"} {
		b := make([]byte, (4<<20)+1024)
		if _, e := rng.Read(b); e != nil {
			t.Fatal(e)
		}
		f[p] = b
	}
	b := makeWheel(t, "alpha", "1.0", f, nil)
	if int64(len(b)) <= dependencybundle.MaxFileBytes {
		t.Fatal("fixture archive is too small")
	}
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	r, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil {
		t.Fatal(e)
	}
	if e = dependencybundle.Verify(context.Background(), store, r.Bundle); e != nil {
		t.Fatal(e)
	}
	assertNoPythonStage(t, store)
}
func TestPythonSharedNamespaceCollision(t *testing.T) {
	for _, p := range []string{"namespace/alpha.py", "Namespace/new.py", "namespace"} {
		t.Run(p, func(t *testing.T) {
			f := wheelFiles("beta", "1.0", "")
			f[p] = []byte("value = 1\n")
			a := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", "Requires-Dist: beta\n"), nil)
			b := makeWheel(t, "beta", "1.0", f, nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": a, "beta": b}, []pythonRoot{{"alpha", "1.0"}})
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("merged colliding purelib tree")
			}
			assertNoPythonStage(t, store)
		})
	}
}
func TestPythonTotalExpansionAndEntryBounds(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	w := pythonWheel{Name: "alpha", Version: "1.0", SHA256: sum(b), Size: int64(len(b))}
	total := dependencybundle.MaxPayloadBytes - 1
	entries := 0
	if _, e := admitWheel(context.Background(), w, b, &total, &entries); e == nil {
		t.Fatal("exceeded cumulative expansion bound")
	}
	total = 0
	entries = dependencybundle.MaxFiles
	if _, e := admitWheel(context.Background(), w, b, &total, &entries); e == nil {
		t.Fatal("exceeded cumulative entry bound")
	}
}

func TestPythonReceiptRejectsUnpublishableLocation(t *testing.T) {
	b := makeWheel(t, "alpha", "1.0", wheelFiles("alpha", "1.0", ""), nil)
	store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
	r, e := ImportPython(context.Background(), store, tree, lock)
	if e != nil {
		t.Fatal(e)
	}
	r.Packages[0].Name = strings.Repeat("a", 129)
	r.Packages[0].Location = r.Packages[0].Name + "-1.0.dist-info"
	r.Edges[0].Name = r.Packages[0].Name
	r.Edges[0].To = r.Packages[0].Location
	r.Digest = r.computeDigest()
	if r.Validate() == nil {
		t.Fatal("accepted location impossible under portable payload policy")
	}
}
func TestPythonMetadataOnlySupportedVersion(t *testing.T) {
	for _, version := range []string{"2.1|2.2", "2.4|2.5", "2.6|"} {
		t.Run(version, func(t *testing.T) {
			f := wheelFiles("alpha", "1.0", "")
			f["alpha-1.0.dist-info/METADATA"] = bytes.Replace(f["alpha-1.0.dist-info/METADATA"], []byte("2.4"), []byte(version), 1)
			b := makeWheel(t, "alpha", "1.0", f, nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("accepted metadata version substring")
			}
		})
	}
}

func TestPythonRejectsLegacyMetadataAndScriptWrappers(t *testing.T) {
	for _, p := range []string{"extra.egg-info/PKG-INFO", "namespace/legacy.egg/module.py", "command.cmd", "command.bat", "command.ps1", "command.sh"} {
		t.Run(p, func(t *testing.T) {
			f := wheelFiles("alpha", "1.0", "")
			f[p] = []byte("inert fixture")
			b := makeWheel(t, "alpha", "1.0", f, nil)
			store, tree, lock := pythonFixture(t, map[string][]byte{"alpha": b}, []pythonRoot{{"alpha", "1.0"}})
			if _, e := ImportPython(context.Background(), store, tree, lock); e == nil {
				t.Fatal("accepted legacy metadata or script wrapper")
			}
		})
	}
}
