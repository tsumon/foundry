package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"foundry/internal/vmruntime"
	"io"
	"path/filepath"
)

// A local maintenance operation, never an HTTP/renderer capability. Imports a
// previously verified bundle only against an explicit complete expected identity.
// It cannot start the controller, access account stores, or execute any runtime.
func retainDesktopRuntime(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "--retain-vm-bundle" {
		return false, nil
	}
	if len(args) != 6 || args[2] != "--runtime-archive" || args[4] != "--expected-vm-identity" {
		return true, errors.New("invalid runtime retention options")
	}
	if !filepath.IsAbs(args[5]) || filepath.Clean(args[5]) != args[5] {
		return true, errors.New("absolute expected identity file required")
	}
	f, err := platformfs.OpenRegular(args[5])
	if err != nil {
		return true, errors.New("cannot read expected runtime identity")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	f.Close()
	if err != nil || len(b) > 4096 {
		return true, errors.New("invalid expected runtime identity")
	}
	var expected domain.C4VMIdentity
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&expected) != nil || d.Decode(new(any)) != io.EOF || expected.Validate() != nil {
		return true, errors.New("invalid expected runtime identity")
	}
	_, err = vmruntime.Retain(args[1], args[3], expected)
	return true, err
}
