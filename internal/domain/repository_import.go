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
	MaxRepositoryInspectionBytes               = 1 << 20
	MaxRepositoryInspectionBytesV2             = 2 << 20
	ImportInputs                   ImportError = "invalid_verification_inputs"
	ImportDependencies             ImportError = "invalid_dependencies"
	ImportInvalidRequest           ImportError = "invalid_request"
	ImportConsentRequired          ImportError = "consent_required"
	ImportSourceChanged            ImportError = "source_changed"
	ImportInvalidRecord            ImportError = "invalid_inspection"
	ImportStorageFailed            ImportError = "storage_failed"
	ImportUnavailable              ImportError = "repository_unavailable"
	ImportLayout                   ImportError = "unsupported_layout"
	ImportMissingHEAD              ImportError = "missing_head"
	ImportObjectsUnavailable       ImportError = "objects_unavailable"
	ImportUnsupportedEntry         ImportError = "unsupported_entry"
	ImportUnsafePath               ImportError = "unsafe_path"
	ImportCaseCollision            ImportError = "case_collision"
	ImportBinary                   ImportError = "binary_file"
	ImportTreeLimit                ImportError = "tree_limit"
	ImportContextLimit             ImportError = "context_limit"
	ImportEmptyContext             ImportError = "empty_context"
	ImportAcceptance               ImportError = "unsupported_acceptance"
	ImportLocation                 ImportError = "source_data_overlap"
	ImportTimeout                  ImportError = "timeout"
	ImportCanceled                 ImportError = "canceled"
	ImportInspectionFailed         ImportError = "inspection_failed"
	ImportGitUnavailable           ImportError = "git_unavailable"
	ImportInvalidBinding           ImportError = "invalid_import_binding"
	ImportIntentConflict           ImportError = "import_intent_conflict"
	ImportIncomplete               ImportError = "import_incomplete"
	ImportPrepareFailed            ImportError = "import_prepare_failed"
	ImportPublishFailed            ImportError = "import_publish_failed"
)

type RepositoryImportSpec struct {
	Scope          []string             `json:"scope"`
	Protected      []string             `json:"protected"`
	Acceptance     []string             `json:"acceptance"`
	Image          string               `json:"image"`
	VerificationV2 *C4ImportSelectionV2 `json:"verification_v2,omitempty"`
}

type RepositoryImportBinding struct {
	SchemaVersion     int    `json:"schema_version"`
	InspectionID      string `json:"inspection_id"`
	InspectionDigest  string `json:"inspection_digest"`
	ImportOperationID string `json:"import_operation_id"`
	Root              string `json:"root"`
	Commit            string `json:"commit"`
	BaseDigest        string `json:"base_digest"`
	ImportSpecDigest  string `json:"import_spec_digest"`
}

type RepositoryImportOperation struct {
	ID           string                   `json:"id"`
	InspectionID string                   `json:"inspection_id"`
	IntentDigest string                   `json:"intent_digest"`
	MissionID    string                   `json:"mission_id"`
	Status       string                   `json:"status"`
	Stage        string                   `json:"stage"`
	Code         ImportError              `json:"code"`
	Revision     int64                    `json:"revision"`
	CreatedAt    string                   `json:"created_at"`
	UpdatedAt    string                   `json:"updated_at"`
	Binding      *RepositoryImportBinding `json:"binding,omitempty"`
	Generation   *GenerationRef           `json:"generation,omitempty"`
}

func RepositoryImportSpecDigest(s RepositoryImportSpec) string {
	// nil and empty optional rules have the same normalized request meaning.
	if s.Protected == nil {
		s.Protected = []string{}
	}
	b, _ := json.Marshal(s)
	prefix := "FOUNDRY repository import spec v1\x00"
	if s.VerificationV2 != nil {
		prefix = "FOUNDRY repository import spec v2\x00"
	}
	h := sha256.Sum256(append([]byte(prefix), b...))
	return hex.EncodeToString(h[:])
}

func ValidateRepositoryImportBinding(b RepositoryImportBinding) error {
	if b.SchemaVersion != 1 || !importID(b.InspectionID) || !importID(b.ImportOperationID) || !importHex(b.InspectionDigest, 64) || !importHex(b.BaseDigest, 64) || !importHex(b.ImportSpecDigest, 64) || !importHex(b.Commit, 40, 64) || !importText(b.Root, 4096, true) || !filepath.IsAbs(b.Root) || filepath.Clean(b.Root) != b.Root {
		return ImportInvalidBinding
	}
	return nil
}

func ValidateMissionRepositoryImport(m Mission) error {
	if m.RepositoryImportInvalid {
		return ImportInvalidBinding
	}
	if m.ImportVersion == 0 && m.RepositoryImport == nil {
		return nil
	}
	mode, _, err := BaseWorkspaceMode(m.WorkspaceMode)
	if err != nil || m.ImportVersion != 1 || m.RepositoryImport == nil || m.CodingVersion != 1 || mode != "generations_v1" || m.ToolMode != "" || m.TaskBindings != nil || m.MCPBindings != nil {
		return ImportInvalidBinding
	}
	if err = ValidateRepositoryImportBinding(*m.RepositoryImport); err != nil {
		return err
	}
	if m.RepoPath != m.RepositoryImport.Root || m.BaseCommit != m.RepositoryImport.Commit {
		return ImportInvalidBinding
	}
	return nil
}

func ValidateRepositoryImportOperation(o RepositoryImportOperation) error {
	if !importID(o.ID) || !importID(o.InspectionID) || !importID(o.MissionID) || !importHex(o.IntentDigest, 64) || o.Revision < 1 {
		return ImportInvalidBinding
	}
	for _, v := range []string{o.CreatedAt, o.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
			return ImportInvalidBinding
		}
	}
	if (o.Binding == nil) != (o.Generation == nil) {
		return ImportInvalidBinding
	}
	if o.Binding != nil {
		if ValidateRepositoryImportBinding(*o.Binding) != nil || o.Binding.ImportOperationID != o.ID || o.Binding.InspectionID != o.InspectionID || !importHex(o.Generation.ID, 32) || o.Generation.Digest != o.Binding.BaseDigest {
			return ImportInvalidBinding
		}
	}
	switch o.Status {
	case "pending":
		if o.Stage != "reserve" || o.Code != "" || o.Binding != nil {
			return ImportInvalidBinding
		}
	case "prepared":
		if o.Stage != "prepare" || o.Code != "" || o.Binding == nil {
			return ImportInvalidBinding
		}
	case "committed":
		if o.Stage != "complete" || o.Code != "" || o.Binding == nil {
			return ImportInvalidBinding
		}
	case "failed":
		valid := o.Stage == "prepare" && o.Code == ImportPrepareFailed || o.Stage == "publish" && o.Code == ImportPublishFailed || (o.Stage == "prepare" || o.Stage == "commit") && o.Code == ImportStorageFailed || o.Stage == "source" && o.Code == ImportSourceChanged
		if !valid {
			return ImportInvalidBinding
		}
	default:
		return ImportInvalidBinding
	}
	return nil
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
	VerificationV2     *C4InputContractV2   `json:"verification_v2,omitempty"`
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
	if s.VerificationV2 != nil && s.VerificationV2.Validate() != nil {
		return ImportInvalidRequest
	}
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
	prefix := "repository_inspection_v1\n"
	if v.SchemaVersion == 2 {
		prefix = "repository_inspection_v2\n"
	}
	h := sha256.Sum256(append([]byte(prefix), raw...))
	return hex.EncodeToString(h[:])
}
func validImportFailure(stage string, code ImportError) bool {
	if code == ImportTimeout || code == ImportCanceled {
		return stage == "identity" || stage == "location" || stage == "tree" || stage == "acceptance" || stage == "inputs" || stage == "dependencies"
	}
	switch stage {
	case "inputs":
		return code == ImportInputs
	case "dependencies":
		return code == ImportDependencies
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
	if !validInspectionVersion(v) || !importID(v.ID) || v.WorkingTreeStatus != "not_checked" || ValidateRepositoryImportSpec(v.Spec) != nil || !importText(v.Root, 4096, false) || !importText(v.Branch, 1024, false) {
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
		if v.SchemaVersion == 1 && (v.Stage == "inputs" || v.Stage == "dependencies") {
			return ImportInvalidRecord
		}
		if v.VerificationV2 != nil {
			return ImportInvalidRecord
		}
		if !validImportFailure(v.Stage, v.Code) || v.FileCount != nil || v.TotalBytes != nil || v.ContextFileCount != nil || v.ContextBytes != nil || len(v.ContextManifest) != 0 || len(v.ProtectedInputs) != 0 {
			return ImportInvalidRecord
		}
	} else if v.Status == "ready" {
		if v.SchemaVersion == 2 && validateInspectionV2(v) != nil {
			return ImportInvalidRecord
		}
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
	limit := MaxRepositoryInspectionBytes
	if v.SchemaVersion == 2 {
		limit = MaxRepositoryInspectionBytesV2
	}
	if err != nil || len(raw) > limit {
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
