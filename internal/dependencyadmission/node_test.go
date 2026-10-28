package dependencyadmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/dependencybundle"
	"foundry/internal/platformfs"
)

func write(t *testing.T, root, p string, b []byte) {
	t.Helper()
	p = filepath.Join(root, filepath.FromSlash(p))
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0644); e != nil {
		t.Fatal(e)
	}
}
func encode(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func setup(t *testing.T) (string, string) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	store := filepath.Join(base, "store")
	tree := filepath.Join(base, "node_modules")
	for _, p := range []string{store, tree} {
		if e = platformfs.PrivateDirectory(p); e != nil {
			t.Fatal(e)
		}
	}
	return store, tree
}
func nodeFixture(t *testing.T, root map[string]any, pkgs map[string]map[string]any) (string, string, NodeSource) {
	t.Helper()
	store, tree := setup(t)
	entries := map[string]any{"": root}
	for loc, p := range pkgs {
		entries[loc] = p
		write(t, tree, strings.TrimPrefix(loc, "node_modules/")+"/package.json", encode(p))
		write(t, tree, strings.TrimPrefix(loc, "node_modules/")+"/index.js", []byte("module.exports = 1;\n"))
	}
	return store, tree, NodeSource{PackageJSON: SourceBytes{Path: "package.json", Data: encode(root)}, PackageLock: SourceBytes{Path: "package-lock.json", Data: encode(map[string]any{"lockfileVersion": 3, "packages": entries})}}
}
func importFixture(t *testing.T, root map[string]any, pkgs map[string]map[string]any) AdmissionReceipt {
	t.Helper()
	store, tree, src := nodeFixture(t, root, pkgs)
	r, e := ImportNode(context.Background(), store, tree, src)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Validate(); e != nil {
		t.Fatal(e)
	}
	if e = dependencybundle.Verify(context.Background(), store, r.Bundle); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestNodeNestedScopedGraphAndReceipt(t *testing.T) {
	r := importFixture(t, map[string]any{"name": "app", "version": "1.0.0", "dependencies": map[string]string{"@s/a": "^1.0.0", "b": "~2.0"}, "devDependencies": map[string]string{"b": ">=2 <3"}}, map[string]map[string]any{
		"node_modules/@s/a":                {"name": "@s/a", "version": "1.2.0", "dependencies": map[string]string{"b": "^1.0.0"}, "devDependencies": map[string]string{"unused": "git:inert"}, "license": map[string]string{"type": "MIT", "url": "https://example.invalid/license"}},
		"node_modules/@s/a/node_modules/b": {"name": "b", "version": "1.1.0"},
		"node_modules/b":                   {"name": "b", "version": "2.0.1"},
	})
	if r.Policy != "node-lock-v3-ranges-v1" || r.Provenance != "user_supplied_tree" || len(r.SourceFiles) != 2 || len(r.Packages) != 3 || len(r.Edges) != 4 {
		t.Fatalf("receipt %+v", r)
	}
	if r.Edges[3].From != "node_modules/@s/a" || r.Edges[3].To != "node_modules/@s/a/node_modules/b" {
		t.Fatalf("nested edge %+v", r.Edges)
	}
	for _, p := range r.Packages {
		if p.ArtifactSHA256 != "" || p.ArtifactSize != 0 || p.ArtifactClaim != "unverified_lock_claim" {
			t.Fatal(p)
		}
	}
	changed := r
	changed.Provenance = "registry_verified"
	if changed.Validate() == nil {
		t.Fatal("accepted altered provenance")
	}
	changed = r
	changed.Edges = append([]DependencyEdge{}, r.Edges...)
	changed.Edges[0].Requirement = "1.0.0"
	if changed.Validate() == nil {
		t.Fatal("accepted altered graph")
	}
	raw := string(encode(r))
	if strings.Contains(raw, "store") || strings.Contains(raw, "node_modulesDir") {
		t.Fatal("external path in receipt")
	}
}
func TestNodeEmptyBindsExactSourceLock(t *testing.T) {
	store, tree, src := nodeFixture(t, map[string]any{}, nil)
	r, e := ImportNode(context.Background(), store, tree, src)
	if e != nil {
		t.Fatal(e)
	}
	src.PackageLock.Data = append(src.PackageLock.Data, ' ')
	other, e := ImportNode(context.Background(), store, tree, src)
	if e != nil {
		t.Fatal(e)
	}
	if r.Bundle == other.Bundle || r.Digest == other.Digest || r.Packages == nil || r.Edges == nil {
		t.Fatal("empty receipt lost exact source lock")
	}
	if r.Validate() != nil {
		t.Fatal("empty receipt invalid")
	}
}
func TestNodeCyclesAndInertMetadata(t *testing.T) {
	importFixture(t, map[string]any{"dependencies": map[string]string{"a": "1.0.0"}, "type": "module", "exports": "./index.js", "imports": map[string]string{"#a": "a"}, "scripts": map[string]string{"test": "echo inert"}}, map[string]map[string]any{
		"node_modules/a": {"name": "a", "version": "1.0.0", "dependencies": map[string]string{"b": "1.0.0"}, "bin": "cli.js", "engines": map[string]string{"node": "^24.0.0", "npm": "not-executed"}},
		"node_modules/b": {"name": "b", "version": "1.0.0", "dependencies": map[string]string{"a": "1.0.0"}},
	})
}
func TestNodeRejectsBeforePublication(t *testing.T) {
	cases := []string{"nearest mismatch", "missing package", "extra package", "root mismatch", "root dev mismatch", "metadata name", "metadata version", "metadata dependencies", "duplicate", "case alias", "trailing", "unsupported lock", "legacy lock", "v2", "missing root", "bad location", "prerelease", "hook", "platform", "peer", "optional", "workspace", "lock flag", "engine syntax", "engine mismatch", "native suffix", "native magic", "native hidden lock", "binding.gyp", "shrinkwrap", "bin directory", "unowned file", "bad source path", "root limit", "metadata limit", "credential URL", "bad integrity"}
	for _, kind := range cases {
		t.Run(kind, func(t *testing.T) {
			root := map[string]any{"dependencies": map[string]string{"a": "1.0.0"}}
			pkgs := map[string]map[string]any{"node_modules/a": {"name": "a", "version": "1.0.0"}}
			store, tree, src := nodeFixture(t, root, pkgs)
			var lock map[string]any
			json.Unmarshal(src.PackageLock.Data, &lock)
			entries := lock["packages"].(map[string]any)
			p := entries["node_modules/a"].(map[string]any)
			switch kind {
			case "nearest mismatch":
				root["dependencies"] = map[string]string{"a": "1.0.0", "b": "2.0.0"}
				p["dependencies"] = map[string]string{"b": "^2.0.0"}
				entries[""] = root
				entries["node_modules/b"] = map[string]any{"name": "b", "version": "2.0.0"}
				entries["node_modules/a/node_modules/b"] = map[string]any{"name": "b", "version": "1.0.0"}
				write(t, tree, "a/package.json", encode(p))
				write(t, tree, "b/package.json", encode(entries["node_modules/b"]))
				write(t, tree, "a/node_modules/b/package.json", encode(entries["node_modules/a/node_modules/b"]))
				src.PackageJSON.Data = encode(root)
			case "missing package":
				os.RemoveAll(filepath.Join(tree, "a"))
			case "extra package":
				write(t, tree, "b/package.json", []byte(`{"name":"b","version":"1.0.0"}`))
			case "root mismatch":
				src.PackageJSON.Data = []byte(`{"dependencies":{"a":"^1.0.0"}}`)
			case "root dev mismatch":
				src.PackageJSON.Data = []byte(`{"dependencies":{"a":"1.0.0"},"devDependencies":{"a":"1.0.0"}}`)
			case "metadata name":
				write(t, tree, "a/package.json", []byte(`{"name":"b","version":"1.0.0"}`))
			case "metadata version":
				write(t, tree, "a/package.json", []byte(`{"name":"a","version":"2.0.0"}`))
			case "metadata dependencies":
				write(t, tree, "a/package.json", []byte(`{"name":"a","version":"1.0.0","dependencies":{"a":"^1.0.0"}}`))
			case "duplicate":
				src.PackageJSON.Data = []byte(`{"dependencies":{},"dependencies":{"a":"1.0.0"}}`)
			case "case alias":
				src.PackageJSON.Data = []byte(`{"Dependencies":{"a":"1.0.0"}}`)
			case "trailing":
				src.PackageJSON.Data = append(src.PackageJSON.Data, []byte(` {}`)...)
			case "unsupported lock":
				lock["futureLayout"] = true
			case "legacy lock":
				lock["dependencies"] = map[string]any{"a": map[string]any{}}
			case "v2":
				lock["lockfileVersion"] = 2
			case "missing root":
				delete(entries, "")
			case "bad location":
				entries["project/node_modules/a"] = p
				delete(entries, "node_modules/a")
			case "prerelease":
				p["version"] = "1.0.0-beta"
			case "hook":
				write(t, tree, "a/package.json", []byte(`{"name":"a","version":"1.0.0","scripts":{"prepare":"anything"}}`))
			case "platform":
				p["os"] = []string{"linux"}
			case "peer":
				p["peerDependencies"] = map[string]string{"b": "1.0.0"}
			case "optional":
				p["optionalDependencies"] = map[string]string{"b": "1.0.0"}
			case "workspace":
				p["workspaces"] = []string{"x"}
			case "lock flag":
				p["hasInstallScript"] = true
			case "engine syntax":
				write(t, tree, "a/package.json", []byte(`{"name":"a","version":"1.0.0","engines":{"node":"24.x"}}`))
			case "engine mismatch":
				write(t, tree, "a/package.json", []byte(`{"name":"a","version":"1.0.0","engines":{"node":"<24"}}`))
			case "native suffix":
				write(t, tree, "a/addon.node", []byte("inert"))
			case "native magic":
				write(t, tree, "a/data", []byte{0x7f, 'E', 'L', 'F'})
			case "native hidden lock":
				write(t, tree, ".package-lock.json", []byte{0x7f, 'E', 'L', 'F'})
			case "binding.gyp":
				write(t, tree, "a/binding.gyp", []byte("{}"))
			case "shrinkwrap":
				write(t, tree, "a/npm-shrinkwrap.json", []byte("{}"))
			case "bin directory":
				write(t, tree, ".bin/a", []byte("inert"))
			case "unowned file":
				write(t, tree, "README", []byte("inert"))
			case "bad source path":
				src.PackageJSON.Path = "../package.json"
			case "root limit":
				src.PackageJSON.Data = make([]byte, (1<<20)+1)
			case "metadata limit":
				write(t, tree, "a/package.json", make([]byte, (256<<10)+1))
			case "credential URL":
				p["resolved"] = "https://user:secret@example.invalid/a.tgz"
			case "bad integrity":
				p["integrity"] = "sha512-notbase64"
			}
			src.PackageLock.Data = encode(lock)
			r, e := ImportNode(context.Background(), store, tree, src)
			if e == nil || r.Digest != "" {
				t.Fatalf("admitted %s %+v %v", kind, r, e)
			}
			if strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), tree) {
				t.Fatalf("unsafe error %v", e)
			}
			es, re := os.ReadDir(store)
			if re != nil || len(es) != 0 {
				t.Fatalf("published before validation: %v %v", es, re)
			}
		})
	}
}
func TestNodePublicationUnknownRetainsBindingWithoutAuthority(t *testing.T) {
	r := AdmissionReceipt{SchemaVersion: 1, Policy: "node-lock-v3-ranges-v1"}
	b := dependencybundle.Binding{SchemaVersion: 1, Ecosystem: "node", BundleDigest: strings.Repeat("a", 64)}
	r, e := finishReceipt(r, b, dependencybundle.ErrPublicationUnknown)
	if !errors.Is(e, dependencybundle.ErrPublicationUnknown) || r.Bundle != b || r.Digest != "" || r.Validate() == nil {
		t.Fatalf("pending %+v %v", r, e)
	}
}

func TestNodeInertClaimsAndCustomMetadata(t *testing.T) {
	root := map[string]any{"dependencies": map[string]string{"a": "1.0.0"}}
	store, tree, src := nodeFixture(t, root, map[string]map[string]any{"node_modules/a": {"name": "a", "version": "1.0.0", "resolved": "https://registry.npmjs.org/a/-/a-1.0.0.tgz", "integrity": "sha512-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="}})
	write(t, tree, "a/package.json", []byte(`{"name":"a","version":"1.0.0","license":{"type":"MIT","url":"https://example.invalid/license"},"custom":{"anything":[true,null,3]},"devDependencies":{"publisher-tool":"workspace:*"}}`))
	write(t, tree, ".package-lock.json", []byte(`inert hidden lock; no graph authority`))
	r, e := ImportNode(context.Background(), store, tree, src)
	if e != nil {
		t.Fatal(e)
	}
	if r.Bundle.FileCount != 3 || r.Packages[0].ArtifactClaim != "unverified_lock_claim" || len(r.Edges) != 1 {
		t.Fatalf("facts %+v", r)
	}
}
func TestNodeOrphanAndMalformedNestedBoundary(t *testing.T) {
	for _, kind := range []string{"orphan", "unknown nested", "nested under data", "missing metadata", "case metadata", "null metadata", "trailing metadata", "duplicate lock", "case lock", "lock null", "null dependencies", "null scripts", "null engines", "root native version"} {
		t.Run(kind, func(t *testing.T) {
			root := map[string]any{"dependencies": map[string]string{"a": "1.0.0"}}
			store, tree, src := nodeFixture(t, root, map[string]map[string]any{"node_modules/a": {"name": "a", "version": "1.0.0"}})
			switch kind {
			case "orphan":
				var lock map[string]any
				json.Unmarshal(src.PackageLock.Data, &lock)
				lock["packages"].(map[string]any)["node_modules/b"] = map[string]any{"version": "1.0.0"}
				src.PackageLock.Data = encode(lock)
				write(t, tree, "b/package.json", []byte(`{"name":"b","version":"1.0.0"}`))
			case "unknown nested":
				write(t, tree, "a/node_modules/b/package.json", []byte(`{"name":"b","version":"1.0.0"}`))
			case "nested under data":
				write(t, tree, "a/data/node_modules/b/package.json", []byte(`{"name":"b","version":"1.0.0"}`))
			case "missing metadata":
				os.Remove(filepath.Join(tree, "a/package.json"))
			case "case metadata":
				write(t, tree, "a/package.json", []byte(`{"Name":"a","version":"1.0.0"}`))
			case "null metadata":
				write(t, tree, "a/package.json", []byte(`null`))
			case "trailing metadata":
				write(t, tree, "a/package.json", []byte(`{"name":"a","version":"1.0.0"} {}`))
			case "duplicate lock":
				src.PackageLock.Data = []byte(`{"lockfileVersion":3,"packages":{"":{},"":{}}}`)
			case "case lock":
				src.PackageLock.Data = []byte(`{"lockfileVersion":3,"Packages":{"":{}}}`)
			case "lock null":
				src.PackageLock.Data = []byte(`{"lockfileVersion":3,"packages":null}`)
			case "null dependencies":
				src.PackageJSON.Data = []byte(`{"dependencies":null}`)
			case "null scripts":
				src.PackageJSON.Data = []byte(`{"scripts":null}`)
			case "null engines":
				src.PackageJSON.Data = []byte(`{"engines":null}`)
			case "root native version":
				src.PackageJSON.Data = []byte(`{"version":"1.0.0-beta","dependencies":{"a":"1.0.0"}}`)
				var lock map[string]any
				json.Unmarshal(src.PackageLock.Data, &lock)
				lock["packages"].(map[string]any)[""] = map[string]any{"version": "1.0.0-beta", "dependencies": map[string]string{"a": "1.0.0"}}
				src.PackageLock.Data = encode(lock)
			}
			if r, e := ImportNode(context.Background(), store, tree, src); e == nil {
				t.Fatalf("accepted %s: %+v", kind, r)
			}
		})
	}
}
func TestNodePackageAndEdgeLimits(t *testing.T) {
	packages := map[string]any{"": map[string]any{}}
	for i := 0; i < 513; i++ {
		packages["node_modules/p"+fmt.Sprint(i)] = map[string]any{"version": "1.0.0"}
	}
	if _, _, e := parseNodeLock(encode(map[string]any{"lockfileVersion": 3, "packages": packages})); e == nil {
		t.Fatal("accepted 513 packages")
	}
	for _, n := range []int{4096, 4097} {
		root := &nodePackage{deps: map[string]string{}, devDeps: map[string]string{}}
		pkgs := map[string]*nodePackage{}
		// Eight packages, 512 dependencies each, and one root edge: 4097 total.
		for i := 0; i < 512; i++ {
			name := "p" + fmt.Sprint(i)
			pkgs["node_modules/"+name] = &nodePackage{name: name, version: "1.0.0", deps: map[string]string{}}
		}
		for i := 0; i < 8; i++ {
			for j := 0; j < 512; j++ {
				if i*512+j < n-1 {
					pkgs["node_modules/p"+fmt.Sprint(i)].deps["p"+fmt.Sprint(j)] = "1.0.0"
				}
			}
		}
		root.deps["p0"] = "1.0.0"
		edges, e := nodeEdges(root, pkgs)
		if n == 4096 && (e != nil || len(edges) != 4096) {
			t.Fatalf("4096 edges: %d %v", len(edges), e)
		}
		if n == 4097 && e == nil {
			t.Fatal("accepted 4097 edges")
		}
	}
}
func TestStrictJSONRejectsMalformedAndDepth(t *testing.T) {
	for _, raw := range []string{`{"a":1,"A":2}`, `{"x":{"a":1,"a":2}}`, `{"x":[1,]}`, `{"x":NaN}`, `{"x":1}true`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'})} {
		if e := strictJSON([]byte(raw), maxRootJSON); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
func TestNodeReceiptBindsAllRetainedFacts(t *testing.T) {
	r := importFixture(t, map[string]any{"dependencies": map[string]string{"a": "1.0.0"}}, map[string]map[string]any{"node_modules/a": {"name": "a", "version": "1.0.0"}})
	for _, change := range []func(*AdmissionReceipt){
		func(x *AdmissionReceipt) { x.Policy = "other" }, func(x *AdmissionReceipt) { x.Bundle.TotalBytes++ }, func(x *AdmissionReceipt) { x.SourceFiles[0].SHA256 = strings.Repeat("b", 64) }, func(x *AdmissionReceipt) { x.Packages[0].MetadataSHA256 = strings.Repeat("b", 64) }, func(x *AdmissionReceipt) { x.Edges[0].To = "node_modules/missing" },
	} {
		var bad AdmissionReceipt
		json.Unmarshal(encode(r), &bad)
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted receipt mutation")
		}
	}
}

func TestNodeTopLevelLockIdentityMatchesRoot(t *testing.T) {
	for _, key := range []string{"name", "version"} {
		t.Run(key, func(t *testing.T) {
			raw := encode(map[string]any{"lockfileVersion": 3, "name": "app", "version": "1.0.0", "packages": map[string]any{"": map[string]any{"name": "app", "version": "1.0.0"}}})
			var lock map[string]any
			json.Unmarshal(raw, &lock)
			lock[key] = "different"
			if _, _, e := parseNodeLock(encode(lock)); e == nil {
				t.Fatal("accepted contradictory lock identity")
			}
		})
	}
}
func TestReceiptValidateRejectsRehashedWrongTarget(t *testing.T) {
	r := importFixture(t, map[string]any{"dependencies": map[string]string{"a": "1.0.0", "b": "2.0.0"}}, map[string]map[string]any{"node_modules/a": {"name": "a", "version": "1.0.0"}, "node_modules/b": {"name": "b", "version": "2.0.0"}})
	r.Edges[0].To = "node_modules/b"
	r.Digest = r.computeDigest()
	if r.Validate() == nil {
		t.Fatal("accepted rehashed edge to another package")
	}
}

func TestNodeMetadataExactByteBounds(t *testing.T) {
	root := []byte(`{"custom":"` + strings.Repeat("x", maxRootJSON-len(`{"custom":""}`)) + `"}`)
	if len(root) != maxRootJSON {
		t.Fatal("fixture size")
	}
	if _, e := parsePackage(root, maxRootJSON, false); e != nil {
		t.Fatal(e)
	}
	if _, e := parsePackage(append(root, ' '), maxRootJSON, false); e == nil {
		t.Fatal("accepted root limit plus one")
	}
	base := `{"name":"a","version":"1.0.0","custom":""}`
	metadata := []byte(`{"name":"a","version":"1.0.0","custom":"` + strings.Repeat("x", maxPackageJSON-len(base)) + `"}`)
	if len(metadata) != maxPackageJSON {
		t.Fatal("fixture size")
	}
	if _, e := parsePackage(metadata, maxPackageJSON, false); e != nil {
		t.Fatal(e)
	}
	if _, e := parsePackage(append(metadata, ' '), maxPackageJSON, false); e == nil {
		t.Fatal("accepted metadata limit plus one")
	}
}
func TestNode512PackagesAdmitted(t *testing.T) {
	deps := map[string]string{}
	pkgs := map[string]map[string]any{}
	for i := 0; i < 512; i++ {
		name := "p" + fmt.Sprint(i)
		deps[name] = "1.0.0"
		pkgs["node_modules/"+name] = map[string]any{"name": name, "version": "1.0.0"}
	}
	r := importFixture(t, map[string]any{"dependencies": deps}, pkgs)
	if len(r.Packages) != 512 || len(r.Edges) != 512 {
		t.Fatal("package bound")
	}
}

func TestNodeLockRootUsesRootJSONLimit(t *testing.T) {
	raw := encode(map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": map[string]any{"license": strings.Repeat("x", maxPackageJSON+1)}}})
	if _, _, e := parseNodeLock(raw); e != nil {
		t.Fatalf("root record incorrectly uses package limit: %v", e)
	}
}

func TestNodeReceiptDigestGolden(t *testing.T) {
	r := importFixture(t, map[string]any{}, nil)
	if r.Digest != "97981ba736266e1e3a04c1c43e9176410b06515ce6bdd7cfee3a3ece5f2e5cec" || r.Bundle.BundleDigest != "79e68c456bbe62d4082260581901e4cf581386fd9e40aada836f2eff85428548" {
		t.Fatalf("canonical digest %+v", r)
	}
	if r.SourceFiles[0].SHA256 != "821efc9eb6d101b9462d3e51d1aa1e4edb0a5d4a7885f7a5879665ade86e0ec7" || r.SourceFiles[1].SHA256 != "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" {
		t.Fatal("source digests")
	}
}
func TestNodeAllUnsupportedExecutionMetadata(t *testing.T) {
	for _, key := range []string{"peerDependencies", "peerDependenciesMeta", "optionalDependencies", "workspaces", "overrides", "bundledDependencies", "bundleDependencies", "os", "cpu", "libc"} {
		p := map[string]any{"name": "a", "version": "1.0.0", key: map[string]string{"b": "1.0.0"}}
		for _, locked := range []bool{false, true} {
			if _, e := parsePackage(encode(p), maxPackageJSON, locked); e == nil {
				t.Fatalf("accepted %s locked=%v", key, locked)
			}
		}
	}
	for _, key := range []string{"peer", "optional", "devOptional", "link", "inBundle", "hasShrinkwrap", "hasInstallScript"} {
		p := map[string]any{"name": "a", "version": "1.0.0", key: true}
		if _, e := parsePackage(encode(p), maxPackageJSON, true); e == nil {
			t.Fatalf("accepted flag %s", key)
		}
		p[key] = false
		if _, e := parsePackage(encode(p), maxPackageJSON, true); e != nil {
			t.Fatalf("false flag %s %v", key, e)
		}
	}
	for _, hook := range []string{"preinstall", "install", "postinstall", "prepare", "preprepare", "postprepare"} {
		if _, e := parsePackage(encode(map[string]any{"scripts": map[string]string{hook: "inert text"}}), maxRootJSON, false); e == nil {
			t.Fatalf("accepted hook %s", hook)
		}
	}
}
func TestNodeEngineErrorCodes(t *testing.T) {
	for _, x := range []struct{ spec, want string }{{"^25", "runtime_incompatible"}, {"24.x", "unsupported_engine_spec"}} {
		_, e := parsePackage(encode(map[string]any{"engines": map[string]string{"node": x.spec}}), maxRootJSON, false)
		if e == nil || e.Error() != x.want {
			t.Fatalf("%s: %v", x.spec, e)
		}
	}
	if _, e := satisfies("latest", "1.0.0"); e != errSpec {
		t.Fatalf("unsupported dependency error %v", e)
	}
}
func TestNodeAbsentEmptyMapsNormalize(t *testing.T) {
	store, tree, src := nodeFixture(t, map[string]any{}, nil)
	src.PackageLock.Data = []byte(`{"lockfileVersion":3,"packages":{"":{"dependencies":{},"devDependencies":{}}}}`)
	if _, e := ImportNode(context.Background(), store, tree, src); e != nil {
		t.Fatal(e)
	}
}
func TestNodeNativePayloadFormats(t *testing.T) {
	for _, p := range []string{"a/x.node", "a/x.so", "a/x.so.1", "a/x.dylib", "a/x.dll", "a/x.exe", "a/x.o", "a/x.a", "a/x.lib", "a/.bin/tool"} {
		if !forbiddenPayload(p, []byte("data")) {
			t.Fatalf("accepted %s", p)
		}
	}
	for _, b := range [][]byte{{0x7f, 'E', 'L', 'F'}, {'M', 'Z'}, {0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe}, {0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}, {0xbe, 0xba, 0xfe, 0xca}, {0xca, 0xfe, 0xba, 0xbf}, {0xbf, 0xba, 0xfe, 0xca}, []byte("!<arch>\n")} {
		if !forbiddenPayload("a/data", b) {
			t.Fatalf("accepted magic %x", b)
		}
	}
}

func TestNodeResolvedClaimsRejectQueries(t *testing.T) {
	for _, claim := range []string{"https://example.invalid/a.tgz?download=1", "https://example.invalid/a.tgz#fragment", "https://example.invalid/a.tgz?", "https://example.invalid/a.tgz#", "https://user:secret@example.invalid/a.tgz", "file:///tmp/a.tgz"} {
		_, e := parsePackage(encode(map[string]any{"resolved": claim}), maxPackageJSON, true)
		if e == nil || e.Error() != "unsupported_resolved_claim" || strings.Contains(e.Error(), claim) {
			t.Fatalf("resolved claim outcome %v", e)
		}
	}
}
