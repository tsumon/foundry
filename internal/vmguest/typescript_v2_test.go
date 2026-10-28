package vmguest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func compilerFixtureV2(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	p := "../../../build/multilang-inputs/typescript-5.9.3.tgz"
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != "10e108c9cf7d5f2879053dff18515fb405abf2ccef63eaaf017d9c571687a1d3" {
		t.Fatal("pinned TS input drift")
	}
	g, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	defer g.Close()
	r := tar.NewReader(g)
	for {
		entry, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		base := strings.TrimPrefix(entry.Name, "package/lib/")
		if entry.Typeflag != tar.TypeReg || base == entry.Name || strings.Contains(base, "/") || (base != "typescript.js" && !(strings.HasPrefix(base, "lib.") && strings.HasSuffix(base, ".d.ts"))) {
			continue
		}
		if entry.Size > 12<<20 {
			t.Fatal("large compiler fixture")
		}
		data, e := io.ReadAll(io.LimitReader(r, 12<<20))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, base), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	return filepath.Join(root, "typescript.js")
}
func TestV2TypeScriptCompilerUsesPinnedAPIAndExactInputs(t *testing.T) {
	compiler := compilerFixtureV2(t)
	for _, tc := range []struct {
		name, body, config string
		ok                 bool
	}{
		{"pass", "export const value: number = 42;", "", true},
		{"ambient", "export const value: FoundryOwned = {value:42};", "", true},
		{"typeerror", "export const value: number = 'bad';", "", false},
		{"declaration", "export declare const value: number;", "", true},
		{"unadmitted", "import './other'; export const value = 1;", "", false},
		{"plugins", "export const value = 1;", `,"plugins":[]`, false},
		{"duplicate", "export const value = 1;", `,"strict":false`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, out := t.TempDir(), t.TempDir()
			if tc.name == "ambient" {
				dir := filepath.Join(root, "node_modules/@types/owned")
				if e := os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
				_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"@types/owned","types":"index.d.ts"}`), 0600)
				_ = os.WriteFile(filepath.Join(dir, "index.d.ts"), []byte(`interface FoundryOwned { value:number }`), 0600)
			}
			input := "a.ts"
			if tc.name == "declaration" {
				input = "a.d.ts"
			}
			_ = os.WriteFile(filepath.Join(root, input), []byte(tc.body), 0600)
			_ = os.WriteFile(filepath.Join(root, "other.ts"), []byte("throw new Error('must not read or execute');"), 0600)
			_ = os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module"}`), 0600)
			config := `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"noEmitOnError":true,"allowJs":false,"skipLibCheck":false` + tc.config + `},"files":["` + input + `"]}`
			_ = os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(config), 0600)
			contract := childContractV2{SchemaVersion: 1, Phase: compileTypeScriptV2, SourceRoots: []string{"."}, TestFiles: []string{}, InputFiles: []string{input}, SourceRoot: root, OutputRoot: out}
			raw, _ := json.Marshal(contract)
			bridge := filepath.Join(t.TempDir(), "bridge.cjs")
			_ = os.WriteFile(bridge, []byte(`try{require(process.argv[2]).compile(JSON.parse(process.argv[4]),require(process.argv[3]));}catch(e){console.error(e.message);process.exitCode=2}`), 0600)
			cmd := exec.Command("node", bridge, runnerAssetV2(t, "typescript.cjs"), compiler, string(raw))
			b, e := cmd.CombinedOutput()
			if (e == nil) != tc.ok {
				t.Fatalf("want ok=%v: %v %s", tc.ok, e, b)
			}
			entries, e := os.ReadDir(out)
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if tc.ok && input == "a.ts" {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("unexpected emitted files %v", entries)
			}
		})
	}
}
