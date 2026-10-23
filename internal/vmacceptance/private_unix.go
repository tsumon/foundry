//go:build !windows

package vmacceptance

import "os"

func reserveDataRoot(path string) error { return os.Mkdir(path, 0700) }
