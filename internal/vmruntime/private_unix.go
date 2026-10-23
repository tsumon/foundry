//go:build !windows

package vmruntime

import "os"

func createPrivateDirectory(path string) error { return os.Mkdir(path, 0700) }
