//go:build !windows

package evidence

import "os"

func publishEvidence(source, target string) error { return os.Link(source, target) }
