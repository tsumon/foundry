//go:build !linux

package vmguest

import "errors"

// Main cannot invoke a guest verifier or privileged setup on a host OS.
func Main() error { return errors.New("guest supervisor is Linux PID1 only") }
