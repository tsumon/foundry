package vmruntime

import "golang.org/x/sys/unix"

func publishArchive(source, target string) error {
	return unix.RenamexNp(source, target, unix.RENAME_EXCL)
}
