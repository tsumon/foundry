package workspace

import (
	"context"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestCommittedV2ContractAndMetadata(t *testing.T) {
	repo := repository(t)
	for p, b := range map[string]string{"package.json": `{"name":"app","version":"1.0.0"}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"app","version":"1.0.0"}}}`, "tests/a.test.js": "test('synthetic',()=>{});", "tests/helper.js": "module.exports=1;"} {
		if err := os.WriteFile(filepath.Join(repo, p), []byte(b), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "v2")
	commit := git(t, repo, "rev-parse", "HEAD")
	s := domain.RepositoryImportSpec{Scope: []string{"src/"}, Protected: []string{}, Acceptance: []string{"/opt/foundry/verify-node-v2"}, VerificationV2: &domain.C4ImportSelectionV2{ProfileID: "c4_node_model_v2", AdmissionDigest: strings.Repeat("a", 64), SourceRoot: ".", TestRoot: "tests", TestSupportPrefixes: []string{}, Dependencies: domain.C4DependencyBindingV2{SchemaVersion: 1, Ecosystem: "node", BundleDigest: strings.Repeat("b", 64), ManifestDigest: strings.Repeat("c", 64)}}}
	metadata, err := CommittedDependencyMetadata(context.Background(), repo, commit, "c4_node_model_v2")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte("dirty"), 0644)
	again, err := CommittedDependencyMetadata(context.Background(), repo, commit, "c4_node_model_v2")
	if err != nil || !reflect.DeepEqual(metadata, again) {
		t.Fatal("dirty metadata selected", err)
	}
	facts, err := InspectCommittedTreeV2(context.Background(), repo, commit, s)
	if err != nil {
		t.Fatal(err)
	}
	c := facts.VerificationV2
	if c == nil || len(c.TestFiles) != 1 || len(c.Protected) < 5 || c.SourceRoots[0] != "." {
		t.Fatalf("contract %+v", c)
	}
	if len(facts.ContextManifest) != 1 || facts.ContextManifest[0].Path != "src/main.txt" {
		t.Fatal("source root became edit context")
	}
	s.VerificationV2.TestRoot = "test"
	if _, err = InspectCommittedTreeV2(context.Background(), repo, commit, s); err == nil {
		t.Fatal("empty tests admitted")
	}
}
func TestCommittedV2TSDeclarationAndFrozenInventory(t *testing.T) {
	repo := repository(t)
	files := map[string]string{"src/main.ts": "export const value:number=1;", "src/types.d.ts": "declare const typeOnly:number;", "tests/a.test.ts": "// synthetic test", "package.json": `{"name":"app","version":"1.0.0"}`, "package-lock.json": `{"lockfileVersion":3,"packages":{"":{"name":"app","version":"1.0.0"}}}`, "tsconfig.json": `{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"noEmitOnError":true,"allowJs":false,"skipLibCheck":false},"files":["src/main.ts","src/types.d.ts","tests/a.test.ts"]}`}
	for p, b := range files {
		os.WriteFile(filepath.Join(repo, p), []byte(b), 0644)
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "synthetic TS")
	commit := git(t, repo, "rev-parse", "HEAD")
	spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Protected: []string{}, Acceptance: []string{"/opt/foundry/verify-typescript-v2"}, VerificationV2: &domain.C4ImportSelectionV2{ProfileID: "c4_typescript_model_v2", AdmissionDigest: strings.Repeat("a", 64), SourceRoot: "src", TestRoot: "tests", TestSupportPrefixes: []string{}, Dependencies: domain.C4DependencyBindingV2{SchemaVersion: 1, Ecosystem: "node", BundleDigest: strings.Repeat("b", 64), ManifestDigest: strings.Repeat("c", 64)}}}
	facts, err := InspectCommittedTreeV2(context.Background(), repo, commit, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.VerificationV2.TypeScript.InputFiles) != 3 {
		t.Fatal("declaration omitted")
	}
	tree, err := readCommittedTree(context.Background(), repo, commit, spec.Scope)
	if err != nil {
		t.Fatal(err)
	}
	inventory := []domain.C4InputFileV2{}
	for _, f := range tree.files {
		inventory = append(inventory, domain.C4InputFileV2{Path: f.p, Mode: uint32(f.mode), Bytes: int64(len(f.content)), SHA256: hash(f.content)})
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
	for _, p := range []string{"src/new.ts", "tests/new-helper.txt"} {
		candidate := append(append([]domain.C4InputFileV2{}, inventory...), domain.C4InputFileV2{Path: p, Mode: 0644, Bytes: 1, SHA256: strings.Repeat("a", 64)})
		sort.Slice(candidate, func(i, j int) bool { return candidate[i].Path < candidate[j].Path })
		if facts.VerificationV2.ValidateSourceInventory(candidate) == nil {
			t.Fatal("candidate bypass", p)
		}
	}
	os.WriteFile(filepath.Join(repo, "src/unlisted.ts"), []byte("export const hidden=1;"), 0644)
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "unlisted")
	commit = git(t, repo, "rev-parse", "HEAD")
	if _, err = InspectCommittedTreeV2(context.Background(), repo, commit, spec); err == nil {
		t.Fatal("unlisted TS inspected")
	}
}
func TestCommittedDependencyMetadataLargeLockNotContext(t *testing.T) {
	repo := repository(t)
	body := `{"lockfileVersion":3,"packages":{}}` + strings.Repeat(" ", 300<<10)
	os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte(body), 0644)
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "large lock")
	commit := git(t, repo, "rev-parse", "HEAD")
	metadata, err := CommittedDependencyMetadata(context.Background(), repo, commit, "c4_node_model_v2")
	if err != nil || len(metadata[0].Data) != len(body) {
		t.Fatal("lockfile became model context", err)
	}
}
func TestTypeScriptConfigBoundMatchesOwnedCompiler(t *testing.T) {
	files := []string{"src/a.ts"}
	config := []byte(`{"compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":true,"noEmitOnError":true,"allowJs":false,"skipLibCheck":false},"files":["src/a.ts"]}` + strings.Repeat(" ", 1<<20))
	if validTSConfig(config, files) {
		t.Fatal("config exceeds compiler bound")
	}
}
func TestCommittedV2RejectsNativeSourceLayout(t *testing.T) {
	repo := repository(t)
	for p, b := range map[string]string{"package-lock.json": "{}", "tests/a.test.js": "//synthetic", "src/native.c": "int main(void){return 0;}"} {
		os.WriteFile(filepath.Join(repo, p), []byte(b), 0644)
	}
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "native source")
	spec := domain.RepositoryImportSpec{Scope: []string{"src/"}, Acceptance: []string{"/opt/foundry/verify-node-v2"}, VerificationV2: &domain.C4ImportSelectionV2{ProfileID: "c4_node_model_v2", AdmissionDigest: strings.Repeat("a", 64), SourceRoot: ".", TestRoot: "tests", TestSupportPrefixes: []string{}, Dependencies: domain.C4DependencyBindingV2{SchemaVersion: 1, Ecosystem: "node", BundleDigest: strings.Repeat("b", 64), ManifestDigest: strings.Repeat("c", 64)}}}
	if _, err := InspectCommittedTreeV2(context.Background(), repo, git(t, repo, "rev-parse", "HEAD"), spec); err == nil {
		t.Fatal("native source admitted")
	}
}
