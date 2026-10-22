package testutil

import "os"

// Windows has no Unix FIFO; a directory exercises the non-regular rejection.
// Native reparse/namespace/disk-handle tests live in platformfs.
func SpecialFile(p string) error { return os.Mkdir(p, 0700) }
