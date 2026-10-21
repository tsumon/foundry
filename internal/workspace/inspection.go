package workspace

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"foundry/internal/domain"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type CommittedTreeInspection struct {
	FileCount       int
	TotalBytes      int64
	ContextBytes    int64
	ContextManifest []domain.FileMeta
	ProtectedInputs []string
}
type committedFile struct {
	p, id   string
	size    int
	mode    os.FileMode
	content []byte
}
type committedTree struct {
	files []committedFile
	facts CommittedTreeInspection
}

// InspectCommittedTree reads only exact local committed objects. The caller must
// first establish repository identity/layout (gitinspect.Inspect for new imports).
// There is deliberately no destination parameter and no filesystem write here.
func InspectCommittedTree(ctx context.Context, repo, commit string, spec domain.RepositoryImportSpec) (CommittedTreeInspection, error) {
	tree, err := readCommittedTree(ctx, repo, commit, spec.Scope)
	if err != nil {
		return CommittedTreeInspection{}, err
	}
	return committedTreeFacts(ctx, tree, spec)
}

func committedTreeFacts(ctx context.Context, tree committedTree, spec domain.RepositoryImportSpec) (CommittedTreeInspection, error) {
	files := make(map[string][]byte, len(tree.files))
	for _, f := range tree.files {
		files[f.p] = f.content
	}
	inputs, err := acceptanceInputs(files, spec.Acceptance)
	if err != nil {
		return CommittedTreeInspection{}, domain.ImportAcceptance
	}
	protected := map[string]bool{}
	for _, p := range inputs {
		protected[p] = true
	}
	for _, f := range tree.files {
		if Protected(f.p) {
			protected[f.p] = true
		}
		for _, rule := range spec.Protected {
			if Matches(f.p, rule) {
				protected[f.p] = true
			}
		}
	}
	tree.facts.ProtectedInputs = make([]string, 0, len(protected))
	for p := range protected {
		tree.facts.ProtectedInputs = append(tree.facts.ProtectedInputs, p)
	}
	sort.Strings(tree.facts.ProtectedInputs)
	if err := ctx.Err(); err != nil {
		return CommittedTreeInspection{}, err
	}
	return tree.facts, nil
}

// A single bounded traversal owns both snapshot and inspection policy. It scans
// every entry and blob, including files outside the selected context.
func readCommittedTree(ctx context.Context, repo, commit string, scope []string) (committedTree, error) {
	zero := committedTree{}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := ValidateRules(scope); err != nil {
		return zero, domain.ImportUnsafePath
	}
	if _, err := hex.DecodeString(commit); err != nil || len(commit) != 40 && len(commit) != 64 {
		return zero, domain.ImportObjectsUnavailable
	}
	kind, err := gitCommand(ctx, repo, 32, "cat-file", "-t", commit)
	if err != nil || strings.TrimSpace(string(kind)) != "commit" {
		return zero, fmt.Errorf("%w: exact local commit required; materialize missing objects outside FOUNDRY", domain.ImportObjectsUnavailable)
	}
	b, err := gitCommand(ctx, repo, 512*1024, "ls-tree", "-rz", "--full-tree", "-l", commit)
	if err != nil {
		return zero, fmt.Errorf("%w: cannot inspect bounded local Git tree; materialize missing objects outside FOUNDRY", domain.ImportObjectsUnavailable)
	}
	tree := committedTree{facts: CommittedTreeInspection{ContextManifest: []domain.FileMeta{}}}
	seen := map[string]bool{}
	paths := map[string]treePathIdentity{}
	entries := map[string]bool{".": true}
	for _, entry := range bytes.Split(b, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := strings.SplitN(string(entry), "\t", 2)
		if len(parts) != 2 {
			return zero, domain.ImportUnsupportedEntry
		}
		fields := strings.Fields(parts[0])
		if len(fields) != 4 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return zero, domain.ImportUnsupportedEntry
		}
		p := parts[1]
		if safePath(p) != nil {
			return zero, domain.ImportUnsafePath
		}
		lower := strings.ToLower(p)
		if seen[lower] {
			return zero, domain.ImportCaseCollision
		}
		seen[lower] = true
		if err := recordTreePath(entries, p); err != nil {
			return zero, domain.ImportTreeLimit
		}
		if err := recordTreePathIdentity(paths, p); err != nil {
			return zero, domain.ImportCaseCollision
		}
		size, err := strconv.Atoi(fields[3])
		if err != nil || size < 0 {
			return zero, fmt.Errorf("%w: invalid or unavailable local blob size; materialize missing objects outside FOUNDRY", domain.ImportObjectsUnavailable)
		}
		if size > MaxSnapshotBytes {
			return zero, domain.ImportTreeLimit
		}
		mode := os.FileMode(0644)
		if fields[0] == "100755" {
			mode = 0755
		}
		tree.files = append(tree.files, committedFile{p: p, id: fields[2], size: size, mode: mode})
		tree.facts.TotalBytes += int64(size)
		if len(tree.files) > 500 || tree.facts.TotalBytes > MaxSnapshotBytes {
			return zero, domain.ImportTreeLimit
		}
	}
	for i := range tree.files {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		f := &tree.files[i]
		b, err := gitCommand(ctx, repo, f.size+1, "cat-file", "blob", f.id)
		if err != nil || len(b) != f.size {
			return zero, fmt.Errorf("%w: bounded local blob read failed; materialize missing objects outside FOUNDRY", domain.ImportObjectsUnavailable)
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return zero, domain.ImportBinary
		}
		f.content = b
		include := false
		for _, r := range scope {
			include = include || Matches(f.p, r)
		}
		if include {
			tree.facts.ContextManifest = append(tree.facts.ContextManifest, domain.FileMeta{Path: f.p, SHA256: hash(b), Bytes: int64(len(b))})
			tree.facts.ContextBytes += int64(len(b))
			if len(tree.facts.ContextManifest) > 30 || tree.facts.ContextBytes > 200*1024 {
				return zero, domain.ImportContextLimit
			}
		}
	}
	if len(tree.facts.ContextManifest) == 0 {
		return zero, domain.ImportEmptyContext
	}
	tree.facts.FileCount = len(tree.files)
	return tree, nil
}
