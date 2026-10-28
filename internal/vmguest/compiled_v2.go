package vmguest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Output validation occurs after the compiler's group is positively empty.
// The sole excluded subtree is the separately checked immutable dependency bind.
func compiledInventoryV2(root string, inputs []string) (string, error) {
	expected := map[string]bool{"package.json": true}
	dirs := map[string]bool{".": true, "node_modules": true}
	for _, p := range inputs {
		if !relativeV2(p) || !strings.HasSuffix(p, ".ts") {
			return "", errGuestV2
		}
		if strings.HasSuffix(p, ".d.ts") {
			continue
		}
		js := strings.TrimSuffix(p, ".ts") + ".js"
		if expected[js] {
			return "", errGuestV2
		}
		expected[js] = true
		for parent := filepath.Dir(js); parent != "."; parent = filepath.Dir(parent) {
			dirs[filepath.ToSlash(parent)] = true
		}
	}
	records := []vmruntime.RuntimeFileV2{}
	var total int64
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		st, e := d.Info()
		if e != nil {
			return e
		}
		if st.IsDir() {
			if !dirs[rel] {
				return errGuestV2
			}
			if rel == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !st.Mode().IsRegular() || !expected[rel] || len(records) >= 1000 {
			return errGuestV2
		}
		size, digest, e := hashRegularV2(p, 8<<20)
		if e != nil || size > 64<<20-total {
			return errGuestV2
		}
		total += size
		records = append(records, vmruntime.RuntimeFileV2{Path: rel, Size: size, SHA256: digest})
		return nil
	})
	if e != nil || len(records) != len(expected) {
		return "", errGuestV2
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Path < records[j].Path })
	raw, e := json.Marshal(records)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	h.Write([]byte("foundry:c4:v2:compiled-output\n"))
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil)), nil
}
func freezeCompiledTreeV2(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == filepath.Join(root, "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return os.Chmod(p, 0555)
		}
		if !d.Type().IsRegular() {
			return errGuestV2
		}
		return os.Chmod(p, 0444)
	})
}
