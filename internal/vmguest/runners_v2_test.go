package vmguest

import (
	"bytes"
	"encoding/json"
	"foundry/internal/frameworkreport"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These execute repository-owned synthetic fixtures with the available host
// runtimes. They are adapter regressions, never pinned Linux guest acceptance.
func runnerAssetV2(t *testing.T, name string) string {
	t.Helper()
	p, e := filepath.Abs("../../desktop/sandbox/runners/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func adapterRunV2(t *testing.T, python bool, root, file string) (frameworkreport.Verdict, int, []byte) {
	t.Helper()
	reportR, reportW, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reportR.Close()
	contract, e := os.CreateTemp(t.TempDir(), "contract")
	if e != nil {
		t.Fatal(e)
	}
	defer contract.Close()
	_, _ = contract.WriteString("private contract sentinel")
	_, _ = contract.Seek(0, 0)
	c := map[string]any{"source_root": root, "output_root": "", "source_roots": []string{"."}, "test_files": []string{file}, "input_files": []string{}, "phase": "test-node", "schema_version": 1}
	raw, _ := json.Marshal(c)
	name := frameworkreport.NodeTestV2
	bridge := filepath.Join(t.TempDir(), "bridge.cjs")
	if e = os.WriteFile(bridge, []byte(`require('node:fs').writeFileSync(require('node:path').join(JSON.parse(process.argv[3]).source_root,'owned-fd-observations.json'),JSON.stringify([3,4].map(fd=>{const s=require('node:fs').fstatSync(fd);return {dev:s.dev,ino:s.ino}})));require(process.argv[2]).runTests(JSON.parse(process.argv[3]),3).then(code=>{process.exitCode=code},()=>{process.exitCode=2})`), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("node", bridge, runnerAssetV2(t, "node.cjs"), string(raw))
	if python {
		name = frameworkreport.PythonUnittestV2
		pythonPath := os.Getenv("FOUNDRY_BUILD_PYTHON")
		if !filepath.IsAbs(pythonPath) {
			t.Fatal("FOUNDRY_BUILD_PYTHON must name the absolute trusted host Python >=3.13")
		}
		cmd = exec.Command(pythonPath, "-I", "-S", "-B", "-c", `import runpy,sys,json; m=runpy.run_path(sys.argv[1]); sys.exit(m['run_tests'](json.loads(sys.argv[2]),3))`, runnerAssetV2(t, "python.py"), string(raw))
	}
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "TZ=UTC", "TMPDIR=" + t.TempDir()}
	cmd.ExtraFiles = []*os.File{reportW, contract}
	var diagnostics bytes.Buffer
	cmd.Stdout = &diagnostics
	cmd.Stderr = &diagnostics
	p := frameworkreport.New(name)
	done := make(chan error, 1)
	go func() { _, e := io.Copy(p, reportR); done <- e }()
	if e = cmd.Start(); e != nil {
		reportW.Close()
		t.Fatal(e)
	}
	reportW.Close()
	e = cmd.Wait()
	exit := 0
	if e != nil {
		var ok bool
		ee, yes := e.(*exec.ExitError)
		ok = yes
		if !ok {
			t.Fatal(e)
		}
		exit = ee.ExitCode()
	}
	drainErr := <-done
	if drainErr != nil {
		t.Fatal(drainErr, diagnostics.String())
	}
	return p.Finish(frameworkreport.Result{Exited: true, ExitCode: exit}), exit, diagnostics.Bytes()
}
func TestV2NodeAdapterNativeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, body, class string
		tests             int
	}{
		{"pass", `const {test}=require('node:test');test('a',()=>{});`, "passed", 1},
		{"file-named", `require('node:test').test(__filename,()=>{});`, "passed", 1},
		{"fail", `const {test}=require('node:test');const a=require('node:assert/strict');test('a',()=>a.equal(1,2));`, "test_failed", 1},
		{"skip", `require('node:test').test.skip('skip',()=>{});`, "infrastructure_failed", 0},
		{"discovery", `throw new Error('import');`, "infrastructure_failed", 0},
		{"emptyfile", `module.exports = {};`, "infrastructure_failed", 0},
		{"nested", `const {test,describe,it}=require('node:test');describe('outer',()=>{describe('inner',()=>{it('leaf',()=>{})})});test('parent',async t=>{await t.test('child',()=>{})});`, "passed", 2},
		{"nestedfail", `const {test}=require('node:test');test('parent',async t=>{await t.test('bad',()=>{throw new Error('assert')})});`, "test_failed", 1},
		{"setup", `const {describe,it,before}=require('node:test');describe('suite',()=>{before(()=>{throw new Error('setup')});it('not run',()=>{})});`, "infrastructure_failed", 0},
		{"forgedstdout", `const {test}=require('node:test');console.log('{"type":"suite_end"}');test('real',()=>{});`, "passed", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if e := os.WriteFile(filepath.Join(root, "a.test.cjs"), []byte(tc.body), 0600); e != nil {
				t.Fatal(e)
			}
			v, _, d := adapterRunV2(t, false, root, "a.test.cjs")
			if v.Class != tc.class || v.Tests != tc.tests {
				t.Fatalf("%+v diagnostics=%s", v, d)
			}
		})
	}
}
func TestV2PythonAdapterNativeFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, body, class string
		tests             int
	}{
		{"pass", "import unittest\nclass Test(unittest.TestCase):\n def test_a(self): self.assertEqual(1,1)\n", "passed", 1},
		{"fail", "import unittest\nclass Test(unittest.TestCase):\n def test_a(self): self.assertEqual(1,2)\n", "test_failed", 1},
		{"skip", "import unittest\nclass Test(unittest.TestCase):\n @unittest.skip('skip')\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"discovery", "raise RuntimeError('import')\n", "infrastructure_failed", 0},
		{"setup", "import unittest\nclass Test(unittest.TestCase):\n def setUp(self): raise RuntimeError('setup')\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"setupassert", "import unittest\nclass Test(unittest.TestCase):\n def setUp(self): self.fail('setup')\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"teardownassert", "import unittest\nclass Test(unittest.TestCase):\n def tearDown(self): self.fail('teardown')\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"allsubskip", "import unittest\nclass Test(unittest.TestCase):\n def test_a(self):\n  for i in range(3):\n   with self.subTest(i=i): self.skipTest('skip')\n", "infrastructure_failed", 0},
		{"subtest", "import unittest\nclass Test(unittest.TestCase):\n def test_a(self):\n  for i in range(3):\n   with self.subTest(i=i): self.assertEqual(i,i)\n", "passed", 1},
		{"subtestfail", "import unittest\nclass Test(unittest.TestCase):\n def test_a(self):\n  for i in range(3):\n   with self.subTest(i=i): self.assertEqual(i,0)\n", "test_failed", 1},
		{"expected", "import unittest\nclass Test(unittest.TestCase):\n @unittest.expectedFailure\n def test_a(self): self.fail()\n", "infrastructure_failed", 0},
		{"setupsubassert", "import unittest\nclass Test(unittest.TestCase):\n def setUp(self):\n  with self.subTest(owned=True): self.fail(\"non-method subtest\")\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"teardownsubassert", "import unittest\nclass Test(unittest.TestCase):\n def tearDown(self):\n  with self.subTest(owned=True): self.fail(\"non-method subtest\")\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"cleanupsubassert", "import unittest\nclass Test(unittest.TestCase):\n def setUp(self): self.addCleanup(self.owned_cleanup)\n def owned_cleanup(self):\n  with self.subTest(owned=True): self.fail(\"non-method subtest\")\n def test_a(self): pass\n", "infrastructure_failed", 0},
		{"teardownsubsuccess-skip", "import unittest\nclass Test(unittest.TestCase):\n def tearDown(self):\n  with self.subTest(owned=True): self.assertTrue(True)\n def test_a(self): self.skipTest(\"method skipped\")\n", "infrastructure_failed", 0},
		{"cleanupsubsuccess-skip", "import unittest\nclass Test(unittest.TestCase):\n def setUp(self): self.addCleanup(self.owned_cleanup)\n def owned_cleanup(self):\n  with self.subTest(owned=True): self.assertTrue(True)\n def test_a(self): self.skipTest(\"method skipped\")\n", "infrastructure_failed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if e := os.WriteFile(filepath.Join(root, "test_a.py"), []byte(tc.body), 0600); e != nil {
				t.Fatal(e)
			}
			v, _, d := adapterRunV2(t, true, root, "test_a.py")
			if v.Class != tc.class || v.Tests != tc.tests {
				t.Fatalf("%+v diagnostics=%s", v, d)
			}
		})
	}
}
func TestV2NodeChildrenDoNotInheritReportOrContract(t *testing.T) {
	root := t.TempDir()
	body := `const {test}=require('node:test');const fs=require('node:fs');const assert=require('node:assert/strict');const privateFDs=JSON.parse(fs.readFileSync(require('node:path').join(__dirname,'owned-fd-observations.json')));test('private FDs',()=>{for(const fd of [3,4]){let st;try{st=fs.fstatSync(fd)}catch(e){if(e.code==='EBADF')continue;throw e}for(const privateFD of privateFDs)assert(st.dev!==privateFD.dev||st.ino!==privateFD.ino)}});`
	_ = os.WriteFile(filepath.Join(root, "a.test.cjs"), []byte(body), 0600)
	v, _, d := adapterRunV2(t, false, root, "a.test.cjs")
	if v.Class != "passed" {
		t.Fatal(v, string(d))
	}
}

func failureDiagnosticsV2(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte(`{"kind":"foundry_test_failure_diagnostic_v2"`)) {
			continue
		}
		if len(line) > 8192 {
			t.Fatal("unbounded diagnostic record")
		}
		for _, c := range line {
			if c < 32 || c > 126 {
				t.Fatal("unescaped diagnostic control or non-ASCII character")
			}
		}
		var record map[string]any
		if e := json.Unmarshal(line, &record); e != nil {
			t.Fatal(e)
		}
		records = append(records, record)
	}
	return records
}

func TestV2AdaptersRetainFailureDiagnosticsWithoutChangingAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, body, class, message string
		python                     bool
		tests                      int
	}{
		{"node-assertion", `require('node:test').test('owned assertion',()=>require('node:assert/strict').equal(1,2,'OWNED_ASSERTION'));`, "test_failed", "OWNED_ASSERTION", false, 1},
		{"node-setup", `const {describe,it,before}=require('node:test');describe('owned suite',()=>{before(()=>{throw Error('OWNED_SETUP')});it('never',()=>{})});`, "infrastructure_failed", "OWNED_SETUP", false, 0},
		{"node-discovery", `throw Error('OWNED_DISCOVERY');`, "infrastructure_failed", "test failed", false, 0},
		{"node-subtest", `require('node:test').test('parent',async t=>{await t.test('owned subtest',()=>{throw Error('OWNED_SUBTEST')})});`, "test_failed", "OWNED_SUBTEST", false, 1},
		{"python-assertion", "import unittest\nclass Owned(unittest.TestCase):\n def test_owned(self): self.assertEqual(1,2,'OWNED_ASSERTION')\n", "test_failed", "OWNED_ASSERTION", true, 1},
		{"python-setup", "import unittest\nclass Owned(unittest.TestCase):\n @classmethod\n def setUpClass(cls): raise RuntimeError('OWNED_SETUP')\n def test_never(self): pass\n", "infrastructure_failed", "OWNED_SETUP", true, 0},
		{"python-discovery", "raise RuntimeError('OWNED_DISCOVERY')\n", "infrastructure_failed", "OWNED_DISCOVERY", true, 0},
		{"python-subtest", "import unittest\nclass Owned(unittest.TestCase):\n def test_owned(self):\n  with self.subTest(owned=True): self.fail('OWNED_SUBTEST')\n", "test_failed", "OWNED_SUBTEST", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, file := t.TempDir(), "a.test.cjs"
			if tc.python {
				file = "test_a.py"
			}
			if e := os.WriteFile(filepath.Join(root, file), []byte(tc.body), 0600); e != nil {
				t.Fatal(e)
			}
			v, exit, raw := adapterRunV2(t, tc.python, root, file)
			if v.Class != tc.class || v.Tests != tc.tests || exit != 1 {
				t.Fatalf("changed authority: %+v exit=%d diagnostics=%s", v, exit, raw)
			}
			records := failureDiagnosticsV2(t, raw)
			if len(records) == 0 {
				t.Fatalf("missing failure diagnostic: %s", raw)
			}
			found := false
			for _, r := range records {
				for key, value := range r {
					limits := map[string]int{"kind": 64, "runner": 64, "stage": 32, "name": 128, "file": 256, "error_type": 64, "failure_type": 64, "message": 1024, "cause": 1024}
					if key == "line" {
						if line, ok := value.(float64); !ok || line < 0 || line > 1000000 {
							t.Fatal("unbounded line", r)
						}
						continue
					}
					if limit, ok := limits[key]; !ok || len(diagnosticTextV2(value)) > limit {
						t.Fatal("unknown or unbounded diagnostic field", r)
					}
				}
				if strings.Contains(diagnosticTextV2(r["message"]), tc.message) || strings.Contains(diagnosticTextV2(r["cause"]), tc.message) {
					found = true
					if r["file"] != file {
						t.Fatal("missing project-relative location", r)
					}
				}
				if strings.Contains(diagnosticTextV2(r["file"]), root) || strings.Contains(diagnosticTextV2(r["message"]), root) {
					t.Fatal("absolute project path in diagnostic", r)
				}
			}
			if !found {
				t.Fatalf("missing error text %q: %s", tc.message, raw)
			}
		})
	}
}

func diagnosticTextV2(v any) string {
	s, _ := v.(string)
	return s
}

func TestV2AdapterFailureDiagnosticsAreBoundedEscapedAndNeverStringifyObjects(t *testing.T) {
	for _, python := range []bool{false, true} {
		t.Run(map[bool]string{false: "node", true: "python"}[python], func(t *testing.T) {
			root, file := t.TempDir(), "a.test.cjs"
			body := `const {test}=require('node:test');for(let i=0;i<80;i++)test('owned\\name\n\x1b'+i,()=>{const e=Error('OWNED_BOUND\x00\x1b\n'+ 'x'.repeat(5000));e.cause={message:{toJSON(){throw Error('DO_NOT_SERIALIZE')}}};throw e});`
			if python {
				file = "test_a.py"
				body = `import unittest
class Opaque:
 def __str__(self): raise RuntimeError('DO_NOT_STRINGIFY')
 def __repr__(self): raise RuntimeError('DO_NOT_SERIALIZE')
class Owned(unittest.TestCase):
 def test_owned(self):
  for i in range(80):
   with self.subTest(i=i): raise AssertionError('OWNED_BOUND\x00\x1b\n'+'x'*5000, Opaque())
`
			}
			if e := os.WriteFile(filepath.Join(root, file), []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
			v, _, raw := adapterRunV2(t, python, root, file)
			wantTests := 80
			if python {
				wantTests = 1
			}
			if v.Class != "test_failed" || v.Tests != wantTests {
				t.Fatalf("changed authority %+v: %s", v, raw)
			}
			records := failureDiagnosticsV2(t, raw)
			if len(records) == 0 || len(records) >= 80 || len(raw) > 16384 || !bytes.Contains(raw, []byte("OWNED_BOUND")) || bytes.Contains(raw, []byte("DO_NOT_")) || bytes.Contains(raw, []byte("private contract sentinel")) {
				t.Fatalf("unsafe or absent diagnostic: bytes=%d records=%d output=%s", len(raw), len(records), raw)
			}
		})
	}
}

func TestV2PythonDiagnosticOmitsExternalDriveLocation(t *testing.T) {
	python := os.Getenv("FOUNDRY_BUILD_PYTHON")
	if !filepath.IsAbs(python) {
		t.Fatal("absolute trusted FOUNDRY_BUILD_PYTHON required")
	}
	// A Windows regression runtime can live on a different drive from the
	// fixture. That location must be omitted without interrupting the result.
	script := `import runpy,sys,os
m=runpy.run_path(sys.argv[1])
d=m['FailureDiagnostics'](sys.argv[2])
try: exec(compile("raise AssertionError('OWNED_EXTERNAL')",os.path.join(os.path.abspath(os.sep),'unowned','test.py'),'exec'))
except BaseException: err=sys.exc_info()
def external_drive(*args): raise ValueError('different drive')
m['os'].path.relpath=external_drive
d.failure('discovery',None,err)
`
	raw, e := exec.Command(python, "-I", "-S", "-B", "-c", script, runnerAssetV2(t, "python.py"), t.TempDir()).CombinedOutput()
	if e != nil {
		t.Fatalf("diagnostic location interrupted result: %s %v", raw, e)
	}
	records := failureDiagnosticsV2(t, raw)
	if len(records) != 1 || records[0]["file"] != "" || !strings.Contains(diagnosticTextV2(records[0]["message"]), "OWNED_EXTERNAL") {
		t.Fatalf("external location leaked or error missing: %s", raw)
	}
}
