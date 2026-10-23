//go:build !windows

package vmruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func validateInstanceLocation(bundleRoot, instanceRoot string) error {
	rel, err := filepath.Rel(bundleRoot, instanceRoot)
	if err != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
		return errors.New("instance root must be outside immutable bundle")
	}
	return nil
}

func createPrivateDirectory(path string) error { return os.Mkdir(path, 0700) }
