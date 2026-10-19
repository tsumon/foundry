package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// CheckLocation prevents even controller-owned output from landing inside the
// source repository. Resolve existing ancestors before comparing future paths.
func CheckLocation(ctx context.Context, repo, destination string) error {
	if !filepath.IsAbs(repo) || !filepath.IsAbs(destination) {
		return errors.New("absolute source and workspace paths required")
	}
	b, err := gitCommand(ctx, repo, 8192, "rev-parse", "--show-toplevel")
	if err != nil {
		return errors.New("working Git repository required")
	}
	source, err := filepath.EvalSymlinks(strings.TrimSpace(string(b)))
	if err != nil {
		return err
	}
	current := filepath.Clean(destination)
	suffix := []string{}
	for {
		resolved, e := filepath.EvalSymlinks(current)
		if e == nil {
			current = resolved
			break
		}
		if !os.IsNotExist(e) {
			return e
		}
		parent := filepath.Dir(current)
		if parent == current {
			return e
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
	target := filepath.Join(append([]string{current}, suffix...)...)
	for _, pair := range [][2]string{{source, target}, {target, source}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return err
		}
		if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("workspace data must be outside the source repository")
		}
	}
	return nil
}
