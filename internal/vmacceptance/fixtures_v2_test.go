package vmacceptance

import (
	"context"
	"encoding/json"
	"foundry/internal/dependencyadmission"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/workspace"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestV2FixturesAdmitRealFrozenDependenciesAndCommittedInputs(t *testing.T) {
	for _, id := range []string{"c4_node_model_v2", "c4_typescript_model_v2", "c4_python_model_v2"} {
		t.Run(id, func(t *testing.T) {
			f, e := selectFixture(id, "workflow")
			if e != nil {
				t.Fatal(e)
			}
			base, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			source, input := filepath.Join(base, "source"), filepath.Join(base, "input")
			if e = f.create(context.Background(), source, input); e != nil {
				t.Fatal(e)
			}
			observation, e := gitinspect.Inspect(context.Background(), source)
			if e != nil {
				t.Fatal(e)
			}
			metadata, e := workspace.CommittedDependencyMetadata(context.Background(), source, observation.HEAD, id)
			if e != nil {
				t.Fatal(e)
			}
			store := filepath.Join(base, "store")
			if e = os.Mkdir(store, 0700); e != nil {
				t.Fatal(e)
			}
			var a dependencyadmission.AdmissionReceipt
			if id == "c4_python_model_v2" {
				a, e = dependencyadmission.ImportPython(context.Background(), store, input, metadata[0])
			} else {
				a, e = dependencyadmission.ImportNode(context.Background(), store, input, dependencyadmission.NodeSource{PackageLock: metadata[0], PackageJSON: metadata[1]})
			}
			if e != nil || a.Validate() != nil || len(a.Packages) != 1 || a.Bundle.FileCount < 3 || a.Bundle.TotalBytes == 0 {
				t.Fatalf("missing real dependency: %+v %v", a, e)
			}
			if _, e = dependencybundle.DescribeVerified(context.Background(), store, a.Bundle); e != nil {
				t.Fatal(e)
			}
			spec := domain.RepositoryImportSpec{Scope: []string{f.path}, Protected: []string{}, Acceptance: []string{f.verifier}, VerificationV2: &domain.C4ImportSelectionV2{SchemaVersion: 2, ProfileID: id, AdmissionDigest: a.Digest, Dependencies: domain.C4DependencyBindingV2(a.Bundle), SourceRoot: f.sourceRoot, TestRoot: "tests", TestSupportPrefixes: []string{}}}
			facts, e := workspace.InspectCommittedTreeV2(context.Background(), source, observation.HEAD, spec)
			if e != nil || facts.VerificationV2 == nil || len(facts.VerificationV2.TestFiles) != 1 || len(facts.VerificationV2.Protected) < 3 {
				t.Fatalf("unbound fixture: %+v %v", facts, e)
			}
			if id == "c4_typescript_model_v2" {
				if facts.VerificationV2.TypeScript == nil {
					t.Fatal("missing typed contract")
				}
				if _, e = os.Stat(filepath.Join(input, "foundry-fixture-dep", "index.d.ts")); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = os.Stat(filepath.Join(source, "node_modules")); !os.IsNotExist(e) {
				t.Fatal("dependency installed on host project")
			}
		})
	}
}
func TestV2PythonFixtureImportsFrozenWheelAndProtectedHelper(t *testing.T) {
	python := os.Getenv("FOUNDRY_BUILD_PYTHON")
	if !filepath.IsAbs(python) {
		t.Fatal("absolute trusted FOUNDRY_BUILD_PYTHON required")
	}
	f, _ := selectFixture("c4_python_model_v2", "workflow")
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	source, input := filepath.Join(root, "source"), filepath.Join(root, "input")
	if e = f.create(context.Background(), source, input); e != nil {
		t.Fatal(e)
	}
	// Import and discovery only: never run the Linux assertions or pressure cases
	// on the host. This catches a broken fixture before expensive native gates.
	script := `import importlib.util,sys,unittest
sys.path[:0]=[sys.argv[1]+'/src',sys.argv[2]+'/foundry_fixture_dep-1.0-py3-none-any.whl']
spec=importlib.util.spec_from_file_location('owned',sys.argv[1]+'/tests/test_triple.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
assert module.identity(9)==9 and module.triple(-2)==-6
assert unittest.defaultTestLoader.loadTestsFromModule(module).countTestCases()==4
`
	if output, e := exec.Command(python, "-I", "-S", "-B", "-c", script, source, input).CombinedOutput(); e != nil {
		t.Fatalf("offline fixture import: %s %v", output, e)
	}
}
func TestV2FixtureSelectionIsClosed(t *testing.T) {
	for _, q := range [][2]string{{"", "workflow"}, {"c4_go_model_v1", "workflow"}, {"c4_node_model_v2", "setup"}, {"c4_typescript_model_v2", "memory"}, {"c4_python_model_v2", "../arbitrary"}} {
		if _, e := selectFixture(q[0], q[1]); e == nil {
			t.Fatal("accepted", q)
		}
	}
	for _, q := range [][2]string{{"c4_typescript_model_v2", "typecheck"}, {"c4_python_model_v2", "setup"}, {"c4_python_model_v2", "discovery"}, {"c4_python_model_v2", "memory"}, {"c4_python_model_v2", "pids"}, {"c4_python_model_v2", "timeout"}, {"c4_python_model_v2", "signal"}, {"c4_python_model_v2", "descendant"}, {"c4_python_model_v2", "held_stream"}} {
		if _, e := selectFixture(q[0], q[1]); e != nil {
			t.Fatal(q, e)
		}
	}
}

func TestV2NodeFixtureGuardsRequirePrimaryOnlyNodeGroups(t *testing.T) {
	for _, profile := range []string{"c4_node_model_v2", "c4_typescript_model_v2"} {
		t.Run(profile, func(t *testing.T) {
			f, e := selectFixture(profile, "workflow")
			if e != nil {
				t.Fatal(e)
			}
			// Execute the actual protected guard with test-only Linux observations.
			// Never run Linux isolation checks or mutate process credentials on host.
			guard, e := json.Marshal(f.files["tests/guards.cjs"])
			if e != nil {
				t.Fatal(e)
			}
			script := `const vm=require('node:vm'),assert=require('node:assert/strict');
const guard=` + string(guard) + `;
function check(change={}) {
 const values={uid:65534,gid:65534,groups:[65534],status:'NoNewPrivs:\t1\n',network:{},mounts:'root / - tmpfs tmpfs ro\n',writable:'',readable:'',fd:'pipe:[1234]',...change};
 const bootstrap='const values='+JSON.stringify(values)+';const process={getuid:()=>values.uid,getgid:()=>values.gid,getgroups:()=>values.groups};'+
 'const fs={readFileSync:p=>p==="/proc/self/status"?values.status:values.mounts,existsSync:()=>true,'+
 'openSync:(p,mode)=>{if(p===(mode==="r+"?values.writable:values.readable))return 10;throw Error("denied")},closeSync:()=>{},readlinkSync:()=>values.fd};'+
 'const os={networkInterfaces:()=>values.network};'+
 'const require=name=>name==="node:fs"?fs:name==="node:os"?os:assert;';
 vm.runInNewContext(bootstrap+guard+'\nmodule.exports();',{module:{exports:{}},assert},{timeout:1000});
}
check();
for(const change of [{groups:[]},{groups:[65534,42]},{groups:[42]},{groups:[65534,65534]},
 {uid:0},{gid:0},{status:'NoNewPrivs:\t0\n'},{network:{eth0:[]}},{mounts:'1 / - virtiofs host rw\n'},
 ...['/workspace/src/triple.js','/workspace/src/triple.ts','/workspace/package.json','/workspace/node_modules/foundry-fixture-dep/index.js','/usr/bin/node','/foundry-build/compiled/src/triple.js'].map(writable=>({writable})),
 ...['/dev/hvc0','/dev/ttyS0','/dev/console','/proc/1/mem','/proc/1/fd/0'].map(readable=>({readable})),
 ...['foundry-child','/dev/hvc0','/dev/ttyS0','/dev/console'].map(fd=>({fd}))]) assert.throws(()=>check(change));
`
			p := filepath.Join(t.TempDir(), "guard.cjs")
			if e = os.WriteFile(p, []byte(script), 0600); e != nil {
				t.Fatal(e)
			}
			if out, e := exec.Command("node", p).CombinedOutput(); e != nil {
				t.Fatalf("offline guard observations: %s %v", out, e)
			}
		})
	}
}
