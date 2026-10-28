package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"foundry/internal/dependencyadmission"
	"foundry/internal/domain"
	"io"
	"path"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// CommittedDependencyMetadata shares the complete bounded traversal but selects
// no model context. Large lockfiles never enter the 200 KiB context budget.
func CommittedDependencyMetadata(ctx context.Context, repo, commit, profile string) ([]dependencyadmission.SourceBytes, error) {
	tree, err := readCommittedTreeMode(ctx, repo, commit, nil, true)
	if err != nil {
		return nil, err
	}
	names := []string{"package-lock.json", "package.json"}
	if profile == "c4_python_model_v2" {
		names = []string{"foundry.python.lock.json"}
	} else if profile != "c4_node_model_v2" && profile != "c4_typescript_model_v2" {
		return nil, domain.ImportInputs
	}
	out := []dependencyadmission.SourceBytes{}
	for _, p := range names {
		found := false
		for _, f := range tree.files {
			if f.p == p {
				out = append(out, dependencyadmission.SourceBytes{Path: p, Data: append([]byte(nil), f.content...)})
				found = true
			}
		}
		if !found {
			return nil, domain.ImportDependencies
		}
	}
	return out, nil
}
func InspectCommittedTreeV2(ctx context.Context, repo, commit string, spec domain.RepositoryImportSpec) (CommittedTreeInspection, error) {
	if spec.VerificationV2 == nil {
		return CommittedTreeInspection{}, domain.ImportInputs
	}
	return InspectCommittedTree(ctx, repo, commit, spec)
}
func PrepareRepositoryImportV2(ctx context.Context, repo, commit, stage, generation string, spec domain.RepositoryImportSpec) (string, domain.GenerationRef, CommittedTreeInspection, error) {
	if spec.VerificationV2 == nil {
		return "", domain.GenerationRef{}, CommittedTreeInspection{}, domain.ImportInputs
	}
	return PrepareRepositoryImport(ctx, repo, commit, stage, generation, spec)
}
func committedTreeFactsV2(ctx context.Context, tree committedTree, spec domain.RepositoryImportSpec) (CommittedTreeInspection, error) {
	s := spec.VerificationV2
	id := s.ProfileID
	verifier := "/opt/foundry/verify-node-v2"
	language := "javascript"
	switch id {
	case "c4_node_model_v2":
	case "c4_typescript_model_v2":
		verifier = "/opt/foundry/verify-typescript-v2"
		language = "typescript"
	case "c4_python_model_v2":
		verifier = "/opt/foundry/verify-python-v2"
		language = "python"
	default:
		return CommittedTreeInspection{}, domain.ImportInputs
	}
	fail := func() (CommittedTreeInspection, error) { return CommittedTreeInspection{}, domain.ImportInputs }
	if !reflect.DeepEqual(spec.Acceptance, []string{verifier}) || spec.Image != "" || (s.SourceRoot != "." && s.SourceRoot != "src") || (s.TestRoot != "test" && s.TestRoot != "tests") || language == "python" && s.TestRoot != "tests" {
		return fail()
	}
	prefixes := append([]string{".foundry-build", ".venv", "node_modules", "site-packages", s.TestRoot}, s.TestSupportPrefixes...)
	sort.Strings(prefixes)
	prefixes = slices.Compact(prefixes)
	c := domain.C4InputContractV2{SchemaVersion: 2, ProfileID: id, Dependencies: s.Dependencies, AdmissionDigest: s.AdmissionDigest, SourceFiles: []domain.C4SourceDigestV2{}, Protected: []domain.C4InputFileV2{}, ProtectedPrefixes: prefixes, TestFiles: []string{}, SourceRoots: []string{s.SourceRoot}, HarnessVersion: "foundry-test-v2"}
	source := map[string]string{"package.json": "manifest", "package-lock.json": "lock"}
	if language == "python" {
		source = map[string]string{"foundry.python.lock.json": "lock"}
	}
	if language == "typescript" {
		source["tsconfig.json"] = "config"
	}
	inventory := []domain.C4InputFileV2{}
	tsFiles := []string{}
	var config []byte
	rootExists := s.SourceRoot == "."
	for _, f := range tree.files {
		if domain.ValidateC4SourcePathV2(f.p) != nil {
			return fail()
		}
		base := path.Base(f.p)
		if strings.HasPrefix(f.p, "src/") {
			rootExists = true
		}
		m := domain.C4InputFileV2{Path: f.p, Mode: uint32(f.mode), Bytes: int64(len(f.content)), SHA256: hash(f.content)}
		inventory = append(inventory, m)
		test := false
		if strings.HasPrefix(f.p, s.TestRoot+"/") {
			switch language {
			case "javascript":
				test = strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.mjs") || strings.HasSuffix(base, ".test.cjs")
			case "typescript":
				test = strings.HasSuffix(base, ".test.ts")
			case "python":
				test = strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")
			}
		}
		if test {
			c.TestFiles = append(c.TestFiles, f.p)
			m.Role = "test"
		} else if role := source[f.p]; role != "" {
			m.Role = role
		} else {
			protected := Protected(f.p)
			for _, rule := range spec.Protected {
				protected = protected || Matches(f.p, rule)
			}
			for _, prefix := range prefixes {
				protected = protected || f.p == prefix || strings.HasPrefix(f.p, prefix+"/")
			}
			if protected {
				m.Role = "helper"
			}
		}
		if m.Role != "" {
			c.Protected = append(c.Protected, m)
		}
		if source[f.p] != "" {
			c.SourceFiles = append(c.SourceFiles, domain.C4SourceDigestV2{Path: f.p, Bytes: m.Bytes, SHA256: m.SHA256})
		}
		if strings.HasSuffix(f.p, ".ts") {
			tsFiles = append(tsFiles, f.p)
		}
		if f.p == "tsconfig.json" {
			config = f.content
		}
	}
	if !rootExists {
		return fail()
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
	sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
	sort.Slice(c.SourceFiles, func(i, j int) bool { return c.SourceFiles[i].Path < c.SourceFiles[j].Path })
	sort.Strings(c.TestFiles)
	sort.Strings(tsFiles)
	if language == "typescript" {
		if !validTSConfig(config, tsFiles) {
			return fail()
		}
		c.TypeScript = &domain.C4TypeScriptContractV2{ConfigPath: "tsconfig.json", ConfigDigest: hash(config), InputFiles: tsFiles, OutputRoot: "/foundry-build/compiled"}
	}
	sealed, err := c.WithDigests()
	if err != nil || sealed.ValidateSourceInventory(inventory) != nil {
		return fail()
	}
	facts := tree.facts
	facts.VerificationV2 = &sealed
	facts.ProtectedInputs = []string{}
	for _, f := range sealed.Protected {
		facts.ProtectedInputs = append(facts.ProtectedInputs, f.Path)
	}
	if err := ctx.Err(); err != nil {
		return CommittedTreeInspection{}, err
	}
	return facts, nil
}

// Exact object grammar, including duplicate-key rejection, before typed decode.
func validTSConfig(raw []byte, files []string) bool {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func() (any, bool)
	value = func() (any, bool) {
		t, e := d.Token()
		if e != nil {
			return nil, false
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				m := map[string]any{}
				for d.More() {
					k, e := d.Token()
					key, ok := k.(string)
					if e != nil || !ok {
						return nil, false
					}
					if _, ok = m[key]; ok {
						return nil, false
					}
					v, ok := value()
					if !ok {
						return nil, false
					}
					m[key] = v
				}
				t, e = d.Token()
				return m, e == nil && t == json.Delim('}')
			case '[':
				a := []any{}
				for d.More() {
					v, ok := value()
					if !ok {
						return nil, false
					}
					a = append(a, v)
				}
				t, e = d.Token()
				return a, e == nil && t == json.Delim(']')
			}
		}
		return t, true
	}
	got, ok := value()
	if !ok {
		return false
	}
	if _, e := d.Token(); e != io.EOF {
		return false
	}
	list := []any{}
	for _, p := range files {
		list = append(list, p)
	}
	want := map[string]any{"compilerOptions": map[string]any{"target": "ES2022", "module": "NodeNext", "moduleResolution": "NodeNext", "strict": true, "noEmitOnError": true, "allowJs": false, "skipLibCheck": false}, "files": list}
	return reflect.DeepEqual(got, want)
}
