package vmacceptance

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/gitexec"
	"foundry/internal/platformfs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// Closed repository-owned protocol fixtures, never user projects or downloaded
// packages. Dependencies are constructed locally and admitted by production APIs.
type fixtureSpec struct {
	profile, caseID, path, initial, worker, repaired, verifier, sourceRoot string
	files, dependencies                                                    map[string]string
	expectedClass                                                          string
	expectedTests                                                          int
}

func selectFixture(profile, caseID string) (fixtureSpec, error) {
	f := fixtureSpec{profile: profile, caseID: caseID, sourceRoot: ".", expectedTests: 4, files: map[string]string{}, dependencies: map[string]string{}}
	if caseID == "" {
		return f, errors.New("explicit_v2_case_required")
	}
	switch profile {
	case "c4_node_model_v2", "c4_typescript_model_v2":
		ext, typ := "js", ""
		f.verifier = "/opt/foundry/verify-node-v2"
		if profile == "c4_typescript_model_v2" {
			ext = "ts"
			typ = ": number"
			f.verifier = "/opt/foundry/verify-typescript-v2"
		}
		f.path = "src/triple." + ext
		prefix := "const { identity } = require('foundry-fixture-dep');\nexports.triple = function(n) { "
		suffix := " };\n"
		if ext == "ts" {
			prefix = "import { identity } from 'foundry-fixture-dep';\nexport function triple(n" + typ + ")" + typ + " { "
			suffix = " }\n"
		}
		f.initial = prefix + "return identity(n) * 3;" + suffix
		f.worker = prefix + "if (n < 0) return 0; return identity(n) + identity(n) + identity(n);" + suffix
		f.repaired = prefix + "return identity(n) + identity(n) + identity(n);" + suffix
		pkg := `{"name":"foundry-native-fixture","version":"1.0.0","dependencies":{"foundry-fixture-dep":"1.0.0"}}`
		dep := `{"name":"foundry-fixture-dep","version":"1.0.0","main":"index.js","types":"index.d.ts"}`
		f.files["package.json"] = pkg
		f.files["package-lock.json"] = `{"lockfileVersion":3,"packages":{"":` + pkg + `,"node_modules/foundry-fixture-dep":{"name":"foundry-fixture-dep","version":"1.0.0"}}}`
		f.dependencies = map[string]string{"foundry-fixture-dep/package.json": dep, "foundry-fixture-dep/index.js": "exports.identity = n => n;\n", "foundry-fixture-dep/index.d.ts": "export declare function identity(n: number): number;\n", "foundry-fixture-dep/LICENSE": "Repository-owned synthetic acceptance fixture; CC0-1.0.\n"}
		f.files["tests/guards.cjs"] = nodeGuardsV2
		test := `const {test} = require('node:test');
const assert = require('node:assert/strict');
const {triple} = require('../src/triple.js');
test('native isolation',()=>require('/workspace/tests/guards.cjs')());
test('frozen dependency resolution',()=>assert.equal(require('foundry-fixture-dep').identity(9),9));
test('positive',()=>assert.equal(triple(4),12));
test('negative',()=>assert.equal(triple(-2),-6));
`
		if ext == "ts" {
			test = "declare const require: (name: string) => any;\n" + test
			f.files["tsconfig.json"] = `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"noEmitOnError":true,"allowJs":false,"skipLibCheck":false},"files":["src/triple.ts","tests/triple.test.ts"]}`
		}
		f.files["tests/triple.test."+ext] = test
		if caseID != "workflow" {
			if ext != "ts" || caseID != "typecheck" {
				return f, errors.New("unsupported_v2_fixture_case")
			}
			f.initial = prefix + `return "genuine compiler error";` + suffix
			f.expectedClass = "infrastructure_failed"
			f.expectedTests = 0
		}
	case "c4_python_model_v2":
		f.sourceRoot = "src"
		f.path = "src/triple.py"
		f.verifier = "/opt/foundry/verify-python-v2"
		f.initial = "from foundry_fixture_dep import identity\ndef triple(n):\n return identity(n) * 3\n"
		f.worker = "from foundry_fixture_dep import identity\ndef triple(n):\n if n < 0: return 0\n return identity(n) + identity(n) + identity(n)\n"
		f.repaired = "from foundry_fixture_dep import identity\ndef triple(n):\n return identity(n) + identity(n) + identity(n)\n"
		f.files["tests/guards.py"] = pythonGuardsV2
		f.files["tests/test_triple.py"] = `import unittest, sys, os
sys.path.insert(0, os.path.dirname(__file__))
from triple import triple
from foundry_fixture_dep import identity
from guards import check
class TestTriple(unittest.TestCase):
 def test_native_isolation(self): check(self)
 def test_dependency(self): self.assertEqual(identity(9), 9)
 def test_positive(self): self.assertEqual(triple(4),12)
 def test_negative(self): self.assertEqual(triple(-2),-6)
`
		if caseID != "workflow" {
			body, ok := pythonFaultsV2[caseID]
			if !ok {
				return f, errors.New("unsupported_v2_fixture_case")
			}
			f.files["tests/test_triple.py"] = body
			f.expectedClass = "infrastructure_failed"
			f.expectedTests = 0
			if caseID == "timeout" {
				f.expectedClass = "timeout"
			}
			if caseID == "descendant" {
				f.expectedClass = "passed"
				f.expectedTests = 1
			}
		}
	default:
		return f, errors.New("unsupported_v2_profile")
	}
	f.files[f.path] = f.initial
	return f, nil
}

const nodeGuardsV2 = `module.exports = function() {
 const fs=require('node:fs'),os=require('node:os'),a=require('node:assert/strict');
 // Node includes the effective GID even when Linux getgroups(2) is empty.
 a.equal(process.getuid(),65534);a.equal(process.getgid(),65534);a.deepEqual(process.getgroups(),[65534]);
 a.match(fs.readFileSync('/proc/self/status','utf8'),/^NoNewPrivs:\s+1$/m);
 a.deepEqual(Object.keys(os.networkInterfaces()),[]);
 const mounts=fs.readFileSync('/proc/self/mountinfo','utf8');
 a.doesNotMatch(mounts,/ - (9p|virtiofs|nfs|cifs) /);
 for(const p of ['/workspace/src/triple.js','/workspace/src/triple.ts','/workspace/package.json','/workspace/node_modules/foundry-fixture-dep/index.js','/usr/bin/node','/foundry-build/compiled/src/triple.js']) {
  if(!fs.existsSync(p))continue;
  a.throws(()=>{const fd=fs.openSync(p,'r+');fs.closeSync(fd)},undefined,'immutable '+p);
 }
 for(const p of ['/dev/hvc0','/dev/ttyS0','/dev/console','/proc/1/mem','/proc/1/fd/0']) a.throws(()=>{const fd=fs.openSync(p,'r');fs.closeSync(fd)});
 for(const fd of [3,4]) {let link;try{link=fs.readlinkSync('/proc/self/fd/'+fd)}catch(e){if(e.code==='ENOENT')continue;throw e}a.doesNotMatch(link,/foundry-child|hvc0|ttyS0|console/)}
};
`
const pythonGuardsV2 = `import os, socket, fcntl, stat
def check(t):
 t.assertEqual((os.getuid(),os.getgid(),os.getgroups()),(65534,65534,[]))
 with open('/proc/self/status') as f: t.assertRegex(f.read(),r'(?m)^NoNewPrivs:\s+1$')
 t.assertEqual([n for _,n in socket.if_nameindex()],['lo'])
 with open('/proc/self/mountinfo') as f: t.assertNotRegex(f.read(),r' - (9p|virtiofs|nfs|cifs) ')
 for p in ['/workspace/src/triple.py','/workspace/foundry.python.lock.json','/foundry-deps/python/foundry_fixture_dep.py','/usr/bin/python3.13']:
  with t.assertRaises(OSError):
   fd=os.open(p,os.O_RDWR);os.close(fd)
 for p in ['/dev/hvc0','/dev/ttyS0','/dev/console','/proc/1/mem','/proc/1/fd/0']:
  with t.assertRaises(OSError):
   fd=os.open(p,os.O_RDONLY|os.O_NONBLOCK);os.close(fd)
 t.assertTrue(stat.S_ISFIFO(os.fstat(3).st_mode))
 t.assertEqual(fcntl.fcntl(3,fcntl.F_GETFL)&os.O_ACCMODE,os.O_WRONLY)
 # The owned adapter closes its validated sealed FD4 before project imports.
 with t.assertRaises(OSError): os.fstat(4)
`

var pythonFaultsV2 = map[string]string{
	"setup":       "import unittest\nclass TestSetup(unittest.TestCase):\n @classmethod\n def setUpClass(cls): raise RuntimeError('SYNTHETIC setup failure')\n def test_never(self): self.fail('must not run')\n",
	"discovery":   "raise RuntimeError('SYNTHETIC discovery failure')\n",
	"memory":      "import unittest\nclass TestMemory(unittest.TestCase):\n def test_memory(self):\n  blocks=[]\n  for _ in range(256): blocks.append(bytearray(16*1024*1024))\n  self.fail('memory limit not enforced')\n",
	"pids":        "import unittest,os,time,errno\nclass TestPids(unittest.TestCase):\n def test_pids(self):\n  for _ in range(256):\n   try: pid=os.fork()\n   except OSError as e:\n    if e.errno!=errno.EAGAIN: os._exit(80)\n    raise RuntimeError('SYNTHETIC pids EAGAIN reached')\n   if pid==0: time.sleep(300);os._exit(0)\n  self.fail('pids limit not enforced')\n",
	"timeout":     "import unittest,time\nclass TestDeadline(unittest.TestCase):\n def test_deadline(self): time.sleep(300)\n",
	"signal":      "import unittest,os,signal\nclass TestSignal(unittest.TestCase):\n def test_signal(self): os.kill(os.getpid(),signal.SIGKILL)\n",
	"descendant":  "import unittest,os,time\nclass TestDescendant(unittest.TestCase):\n def test_descendant(self):\n  if os.fork()==0: time.sleep(300);os._exit(0)\n  self.assertTrue(True)\n",
	"held_stream": "import unittest,os,time\nclass TestHeldStream(unittest.TestCase):\n def test_held_stream(self):\n  if os.fork()==0: time.sleep(300);os._exit(0)\n  os._exit(0)\n",
}

func (f *fixtureSpec) create(ctx context.Context, source, input string) error {
	for _, p := range []string{source, input} {
		if e := platformfs.PrivateDirectory(p); e != nil {
			return e
		}
	}
	if f.profile == "c4_python_model_v2" {
		wheel, e := fixtureWheelV2()
		if e != nil {
			return e
		}
		name := "foundry_fixture_dep-1.0-py3-none-any.whl"
		if e = os.WriteFile(filepath.Join(input, name), wheel, 0644); e != nil {
			return e
		}
		lock := map[string]any{"schema_version": 1, "python": "3.13.5", "selected_extras": []string{}, "roots": []any{map[string]string{"name": "foundry-fixture-dep", "version": "1.0"}}, "wheels": []any{map[string]any{"name": "foundry-fixture-dep", "version": "1.0", "filename": name, "sha256": digestBytes(wheel), "size": len(wheel)}}}
		raw, e := json.Marshal(lock)
		if e != nil {
			return e
		}
		f.files["foundry.python.lock.json"] = string(raw)
	}
	for root, files := range map[string]map[string]string{source: f.files, input: f.dependencies} {
		for p, b := range files {
			target := filepath.Join(root, filepath.FromSlash(p))
			if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return e
			}
			if e := os.WriteFile(target, []byte(b), 0644); e != nil {
				return e
			}
		}
	}
	git, e := gitexec.New()
	if e != nil {
		return e
	}
	defer git.Close()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "--", "."}, {"-c", "user.name=SYNTHETIC acceptance", "-c", "user.email=synthetic@example.invalid", "commit", "-qm", "SYNTHETIC disposable v2 native acceptance input"}} {
		cmd := exec.CommandContext(ctx, git.Path, append([]string{"-c", "core.hooksPath=" + git.Hooks, "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)...)
		cmd.Env = git.Environment
		cmd.Dir = source
		if e = cmd.Run(); e != nil {
			return errors.New("disposable_repository_creation_failed")
		}
	}
	return nil
}
func fixtureWheelV2() ([]byte, error) {
	prefix := "foundry_fixture_dep-1.0.dist-info/"
	files := map[string]string{"foundry_fixture_dep.py": "def identity(n):\n return n\n", prefix + "METADATA": "Metadata-Version: 2.4\nName: foundry-fixture-dep\nVersion: 1.0\n\nRepository-owned synthetic fixture.\n", prefix + "WHEEL": "Wheel-Version: 1.0\nGenerator: Foundry acceptance fixture\nRoot-Is-Purelib: true\nTag: py3-none-any\n", prefix + "licenses/LICENSE": "Repository-owned synthetic acceptance fixture; CC0-1.0.\n"}
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	var record bytes.Buffer
	csvw := csv.NewWriter(&record)
	for _, p := range names {
		sum := sha256.Sum256([]byte(files[p]))
		if e := csvw.Write([]string{p, "sha256=" + base64.RawURLEncoding.EncodeToString(sum[:]), fmt.Sprint(len(files[p]))}); e != nil {
			return nil, e
		}
	}
	if e := csvw.Write([]string{prefix + "RECORD", "", ""}); e != nil {
		return nil, e
	}
	csvw.Flush()
	if e := csvw.Error(); e != nil {
		return nil, e
	}
	files[prefix+"RECORD"] = record.String()
	names = append(names, prefix+"RECORD")
	sort.Strings(names)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, p := range names {
		h := &zip.FileHeader{Name: p, Method: zip.Deflate}
		h.SetMode(0644)
		w, e := zw.CreateHeader(h)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write([]byte(files[p])); e != nil {
			return nil, e
		}
	}
	if e := zw.Close(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func (f fixtureSpec) protectedDigests(root string) (map[string]string, error) {
	out := map[string]string{}
	for p := range f.files {
		if p == f.path {
			continue
		}
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if e != nil {
			return nil, e
		}
		out[p] = digestBytes(b)
	}
	return out, nil
}
func (f fixtureSpec) fileDigests() map[string]string {
	out := map[string]string{}
	for p, b := range f.files {
		out[p] = digestBytes([]byte(b))
	}
	return out
}
func (f fixtureSpec) deliveryPatchMarker() string { return "identity(n) + identity(n) + identity(n)" }
