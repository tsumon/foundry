package platformfs

import (
	"os"
	"testing"
)

func TestWindowsPathAdmissionRejectsNamespaceAndAliases(t *testing.T) {
	for _, p := range []string{`C:\COM¹.txt`, `C:\LPT²`, `C:\COM³`} {
		if WindowsAbsolute(p) == nil {
			t.Errorf("admitted reserved superscript device %q", p)
		}
	}
	for _, p := range []string{`\\server\share\a`, `\\?\C:\a`, `C:relative`, `C:\a:stream`, `C:\CON.txt`, `C:\a\NUL`, `C:\COM1`, `C:\LPT9.txt`, `C:\a.`, `C:\a `, `C:\a\..\b`, `C:\a\b~1`} {
		if WindowsAbsolute(p) == nil {
			t.Errorf("admitted alias %q", p)
		}
	}
	for _, p := range []string{`C:\Users\用户\Source Folder`, `D:/source/src/main.go`} {
		if err := WindowsAbsolute(p); err != nil {
			t.Errorf("rejected supported path %q: %v", p, err)
		}
	}
}

func TestWindowsModesPreserveReadonlyAndRejectUnsupported(t *testing.T) {
	for _, tc := range []struct{ in, want os.FileMode }{{0666, 0644}, {0444, 0444}} {
		got, err := WindowsMode(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("mode %o => %o %v", tc.in, got, err)
		}
	}
	for _, mode := range []os.FileMode{0755, 0644, 0600, 0, os.ModeSymlink | 0666} {
		if _, err := WindowsMode(mode); err == nil {
			t.Errorf("admitted unsupported mode %v", mode)
		}
	}
}
