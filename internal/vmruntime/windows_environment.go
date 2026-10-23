package vmruntime

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf16"
)

// Build only explicit child values plus the OS-reported critical directory.
// This never reads the ambient environment or allows a caller override.
func windowsEnvironment(values []string, systemRoot string) ([]uint16, error) {
	if len(systemRoot) < 3 || !((systemRoot[0] >= 'A' && systemRoot[0] <= 'Z') || (systemRoot[0] >= 'a' && systemRoot[0] <= 'z')) || systemRoot[1:3] != ":\\" || strings.ContainsRune(systemRoot, 0) {
		return nil, errors.New("invalid system Windows directory")
	}
	seen := map[string]bool{"SYSTEMROOT": true}
	entries := make([]string, 0, len(values)+1)
	for _, value := range values {
		name, _, ok := strings.Cut(value, "=")
		key := strings.ToUpper(name)
		if !ok || name == "" || strings.ContainsRune(value, 0) || seen[key] {
			return nil, errors.New("invalid explicit Windows environment")
		}
		seen[key] = true
		entries = append(entries, value)
	}
	entries = append(entries, "SYSTEMROOT="+systemRoot)
	sort.Slice(entries, func(i, j int) bool { return strings.ToUpper(entries[i]) < strings.ToUpper(entries[j]) })
	var block []uint16
	for _, value := range entries {
		block = append(block, utf16.Encode([]rune(value))...)
		block = append(block, 0)
	}
	return append(block, 0), nil
}
