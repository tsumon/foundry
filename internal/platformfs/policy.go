// Package platformfs contains the deliberately narrow native filesystem boundary.
package platformfs

import (
	"errors"
	"os"
	"strings"
	"unicode"
)

func WindowsRelative(p string) error {
	if p == "" || strings.ContainsAny(p, `:*?"<>|~`) || strings.ContainsFunc(p, unicode.IsControl) {
		return errors.New("unsupported Windows path")
	}
	for _, part := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return errors.New("Windows path alias refused")
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		runes := []rune(base)
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(runes) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("123456789¹²³", runes[3])) {
			return errors.New("Windows device name refused")
		}
	}
	return nil
}

func WindowsAbsolute(p string) error {
	if len(p) < 4 || !((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) || p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return errors.New("drive-absolute Windows path required")
	}
	return WindowsRelative(p[3:])
}

func WindowsMode(mode os.FileMode) (os.FileMode, error) {
	if !mode.IsRegular() {
		return 0, errors.New("regular file required")
	}
	switch mode.Perm() {
	case 0666:
		return 0644, nil
	case 0444:
		return 0444, nil
	}
	return 0, errors.New("unsupported Windows file mode")
}
