package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"os"
	"path/filepath"
)

func CreateRepositoryImportDirectories(data string) error {
	fsys := durableFS()
	for _, name := range []string{"repository-import-staging", "missions"} {
		if _, err := managedContainer(fsys, data, name); err != nil {
			return err
		}
	}
	return nil
}

// PrepareRepositoryImport materializes only the explicit commit into a fresh
// private staging directory. Failures retain partial artifacts, never authority.
func PrepareRepositoryImport(ctx context.Context, repo, commit, stage, generationID string, spec domain.RepositoryImportSpec) (string, domain.GenerationRef, CommittedTreeInspection, error) {
	return prepareRepositoryImport(ctx, durableFS(), repo, commit, stage, generationID, spec)
}
func prepareRepositoryImport(ctx context.Context, fsys filesystem, repo, commit, stage, generationID string, spec domain.RepositoryImportSpec) (string, domain.GenerationRef, CommittedTreeInspection, error) {
	fail := func(err error) (string, domain.GenerationRef, CommittedTreeInspection, error) {
		return "", domain.GenerationRef{}, CommittedTreeInspection{}, err
	}
	if !validHex(generationID, 32) {
		return fail(errors.New("invalid generation ID"))
	}
	if err := CheckLocation(ctx, repo, stage); err != nil {
		return fail(err)
	}
	if err := realDirectory(filepath.Dir(stage)); err != nil {
		return fail(err)
	}
	tree, err := readCommittedTree(ctx, repo, commit, spec.Scope)
	if err != nil {
		return fail(err)
	}
	facts, err := committedTreeFacts(ctx, tree, spec)
	if err != nil {
		return fail(err)
	}
	if err = os.Mkdir(stage, 0700); err != nil {
		return fail(err)
	}
	base := filepath.Join(stage, "base")
	generation := filepath.Join(stage, "generations", generationID)
	candidate := filepath.Join(generation, "tree")
	for _, root := range []string{base, candidate} {
		if err = os.MkdirAll(root, 0755); err != nil {
			return fail(err)
		}
		for _, f := range tree.files {
			if err = ctx.Err(); err != nil {
				return fail(err)
			}
			name := filepath.Join(root, f.p)
			if err = os.MkdirAll(filepath.Dir(name), 0755); err != nil {
				return fail(err)
			}
			if err = fsys.write(name, f.content, f.mode); err != nil {
				return fail(err)
			}
		}
		// Read back the entire materialized tree, including files outside context.
		files, err := readTree(root)
		if err != nil {
			return fail(err)
		}
		if len(files) != len(tree.files) {
			return fail(errors.New("import snapshot mismatch"))
		}
		handle, err := os.OpenRoot(root)
		if err != nil {
			return fail(err)
		}
		for _, f := range tree.files {
			info, statErr := handle.Stat(f.p)
			if statErr != nil || !bytes.Equal(files[f.p], f.content) || info.Mode().Perm() != f.mode {
				handle.Close()
				return fail(errors.New("import snapshot mismatch"))
			}
		}
		if err = handle.Close(); err != nil {
			return fail(err)
		}
	}
	digest, err := Digest(base)
	if err != nil {
		return fail(err)
	}
	candidateDigest, err := Digest(candidate)
	if err != nil || candidateDigest != digest {
		return fail(errors.New("import snapshot mismatch"))
	}
	ref := domain.GenerationRef{ID: generationID, Digest: digest}
	raw, err := json.Marshal(generationManifest{Version: 1, Generation: ref})
	if err != nil {
		return fail(err)
	}
	if err = fsys.write(filepath.Join(generation, "manifest.json"), raw, 0600); err != nil {
		return fail(err)
	}
	if err = VerifyGeneration(stage, ref); err != nil {
		return fail(err)
	}
	if err = syncTree(fsys, stage); err != nil {
		return fail(err)
	}
	if err = fsys.sync(filepath.Dir(stage)); err != nil {
		return fail(err)
	}
	return digest, ref, facts, nil
}

// Publication cannot replace an occupied mission; no adoption or cleanup occurs.
func PublishRepositoryImport(stage, final string, ref domain.GenerationRef, digest string) error {
	return publishRepositoryImport(durableFS(), stage, final, ref, digest)
}
func publishRepositoryImport(fsys filesystem, stage, final string, ref domain.GenerationRef, digest string) error {
	for _, p := range []string{stage, filepath.Dir(stage), filepath.Dir(final)} {
		if err := realDirectory(p); err != nil {
			return err
		}
	}
	if err := VerifyGeneration(stage, ref); err != nil {
		return err
	}
	actual, err := Digest(filepath.Join(stage, "base"))
	if err != nil || actual != digest || ref.Digest != digest {
		return errors.New("import snapshot mismatch")
	}
	if err = fsys.publish(stage, final); err != nil {
		return err
	}
	if err = fsys.sync(filepath.Dir(final)); err != nil {
		return err
	}
	return fsys.sync(filepath.Dir(stage))
}
