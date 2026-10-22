//go:build (!darwin && !linux && !windows) || (!cgo && !windows)

package workspace

import "errors"

func publishAbsent(source, target string) error {
	return errors.New("exclusive directory publication is unsupported on this platform")
}
