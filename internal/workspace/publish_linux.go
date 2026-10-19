//go:build linux && cgo

package workspace

/*
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <errno.h>
#include <fcntl.h>
static int publish_absent(const char *source, const char *target) {
 if (renameat2(AT_FDCWD, source, AT_FDCWD, target, RENAME_NOREPLACE) == 0) return 0;
 return errno;
}
*/
import "C"

import (
	"os"
	"syscall"
	"unsafe"
)

func publishAbsent(source, target string) error {
	a, b := C.CString(source), C.CString(target)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(b))
	if code := C.publish_absent(a, b); code != 0 {
		return &os.LinkError{Op: "publish", Old: source, New: target, Err: syscall.Errno(code)}
	}
	return nil
}
