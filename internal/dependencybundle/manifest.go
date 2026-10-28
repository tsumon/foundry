// Package dependencybundle retains bounded, content-addressed file snapshots.
// Callers must admit ecosystem lock/layout/provenance before ImportFrozen. This
// generic store neither resolves dependencies nor inspects the source lock bytes.
package dependencybundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/platformfs"
	"strings"
)

const (
	MaxPayloadBytes  int64 = 64 << 20
	MaxFileBytes     int64 = 8 << 20
	MaxFiles               = 10000
	MaxManifestBytes       = 4 << 20
)

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Ecosystem     string `json:"ecosystem"`
	LockSHA256    string `json:"lock_sha256"`
	Files         []File `json:"files"`
}
type Binding struct {
	SchemaVersion  int    `json:"schema_version"`
	Ecosystem      string `json:"ecosystem"`
	BundleDigest   string `json:"bundle_digest"`
	ManifestDigest string `json:"manifest_digest"`
	FileCount      int    `json:"file_count"`
	TotalBytes     int64  `json:"total_bytes"`
}

var ErrInvalid = errors.New("invalid dependency bundle")

// CanonicalJSON validates before hashing; input ordering is never normalized.
func (m Manifest) CanonicalJSON() ([]byte, error) {
	if m.SchemaVersion != 1 || !ecosystem(m.Ecosystem) || !hashValid(m.LockSHA256) || m.Files == nil || len(m.Files) > MaxFiles {
		return nil, ErrInvalid
	}
	// Track directory spellings as well as files to reject aliases on case-folding filesystems.
	names := map[string]string{}
	files := map[string]bool{}
	dirs := map[string]bool{}
	var total int64
	previous := ""
	out := []byte(`{"schema_version":1,"ecosystem":"` + m.Ecosystem + `","lock_sha256":"` + m.LockSHA256 + `","files":[`)
	for i, f := range m.Files {
		if !validPath(f.Path) || (i > 0 && f.Path <= previous) || f.Size < 0 || f.Size > MaxFileBytes || !hashValid(f.SHA256) {
			return nil, ErrInvalid
		}
		previous = f.Path
		total += f.Size
		if total > MaxPayloadBytes {
			return nil, ErrInvalid
		}
		parts := strings.Split(f.Path, "/")
		for j := 1; j <= len(parts); j++ {
			p := strings.Join(parts[:j], "/")
			k := strings.ToLower(p)
			if old, ok := names[k]; ok && old != p {
				return nil, ErrInvalid
			}
			if j < len(parts) && files[k] {
				return nil, ErrInvalid
			}
			names[k] = p
			if j < len(parts) {
				dirs[k] = true
			}
		}
		k := strings.ToLower(f.Path)
		// A file cannot replace an already-seen parent directory.
		if dirs[k] {
			return nil, ErrInvalid
		}
		files[k] = true
		raw, _ := json.Marshal(f)
		if len(out)+len(raw)+3 > MaxManifestBytes {
			return nil, ErrInvalid
		}
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, raw...)
	}
	return append(out, ']', '}'), nil
}
func (m Manifest) Binding() (Binding, error) {
	raw, e := m.CanonicalJSON()
	if e != nil {
		return Binding{}, e
	}
	b := Binding{SchemaVersion: 1, Ecosystem: m.Ecosystem, ManifestDigest: sum(raw), BundleDigest: sum(append([]byte("foundry:dependencies:v1\n"), raw...)), FileCount: len(m.Files)}
	for _, f := range m.Files {
		b.TotalBytes += f.Size
	}
	return b, nil
}
func Empty(eco string) Manifest {
	return Manifest{SchemaVersion: 1, Ecosystem: eco, LockSHA256: sum(nil), Files: []File{}}
}
func ecosystem(s string) bool { return s == "node" || s == "python" }
func sum(b []byte) string     { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hashValid(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (b Binding) valid() bool {
	return b.SchemaVersion == 1 && ecosystem(b.Ecosystem) && hashValid(b.BundleDigest) && hashValid(b.ManifestDigest) && b.FileCount >= 0 && b.FileCount <= MaxFiles && b.TotalBytes >= 0 && b.TotalBytes <= MaxPayloadBytes
}
func validPath(p string) bool {
	if p == "" || len(p) > 1024 || platformfs.WindowsRelative(p) != nil {
		return false
	}
	for _, c := range []byte(p) {
		if c < 32 || c > 126 || c == '\\' || c == ':' {
			return false
		}
	}
	parts := strings.Split(p, "/")
	if len(parts) > 32 {
		return false
	}
	for _, c := range parts {
		if c == "" || c == "." || c == ".." || len(c) > 128 || strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") {
			return false
		}
		name := strings.ToUpper(strings.SplitN(c, ".", 2)[0])
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9' {
			return false
		}
	}
	return true
}
