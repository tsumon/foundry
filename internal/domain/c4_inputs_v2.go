package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/platformfs"
	"slices"
	"strings"
)

const (
	MaxC4InputFilesV2               = 500
	MaxC4InputSourceBytesV2   int64 = 10 << 20
	MaxC4InputContractBytesV2       = 1 << 20
)

var ErrInvalidC4InputV2 = errors.New("invalid c4 v2 input contract")

// C4SourceDigestV2 binds the exact committed admission metadata bytes.
type C4SourceDigestV2 struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type C4InputFileV2 struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Role   string `json:"role"`
}

// C4DependencyBindingV2 is the complete store binding without a store import.
type C4DependencyBindingV2 struct {
	SchemaVersion  int    `json:"schema_version"`
	Ecosystem      string `json:"ecosystem"`
	BundleDigest   string `json:"bundle_digest"`
	ManifestDigest string `json:"manifest_digest"`
	FileCount      int    `json:"file_count"`
	TotalBytes     int64  `json:"total_bytes"`
}

type C4TypeScriptContractV2 struct {
	ConfigPath   string   `json:"config_path"`
	ConfigDigest string   `json:"config_digest"`
	InputFiles   []string `json:"input_files"`
	OutputRoot   string   `json:"output_root"`
}

// C4InputContractV2 declares inspected immutable inputs, never execution authority.
type C4InputContractV2 struct {
	SchemaVersion           int                     `json:"schema_version"`
	ProfileID               string                  `json:"profile_id"`
	Dependencies            C4DependencyBindingV2   `json:"dependencies"`
	AdmissionDigest         string                  `json:"admission_digest"`
	SourceFiles             []C4SourceDigestV2      `json:"source_files"`
	Protected               []C4InputFileV2         `json:"protected"`
	ProtectedPrefixes       []string                `json:"protected_prefixes"`
	TestFiles               []string                `json:"test_files"`
	SourceRoots             []string                `json:"source_roots"`
	HarnessVersion          string                  `json:"harness_version"`
	TypeScript              *C4TypeScriptContractV2 `json:"typescript,omitempty"`
	ProtectedInputsDigest   string                  `json:"protected_inputs_digest"`
	ExecutionContractDigest string                  `json:"execution_contract_digest"`
}

// WithDigests seals an inspected declaration into fresh storage. It does not
// normalize ordering, infer tests/helpers, or grant permission to execute.
func (c C4InputContractV2) WithDigests() (C4InputContractV2, error) {
	if err := c.validateMaterial(); err != nil {
		return C4InputContractV2{}, err
	}
	c.SourceFiles = slices.Clone(c.SourceFiles)
	c.Protected = slices.Clone(c.Protected)
	c.ProtectedPrefixes = slices.Clone(c.ProtectedPrefixes)
	c.TestFiles = slices.Clone(c.TestFiles)
	c.SourceRoots = slices.Clone(c.SourceRoots)
	if c.TypeScript != nil {
		ts := *c.TypeScript
		ts.InputFiles = slices.Clone(ts.InputFiles)
		c.TypeScript = &ts
	}
	c.ProtectedInputsDigest, c.ExecutionContractDigest = c.digests()
	if err := c.Validate(); err != nil {
		return C4InputContractV2{}, err
	}
	return c, nil
}

func (c C4InputContractV2) digests() (string, string) {
	raw, _ := json.Marshal(struct {
		Protected []C4InputFileV2 `json:"protected"`
		Prefixes  []string        `json:"protected_prefixes"`
	}{c.Protected, c.ProtectedPrefixes})
	protected := c4InputSumV2("foundry:c4:v2:protected-inputs\n", raw)
	c.ProtectedInputsDigest, c.ExecutionContractDigest = "", ""
	raw, _ = json.Marshal(c)
	return protected, c4InputSumV2("foundry:c4:v2:execution-contract\n", raw)
}

func c4InputSumV2(domain string, raw []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

func (c C4InputContractV2) Validate() error {
	if err := c.validateMaterial(); err != nil {
		return err
	}
	if !c4SHA.MatchString(c.ProtectedInputsDigest) || !c4SHA.MatchString(c.ExecutionContractDigest) {
		return ErrInvalidC4InputV2
	}
	protected, execution := c.digests()
	if c.ProtectedInputsDigest != protected || c.ExecutionContractDigest != execution {
		return ErrInvalidC4InputV2
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxC4InputContractBytesV2 {
		return ErrInvalidC4InputV2
	}
	return nil
}

// CanonicalJSON requires validated, already sorted material. Wire readers must
// separately reject unknown/duplicate keys before constructing this value.
func (c C4InputContractV2) CanonicalJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

func (b C4DependencyBindingV2) Validate() error {
	if b.SchemaVersion != 1 || (b.Ecosystem != "node" && b.Ecosystem != "python") || !c4SHA.MatchString(b.BundleDigest) || !c4SHA.MatchString(b.ManifestDigest) || b.FileCount < 0 || b.FileCount > 10000 || b.TotalBytes < 0 || b.TotalBytes > 64<<20 {
		return ErrInvalidC4InputV2
	}
	return nil
}

func (c C4InputContractV2) validateMaterial() error {
	language, _, _, err := c4LanguageProfileV2(c.ProfileID)
	if err != nil || c.SchemaVersion != 2 || c.HarnessVersion != "foundry-test-v2" || !c4SHA.MatchString(c.AdmissionDigest) || c.Dependencies.Validate() != nil {
		return ErrInvalidC4InputV2
	}
	ecosystem := "node"
	required := map[string]string{"package.json": "manifest", "package-lock.json": "lock"}
	if language == "python" {
		ecosystem = "python"
		required = map[string]string{"foundry.python.lock.json": "lock"}
	}
	if language == "typescript" {
		required["tsconfig.json"] = "config"
	}
	if c.Dependencies.Ecosystem != ecosystem || len(c.SourceRoots) != 1 || (c.SourceRoots[0] != "." && c.SourceRoots[0] != "src") || len(c.TestFiles) == 0 {
		return ErrInvalidC4InputV2
	}
	paths := newC4InputPathsV2()
	if c.SourceRoots[0] == "src" && !paths.add("src", 2) {
		return ErrInvalidC4InputV2
	}
	if !c4InputListV2(c.ProtectedPrefixes, paths, 0) || !c4InputListV2(c.TestFiles, paths, 1) {
		return ErrInvalidC4InputV2
	}
	for _, reserved := range []string{".foundry-build", ".venv", "node_modules", "site-packages"} {
		if !slices.Contains(c.ProtectedPrefixes, reserved) {
			return ErrInvalidC4InputV2
		}
	}
	if c.Protected == nil || len(c.Protected) > MaxC4InputFilesV2 || c.SourceFiles == nil || len(c.SourceFiles) != len(required) {
		return ErrInvalidC4InputV2
	}
	protected := make(map[string]C4InputFileV2, len(c.Protected))
	var total int64
	previous := ""
	for i, f := range c.Protected {
		if (i > 0 && f.Path <= previous) || !paths.add(f.Path, 1) || !c4InputFileMetadataV2(f) || !c4InputRoleV2(f.Role) || f.Bytes > MaxC4InputSourceBytesV2-total {
			return ErrInvalidC4InputV2
		}
		previous = f.Path
		total += f.Bytes
		protected[f.Path] = f
	}
	total, previous = 0, ""
	for i, f := range c.SourceFiles {
		p, ok := protected[f.Path]
		if (i > 0 && f.Path <= previous) || !ok || required[f.Path] == "" || p.Role != required[f.Path] || f.Bytes != p.Bytes || f.SHA256 != p.SHA256 || f.Bytes > MaxC4InputSourceBytesV2-total {
			return ErrInvalidC4InputV2
		}
		previous = f.Path
		total += f.Bytes
	}
	selected := strings.SplitN(c.TestFiles[0], "/", 2)[0]
	if (language == "python" && selected != "tests") || (language != "python" && selected != "test" && selected != "tests") || !slices.Contains(c.ProtectedPrefixes, selected) {
		return ErrInvalidC4InputV2
	}
	for _, p := range c.TestFiles {
		f, ok := protected[p]
		if !ok || f.Role != "test" || !strings.HasPrefix(p, selected+"/") || !c4InputTestV2(p, language) {
			return ErrInvalidC4InputV2
		}
	}
	for _, f := range c.Protected {
		if f.Role == "test" && !slices.Contains(c.TestFiles, f.Path) {
			return ErrInvalidC4InputV2
		}
	}
	if language != "typescript" {
		if c.TypeScript != nil {
			return ErrInvalidC4InputV2
		}
		return nil
	}
	ts := c.TypeScript
	if ts == nil || ts.ConfigPath != "tsconfig.json" || ts.ConfigDigest != protected["tsconfig.json"].SHA256 || ts.OutputRoot != "/foundry-build/compiled" || !c4InputListV2(ts.InputFiles, paths, 1) {
		return ErrInvalidC4InputV2
	}
	for _, p := range ts.InputFiles {
		if !strings.HasSuffix(p, ".ts") {
			return ErrInvalidC4InputV2
		}
	}
	for _, p := range c.TestFiles {
		if !slices.Contains(ts.InputFiles, p) {
			return ErrInvalidC4InputV2
		}
	}
	return nil
}

func c4InputRoleV2(role string) bool {
	switch role {
	case "test", "helper", "manifest", "config", "lock":
		return true
	}
	return false
}

func c4InputTestV2(p, language string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	switch language {
	case "javascript":
		return strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.mjs") || strings.HasSuffix(base, ".test.cjs")
	case "typescript":
		return strings.HasSuffix(base, ".test.ts")
	case "python":
		return strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")
	}
	return false
}

func c4InputFileMetadataV2(f C4InputFileV2) bool {
	return (f.Mode == 0644 || f.Mode == 0755) && f.Bytes >= 0 && f.Bytes <= MaxC4InputSourceBytesV2 && c4SHA.MatchString(f.SHA256)
}

func c4InputListV2(list []string, paths *c4InputPathsV2, kind int) bool {
	if list == nil || len(list) > MaxC4InputFilesV2 {
		return false
	}
	for i, p := range list {
		if (i > 0 && p <= list[i-1]) || !paths.add(p, kind) {
			return false
		}
	}
	return true
}

// c4InputPathsV2 tracks component spellings across all lists, including parent
// directories. kind 0 is a prefix, 1 a file, 2 an explicit directory.
type c4InputPathsV2 struct {
	names       map[string]string
	files, dirs map[string]bool
}

func newC4InputPathsV2() *c4InputPathsV2 {
	return &c4InputPathsV2{map[string]string{}, map[string]bool{}, map[string]bool{}}
}
func (n *c4InputPathsV2) add(p string, kind int) bool {
	if p == "" || len(p) > 1024 || platformfs.WindowsRelative(p) != nil {
		return false
	}
	for _, b := range []byte(p) {
		if b < 32 || b > 126 || b == '\\' {
			return false
		}
	}
	parts := strings.Split(p, "/")
	if len(parts) > 32 {
		return false
	}
	for i, part := range parts {
		if kind == 1 && c4InputReservedV2(part) {
			return false
		}
		prefix := strings.Join(parts[:i+1], "/")
		key := strings.ToLower(prefix)
		if old, ok := n.names[key]; ok && old != prefix {
			return false
		}
		n.names[key] = prefix
		if i < len(parts)-1 || kind == 2 {
			if n.files[key] {
				return false
			}
			n.dirs[key] = true
		} else if kind == 1 {
			if n.dirs[key] {
				return false
			}
			n.files[key] = true
		}
	}
	return true
}
func c4InputReservedV2(part string) bool {
	switch strings.ToLower(part) {
	case "node_modules", ".venv", "site-packages", ".foundry-build":
		return true
	}
	return false
}

// ValidateSourceInventory compares caller-computed metadata, never reads disk,
// and ignores inventory Role. Whole-source digest validation belongs to caller.
func (c C4InputContractV2) ValidateSourceInventory(files []C4InputFileV2) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if files == nil || len(files) > MaxC4InputFilesV2 {
		return ErrInvalidC4InputV2
	}
	protected := make(map[string]C4InputFileV2, len(c.Protected))
	for _, f := range c.Protected {
		protected[f.Path] = f
	}
	paths := newC4InputPathsV2()
	// Seed spellings from the immutable contract as well as the candidate.
	for _, p := range c.ProtectedPrefixes {
		if !paths.add(p, 0) {
			return ErrInvalidC4InputV2
		}
	}
	for _, f := range c.Protected {
		if !paths.add(f.Path, 1) {
			return ErrInvalidC4InputV2
		}
	}
	if c.SourceRoots[0] == "src" && !paths.add("src", 2) {
		return ErrInvalidC4InputV2
	}
	seen := make(map[string]bool, len(files))
	var total int64
	for i, f := range files {
		if (i > 0 && f.Path <= files[i-1].Path) || !paths.add(f.Path, 1) || !c4InputFileMetadataV2(f) || f.Bytes > MaxC4InputSourceBytesV2-total {
			return ErrInvalidC4InputV2
		}
		total += f.Bytes
		seen[f.Path] = true
		if old, ok := protected[f.Path]; ok {
			if f.Mode != old.Mode || f.Bytes != old.Bytes || f.SHA256 != old.SHA256 {
				return ErrInvalidC4InputV2
			}
		} else {
			for _, prefix := range c.ProtectedPrefixes {
				if f.Path == prefix || strings.HasPrefix(f.Path, prefix+"/") {
					return ErrInvalidC4InputV2
				}
			}
		}
		if c.TypeScript != nil && strings.HasSuffix(f.Path, ".ts") && !slices.Contains(c.TypeScript.InputFiles, f.Path) {
			return ErrInvalidC4InputV2
		}
	}
	for p := range protected {
		if !seen[p] {
			return ErrInvalidC4InputV2
		}
	}
	if c.TypeScript != nil {
		for _, p := range c.TypeScript.InputFiles {
			if !seen[p] {
				return ErrInvalidC4InputV2
			}
		}
	}
	return nil
}

// ValidateProfileBinding compares the exact inspected inputs. Installed runtime
// inventory validation remains the caller's target responsibility.
func (c C4InputContractV2) ValidateProfileBinding(p C4Profile) error {
	if c.Validate() != nil || p.Validate() != nil || p.SchemaVersion != 2 || p.ID != c.ProfileID || p.V2.DependencyBundleDigest != c.Dependencies.BundleDigest || p.V2.ProtectedInputsDigest != c.ProtectedInputsDigest || p.V2.ExecutionContractDigest != c.ExecutionContractDigest {
		return ErrInvalidC4InputV2
	}
	return nil
}
