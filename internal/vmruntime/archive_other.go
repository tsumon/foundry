//go:build !darwin && !windows

package vmruntime

import "errors"

func publishArchive(string, string) error {
	return errors.New("runtime archive unsupported on this platform")
}
