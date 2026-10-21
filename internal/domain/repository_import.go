package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ImportError is a closed public vocabulary, never raw repository/process output.
type ImportError string

func (e ImportError) Error() string { return string(e) }

const (
	MaxRepositoryInspectionBytes             = 1 << 20
	ImportInvalidRequest         ImportError = "invalid_request"
	ImportConsentRequired        ImportError = "consent_required"
	ImportSourceChanged          ImportError = "source_changed"
	ImportInvalidRecord          ImportError = "invalid_inspection"
	ImportStorageFailed          ImportError = "storage_failed"
	ImportUnavailable            ImportError = "repository_unavailable"
	ImportLayout                 ImportError = "unsupported_layout"
	ImportMissingHEAD            ImportError = "missing_head"
	ImportObjectsUnavailable     ImportError = "objects_unavailable"
	ImportUnsupportedEntry       ImportError = "unsupported_entry"
	ImportUnsafePath             ImportError = "unsafe_path"
	ImportCaseCollision          ImportError = "case_collision"
	ImportBinary                 ImportError = "binary_file"
	ImportTreeLimit              ImportError = "tree_limit"
	ImportContextLimit           ImportError = "context_limit"
	ImportEmptyContext           ImportError = "empty_context"
	ImportAcceptance             ImportError = "unsupported_acceptance"
	ImportLocation               ImportError = "source_data_overlap"
	ImportTimeout                ImportError = "timeout"
	ImportCanceled               ImportError = "canceled"
	ImportInspectionFailed       ImportError = "inspection_failed"
	ImportGitUnavailable         ImportError = "git_unavailable"
)

type RepositoryImportSpec struct {
	Scope      []string `json:"scope"`
	Protected  []string `json:"protected"`
	Acceptance []string `json:"acceptance"`
	Image      string   `json:"image"`
}
type RepositoryInspectionRequest struct {
	RepositoryID       string               `json:"repository_id,omitempty"`
	RepositoryRevision int64                `json:"repository_revision,omitempty"`
	RepoPath           string               `json:"repo_path,omitempty"`
	Spec               RepositoryImportSpec `json:"spec"`
	Consent            bool                 `json:"consent"`
}
type ImportSelection struct {
	InspectionID string `json:"inspection_id"`
	Digest       string `json:"digest"`
}
type RepositoryInspection struct {
	SchemaVersion      int                  `json:"schema_version"`
	ID                 string               `json:"id"`
	RepositoryID       string               `json:"repository_id,omitempty"`
	RepositoryRevision int64                `json:"repository_revision,omitempty"`
	Root               string               `json:"root"`
	Branch             string               `json:"branch"`
	Commit             string               `json:"commit"`
	Spec               RepositoryImportSpec `json:"spec"`
	PolicyVersion      string               `json:"policy_version"`
	Status             string               `json:"status"`
	Stage              string               `json:"stage"`
	Code               ImportError          `json:"code"`
	CheckedAt          string               `json:"checked_at"`
	WorkingTreeStatus  string               `json:"working_tree_status"`
	FileCount          *int                 `json:"file_count"`
	TotalBytes         *int64               `json:"total_bytes"`
	ContextFileCount   *int                 `json:"context_file_count"`
	ContextBytes       *int64               `json:"context_bytes"`
	ContextManifest    []FileMeta           `json:"context_manifest"`
	ProtectedInputs    []string             `json:"protected_inputs"`
	Digest             string               `json:"digest"`
}

func importText(s string, max int, required bool) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl) && (!required || strings.TrimSpace(s) != "")
}
func importID(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func importHex(s string, sizes ...int) bool {
	if s != strings.ToLower(s) {
		return false
	}
	_, err := hex.DecodeString(s)
	if err != nil {
		return false
	}
	for _, n := range sizes {
		if len(s) == n {
			return true
		}
	}
	return false
}
func importRelative(p string) bool {
	return importText(p, 4096, true) && !path.IsAbs(p) && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../") && !strings.Contains(p, "\\")
}
func ValidateRepositoryImportSpec(s RepositoryImportSpec) error {
	if len(s.Scope) < 1 || len(s.Scope) > 500 || len(s.Protected) > 500 || len(s.Acceptance) < 1 || len(s.Acceptance) > 12 || !importText(s.Image, 512, false) {
		return ImportInvalidRequest
	}
	total := len(s.Image)
	for _, rules := range [][]string{s.Scope, s.Protected} {
		for _, r := range rules {
			total += len(r)
			if !importRelative(strings.TrimSuffix(r, "/")) {
				return ImportInvalidRequest
			}
		}
	}
	for _, c := range s.Acceptance {
		total += len(c)
		if !importText(c, 8192, true) {
			return ImportInvalidRequest
		}
	}
	if total > 256<<10 {
		return ImportInvalidRequest
	}
	return nil
}
func ValidateRepositoryInspectionRequest(r RepositoryInspectionRequest) error {
	if !r.Consent {
		return ImportConsentRequired
	}
	if r.RepositoryID != "" {
		if !importID(r.RepositoryID) || r.RepositoryRevision < 1 || r.RepoPath != "" {
			return ImportInvalidRequest
		}
	} else if r.RepositoryRevision != 0 || !filepath.IsAbs(r.RepoPath) || !importText(r.RepoPath, 4096, true) {
		return ImportInvalidRequest
	}
	return ValidateRepositoryImportSpec(r.Spec)
}

// Version one hashes canonical JSON of the complete record with only digest removed.
func RepositoryInspectionDigest(v RepositoryInspection) string {
	raw, _ := json.Marshal(v)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	delete(fields, "digest")
	raw, _ = json.Marshal(fields)
	h := sha256.Sum256(append([]byte("repository_inspection_v1\n"), raw...))
	return hex.EncodeToString(h[:])
}
func validImportFailure(stage string, code ImportError) bool {
	if code == ImportTimeout || code == ImportCanceled {
		return stage == "identity" || stage == "location" || stage == "tree" || stage == "acceptance"
	}
	switch stage {
	case "identity":
		return code == ImportUnavailable || code == ImportLayout || code == ImportMissingHEAD || code == ImportInspectionFailed || code == ImportGitUnavailable
	case "location":
		return code == ImportLocation
	case "tree":
		return code == ImportObjectsUnavailable || code == ImportUnsupportedEntry || code == ImportUnsafePath || code == ImportCaseCollision || code == ImportBinary || code == ImportTreeLimit || code == ImportContextLimit || code == ImportEmptyContext || code == ImportInspectionFailed
	case "acceptance":
		return code == ImportAcceptance
	}
	return false
}
func ValidateRepositoryInspection(v RepositoryInspection) error {
	if v.SchemaVersion != 1 || !importID(v.ID) || v.PolicyVersion != "committed_text_v1" || v.WorkingTreeStatus != "not_checked" || ValidateRepositoryImportSpec(v.Spec) != nil || !importText(v.Root, 4096, false) || !importText(v.Branch, 1024, false) {
		return ImportInvalidRecord
	}
	if v.RepositoryID == "" && v.RepositoryRevision != 0 || v.RepositoryID != "" && (!importID(v.RepositoryID) || v.RepositoryRevision < 1) {
		return ImportInvalidRecord
	}
	checked, err := time.Parse(time.RFC3339Nano, v.CheckedAt)
	if err != nil {
		return ImportInvalidRecord
	}
	_, offset := checked.Zone()
	if offset != 0 {
		return ImportInvalidRecord
	}
	if v.Root != "" && !filepath.IsAbs(v.Root) || v.Commit != "" && !importHex(v.Commit, 40, 64) {
		return ImportInvalidRecord
	}
	if v.Status == "blocked" {
		if !validImportFailure(v.Stage, v.Code) || v.FileCount != nil || v.TotalBytes != nil || v.ContextFileCount != nil || v.ContextBytes != nil || len(v.ContextManifest) != 0 || len(v.ProtectedInputs) != 0 {
			return ImportInvalidRecord
		}
	} else if v.Status == "ready" {
		if v.Stage != "complete" || v.Code != "" || v.Root == "" || v.Commit == "" || v.FileCount == nil || v.TotalBytes == nil || v.ContextFileCount == nil || v.ContextBytes == nil {
			return ImportInvalidRecord
		}
		if *v.FileCount < 1 || *v.FileCount > 500 || *v.TotalBytes < 0 || *v.TotalBytes > 10<<20 || *v.ContextFileCount < 1 || *v.ContextFileCount > 30 || *v.ContextFileCount > *v.FileCount || *v.ContextBytes < 0 || *v.ContextBytes > 200<<10 || *v.ContextBytes > *v.TotalBytes || len(v.ContextManifest) != *v.ContextFileCount || len(v.ProtectedInputs) > 500 {
			return ImportInvalidRecord
		}
		seen := map[string]bool{}
		var total int64
		for _, f := range v.ContextManifest {
			p := strings.ToLower(f.Path)
			if !importRelative(f.Path) || seen[p] || !importHex(f.SHA256, 64) || f.Bytes < 0 || f.Bytes > 200<<10 {
				return ImportInvalidRecord
			}
			seen[p] = true
			total += f.Bytes
		}
		if total != *v.ContextBytes {
			return ImportInvalidRecord
		}
		previous := ""
		for _, p := range v.ProtectedInputs {
			if !importRelative(p) || p <= previous {
				return ImportInvalidRecord
			}
			previous = p
		}
	} else {
		return ImportInvalidRecord
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > MaxRepositoryInspectionBytes {
		return ImportInvalidRecord
	}
	if !importHex(v.Digest, 64) || v.Digest != RepositoryInspectionDigest(v) {
		return ImportInvalidRecord
	}
	return nil
}
func ValidateImportSelection(v RepositoryInspection, s ImportSelection) error {
	if ValidateRepositoryInspection(v) != nil || v.Status != "ready" || s.InspectionID != v.ID || s.Digest != v.Digest {
		return ImportInvalidRecord
	}
	return nil
}
