//go:build !linux

package vmguest

// MainV2 cannot activate privileged guest setup on a host OS.
func MainV2() error { return errGuestV2 }
