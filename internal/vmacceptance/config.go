// Package vmacceptance runs explicitly requested native VM acceptance using
// production runtime/engine authority and a loopback-only model protocol fixture.
// It never substitutes a synthetic VM result or accesses a real credential store.
package vmacceptance

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Config struct {
	NativeVMAcceptance bool
	Bundle, DataDir    string
	Profile, Case      string
}

func (c Config) Validate() error { return c.validate(runtime.GOOS + "/" + runtime.GOARCH) }
func (c Config) validate(host string) error {
	if !c.NativeVMAcceptance {
		return errors.New("explicit_native_vm_acceptance_required")
	}
	if c.Profile != "" || c.Case != "" {
		if _, e := selectFixture(c.Profile, c.Case); e != nil {
			return e
		}
	}
	if host != "darwin/arm64" && host != "windows/amd64" {
		return errors.New("unsupported_native_host")
	}
	for _, p := range []string{c.Bundle, c.DataDir} {
		if len(p) > 4096 || !filepath.IsAbs(p) || filepath.Clean(p) != p || p == filepath.VolumeName(p)+string(filepath.Separator) || strings.ContainsAny(p, "\x00\r\n") {
			return errors.New("absolute_clean_paths_required")
		}
	}
	bundle, err := filepath.EvalSymlinks(c.Bundle)
	if err != nil || bundle != c.Bundle {
		return errors.New("canonical_installed_bundle_required")
	}
	info, err := os.Stat(bundle)
	if err != nil || !info.IsDir() {
		return errors.New("installed_bundle_directory_required")
	}
	parent := filepath.Dir(c.DataDir)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return errors.New("canonical_existing_data_parent_required")
	}
	if _, err := os.Lstat(c.DataDir); !os.IsNotExist(err) {
		return errors.New("fresh_nonexistent_data_directory_required")
	}
	rel, err := filepath.Rel(bundle, c.DataDir)
	if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("data_directory_must_be_outside_bundle")
	}
	return nil
}
