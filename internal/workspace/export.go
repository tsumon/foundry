package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func Export(ctx context.Context, dir, report string) (string, string, error) {
	patch, e := generatePatch(ctx, dir, "candidate")
	if e != nil {
		return "", "", e
	}
	tmp, e := os.MkdirTemp(dir, "delivery-stage-")
	if e != nil {
		return "", "", e
	}
	defer os.RemoveAll(tmp)
	if e := os.WriteFile(filepath.Join(tmp, "changes.patch"), patch, 0600); e != nil {
		return "", "", e
	}
	if e := os.WriteFile(filepath.Join(tmp, "report.md"), []byte(report), 0600); e != nil {
		return "", "", e
	}
	final := filepath.Join(dir, "delivery")
	if e := os.Rename(tmp, final); e != nil {
		return "", "", e
	}
	return filepath.Join(final, "changes.patch"), filepath.Join(final, "report.md"), nil
}

func generatePatch(ctx context.Context, dir, candidate string) ([]byte, error) {
	baseDigest, e := Digest(filepath.Join(dir, "base"))
	if e != nil {
		return nil, e
	}
	candidateDigest, e := Digest(filepath.Join(dir, candidate))
	if e != nil {
		return nil, e
	}
	b, stderr, e := gitCommandOutput(ctx, dir, 24*1024*1024, "diff", "--no-index", "--no-renames", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "--", "base", candidate)
	if e := checkPatchResult(b, stderr, e, baseDigest != candidateDigest); e != nil {
		return nil, e
	}
	return normalizePatch(b, candidate)
}

func checkPatchResult(b, stderr []byte, e error, different bool) error {
	var exit *exec.ExitError
	// --no-index uses exit 1 both for differences and some filesystem errors.
	// Never seal missing/partial output as a successful delivery. Keep stderr
	// private: it can contain paths or source data, so return only fixed errors.
	if len(stderr) != 0 || (e != nil && (!errors.As(e, &exit) || exit.ExitCode() != 1)) {
		return errors.New("patch generation failed")
	}
	if len(b) == 0 && (e != nil || different) {
		return errors.New("patch generation returned no diff for changed trees or exit status")
	}
	return nil
}

func normalizePatch(b []byte, candidate string) ([]byte, error) {
	lines := strings.Split(string(b), "\n")
	inHunk := false
	for i, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			inHunk = false
		}
		if strings.HasPrefix(line, "@@") {
			inHunk = true
		}
		if !inHunk {
			var err error
			lines[i], err = normalizeHeader(line, "base/", candidate+"/")
			if err != nil {
				return nil, err
			}
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// Strip only the controller-added directory immediately after a/ or b/.
// Legitimate later components named a/base or b/candidate remain untouched.
func normalizePath(p, side string, prefixes ...string) (string, string, error) {
	if len(prefixes) == 0 {
		prefixes = []string{"base/", "candidate/"}
	}
	quoted := strings.HasPrefix(p, `"`)
	if quoted {
		decoded, err := strconv.Unquote(p)
		if err != nil {
			return "", "", err
		}
		p = decoded
	}
	for _, dir := range prefixes {
		prefix := side + "/" + dir
		if strings.HasPrefix(p, prefix) {
			rel := strings.TrimPrefix(p, prefix)
			out := side + "/" + rel
			if quoted {
				out = strconv.Quote(out)
			}
			return out, rel, nil
		}
	}
	return "", "", errors.New("unexpected diff path prefix")
}
func normalizeHeader(line string, prefixes ...string) (string, error) {
	for _, header := range []struct{ prefix, side string }{{"--- ", "a"}, {"+++ ", "b"}} {
		if strings.HasPrefix(line, header.prefix) {
			p := strings.TrimPrefix(line, header.prefix)
			if p == "/dev/null" {
				return line, nil
			}
			out, _, err := normalizePath(p, header.side, prefixes...)
			return header.prefix + out, err
		}
	}
	if !strings.HasPrefix(line, "diff --git ") {
		return line, nil
	}
	pair := strings.TrimPrefix(line, "diff --git ")
	// Git can leave spaces unquoted. Match the same relative path on each side
	// rather than splitting on the first space or rewriting inside either path.
	for i, r := range pair {
		if r != ' ' {
			continue
		}
		left, l, le := normalizePath(pair[:i], "a", prefixes...)
		right, r, re := normalizePath(pair[i+1:], "b", prefixes...)
		if le == nil && re == nil && l == r {
			return "diff --git " + left + " " + right, nil
		}
	}
	return "", errors.New("unexpected diff header paths")
}
