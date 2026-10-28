package vmguest

import (
	"bytes"
	"encoding/json"
	"foundry/internal/frameworkreport"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		cmd = exec.Command("/opt/anaconda3/bin/python3", "-I", "-S", "-B", "-c", `import runpy,sys,json; m=runpy.run_path(sys.argv[1]); sys.exit(m['run_tests'](json.loads(sys.argv[2]),3))`, runnerAssetV2(t, "python.py"), string(raw))
	}
	cmd.Dir = root
	cmd.Env = []string{"PATH=/opt/anaconda3/bin:/usr/bin:/bin", "LANG=C.UTF-8", "TZ=UTC", "TMPDIR=" + t.TempDir()}
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
