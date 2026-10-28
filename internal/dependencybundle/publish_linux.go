package dependencybundle

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishAbsent(r *os.Root, source, target string) error {
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return unix.Renameat2(int(f.Fd()), source, int(f.Fd()), target, unix.RENAME_NOREPLACE)
}
