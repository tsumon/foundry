package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// PreparedExport describes sealed bytes, not permission or database state.
type PreparedExport struct {
	PatchPath    string `json:"patch_path"`
	ReportPath   string `json:"report_path"`
	PatchSHA256  string `json:"patch_sha256"`
	ReportSHA256 string `json:"report_sha256"`
}

const maxPatchBytes = 24 * 1024 * 1024
const maxReportBytes = 1024 * 1024

type exportManifest struct {
	Version      int                  `json:"version"`
	ID           string               `json:"id"`
	Candidate    domain.GenerationRef `json:"candidate"`
	BaseDigest   string               `json:"base_digest"`
	PatchSHA256  string               `json:"patch_sha256"`
	ReportSHA256 string               `json:"report_sha256"`
}

// PrepareExport publishes an immutable patch/report pair or reuses an exact match.
func PrepareExport(ctx context.Context, dir, id, candidate, report string) (PreparedExport, error) {
	return prepareExport(ctx, durableFS(), dir, id, candidate, report)
}
func prepareExport(ctx context.Context, fsys filesystem, dir, id, candidate, report string) (PreparedExport, error) {
	fail := func(err error) (PreparedExport, error) { return PreparedExport{}, err }
	if !validHex(id, 32) {
		return fail(errors.New("invalid export ID"))
	}
	if len(report) > maxReportBytes || !utf8.ValidString(report) || strings.ContainsRune(report, 0) {
		return fail(errors.New("report exceeds text bounds"))
	}
	if err := realDirectory(dir); err != nil {
		return fail(err)
	}
	ref, err := candidateReference(dir, candidate)
	if err != nil {
		return fail(err)
	}
	baseDigest, err := Digest(filepath.Join(dir, "base"))
	if err != nil {
		return fail(err)
	}
	rel, err := filepath.Rel(dir, candidate)
	if err != nil {
		return fail(err)
	}
	patch, err := generatePatch(ctx, dir, filepath.ToSlash(rel))
	if err != nil {
		return fail(err)
	}
	if err := VerifyGeneration(dir, ref); err != nil {
		return fail(err)
	}
	if after, err := Digest(filepath.Join(dir, "base")); err != nil || after != baseDigest {
		return fail(errors.New("base changed during export"))
	}
	seal := exportManifest{Version: 1, ID: id, Candidate: ref, BaseDigest: baseDigest, PatchSHA256: hash(patch), ReportSHA256: hash([]byte(report))}
	container, err := managedContainer(fsys, dir, "exports")
	if err != nil {
		return fail(err)
	}
	final := filepath.Join(container, id)
	result := PreparedExport{PatchPath: filepath.Join(final, "changes.patch"), ReportPath: filepath.Join(final, "report.md"), PatchSHA256: seal.PatchSHA256, ReportSHA256: seal.ReportSHA256}
	if _, err := os.Lstat(final); err == nil {
		if err := VerifyExport(dir, id, result); err != nil {
			return fail(err)
		}
		var existing exportManifest
		if err := readManifest(filepath.Join(final, "manifest.json"), &existing); err != nil {
			return fail(err)
		}
		if existing != seal {
			return fail(errors.New("existing export does not match intent"))
		}
		// Retry may follow a previous failure syncing the published parent.
		if err := fsys.sync(container); err != nil {
			return fail(err)
		}
		return result, nil
	} else if !os.IsNotExist(err) {
		return fail(err)
	}
	stage, err := os.MkdirTemp(container, ".stage-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(stage)
	patchPath := filepath.Join(stage, "changes.patch")
	if err := fsys.write(patchPath, patch, 0600); err != nil {
		return fail(err)
	}
	if err := fsys.write(filepath.Join(stage, "report.md"), []byte(report), 0600); err != nil {
		return fail(err)
	}
	// Validate the exact staged bytes against the frozen base, without applying.
	if len(patch) > 0 {
		if _, err := gitCommand(ctx, dir, 4096, "apply", "--check", "--directory=base", "--", patchPath); err != nil {
			return fail(errors.New("prepared patch does not apply to frozen base"))
		}
	}
	for _, entry := range []struct {
		name, want string
		limit      int64
	}{{"changes.patch", seal.PatchSHA256, maxPatchBytes}, {"report.md", seal.ReportSHA256, maxReportBytes}} {
		b, err := readBoundedFile(filepath.Join(stage, entry.name), entry.limit)
		if err != nil {
			return fail(err)
		}
		if hash(b) != entry.want {
			return fail(errors.New("staged export bytes changed"))
		}
	}
	if err := syncTree(fsys, stage); err != nil {
		return fail(err)
	}
	manifest, err := json.Marshal(seal)
	if err != nil {
		return fail(err)
	}
	if err := fsys.write(filepath.Join(stage, "manifest.json"), manifest, 0600); err != nil {
		return fail(err)
	}
	if err := fsys.sync(filepath.Join(stage, "manifest.json")); err != nil {
		return fail(err)
	}
	if err := fsys.sync(stage); err != nil {
		return fail(err)
	}
	if err := fsys.publish(stage, final); err != nil {
		return fail(err)
	}
	if err := fsys.sync(container); err != nil {
		return fail(err)
	}
	return result, nil
}

// VerifyExport validates managed paths and bounded bytes against recorded hashes.
// Committed-operation consumers must use VerifyExportBinding for provenance.
func VerifyExport(dir, id string, expected PreparedExport) error {
	return verifyExport(dir, id, expected, nil, "")
}

// VerifyExportBinding additionally checks the seal's provenance against the
// authoritative committed operation, not merely the shape of its digests.
func VerifyExportBinding(dir, id string, expected PreparedExport, candidate domain.GenerationRef, baseDigest string) error {
	if !validHex(candidate.ID, 32) || !validHex(candidate.Digest, 64) || !validHex(baseDigest, 64) {
		return errors.New("invalid export binding")
	}
	return verifyExport(dir, id, expected, &candidate, baseDigest)
}

func verifyExport(dir, id string, expected PreparedExport, candidate *domain.GenerationRef, baseDigest string) error {
	if !validHex(id, 32) || !validHex(expected.PatchSHA256, 64) || !validHex(expected.ReportSHA256, 64) {
		return errors.New("invalid export reference")
	}
	final := filepath.Join(dir, "exports", id)
	for _, p := range []string{dir, filepath.Join(dir, "exports"), final} {
		if err := realDirectory(p); err != nil {
			return err
		}
	}
	if expected.PatchPath != filepath.Join(final, "changes.patch") || expected.ReportPath != filepath.Join(final, "report.md") {
		return errors.New("export paths do not match managed identity")
	}
	if err := exactEntries(final, "changes.patch", "report.md", "manifest.json"); err != nil {
		return err
	}
	var seal exportManifest
	if err := readManifest(filepath.Join(final, "manifest.json"), &seal); err != nil {
		return err
	}
	if seal.Version != 1 || seal.ID != id || seal.PatchSHA256 != expected.PatchSHA256 || seal.ReportSHA256 != expected.ReportSHA256 || !validHex(seal.BaseDigest, 64) || !validHex(seal.Candidate.ID, 32) || !validHex(seal.Candidate.Digest, 64) {
		return errors.New("export seal mismatch")
	}
	if candidate != nil && (seal.Candidate != *candidate || seal.BaseDigest != baseDigest) {
		return errors.New("export seal provenance differs from committed operation")
	}
	for _, entry := range []struct {
		path, want string
		limit      int64
	}{{expected.PatchPath, expected.PatchSHA256, maxPatchBytes}, {expected.ReportPath, expected.ReportSHA256, maxReportBytes}} {
		b, err := readBoundedFile(entry.path, entry.limit)
		if err != nil {
			return err
		}
		if hash(b) != entry.want {
			return errors.New("export artifact digest mismatch")
		}
	}
	return nil
}

func candidateReference(dir, candidate string) (domain.GenerationRef, error) {
	fail := func(err error) (domain.GenerationRef, error) { return domain.GenerationRef{}, err }
	rel, err := filepath.Rel(filepath.Join(dir, "generations"), candidate)
	if err != nil {
		return fail(err)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || !validHex(parts[0], 32) || parts[1] != "tree" || filepath.Clean(candidate) != candidate {
		return fail(errors.New("export requires managed generation tree"))
	}
	ref := domain.GenerationRef{ID: parts[0], Digest: strings.Repeat("0", 64)}
	if _, err := GenerationPath(dir, ref); err != nil {
		return fail(err)
	}
	var seal generationManifest
	if err := readManifest(filepath.Join(filepath.Dir(candidate), "manifest.json"), &seal); err != nil {
		return fail(err)
	}
	if seal.Generation.ID != parts[0] {
		return fail(errors.New("generation identity mismatch"))
	}
	if err := VerifyGeneration(dir, seal.Generation); err != nil {
		return fail(err)
	}
	return seal.Generation, nil
}
