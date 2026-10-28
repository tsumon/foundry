package vmruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	HeaderV2                 = "FOUNDRY_VM_V2\n"
	ReadyMarkerV2            = "FOUNDRY_VM_READY_V2\n"
	MaxRequestBytesV2        = 8 << 20
	MaxSourceMetadataBytesV2 = 1 << 20
	MaxResponseBytesV2       = 128 << 10
)

// ErrProtocolV2 never contains input JSON, payloads, store paths or callback errors.
var ErrProtocolV2 = errors.New("invalid or incomplete VM v2 transaction")

type ProbeTargetV2 struct {
	ProfileID              string              `json:"profile_id"`
	Identity               domain.C4VMIdentity `json:"identity"`
	RuntimeInventoryDigest string              `json:"runtime_inventory_digest"`
}
type SourceFileV2 struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type VerifyRequestV2 struct {
	OwnerDigest        string                    `json:"owner_digest"`
	Profile            domain.C4Profile          `json:"profile"`
	ProfileDigest      string                    `json:"profile_digest"`
	PolicyDigest       string                    `json:"policy_digest"`
	Inputs             domain.C4InputContractV2  `json:"inputs"`
	CandidateDigest    string                    `json:"candidate_digest"`
	SourceFiles        []SourceFileV2            `json:"source_files"`
	DependencyManifest dependencybundle.Manifest `json:"dependency_manifest"`
}
type WireRequestV2 struct {
	SchemaVersion int              `json:"schema_version"`
	Action        string           `json:"action"`
	Nonce         string           `json:"nonce"`
	Secret        string           `json:"secret"`
	Target        ProbeTargetV2    `json:"target"`
	Verify        *VerifyRequestV2 `json:"verify"`
}

// FrameSinkV2 receives provisional, bounded file bodies. It must consume the
// complete body during the call and write only to private staging. No callback
// result authorizes execution: discard all staging unless ReadRequestV2 succeeds.
// kind 1 is source; kind 2 is dependency, with staging mode fixed at 0444.
type FrameSinkV2 func(kind uint8, index uint32, file SourceFileV2, body io.Reader) error

func digestV2(prefix string, v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.New()
	h.Write([]byte(prefix))
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// OwnerDigestV2 binds ownership only; no host path or candidate bytes are hashed.
func OwnerDigestV2(q Request) (string, error) {
	for _, s := range []string{q.OperationID, q.MissionID, q.Owner, q.Name} {
		if s == "" || len(s) > 256 || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") {
			return "", ErrProtocolV2
		}
	}
	return digestV2("foundry:vm:owner:v2\n", struct {
		OperationID string `json:"operation_id"`
		MissionID   string `json:"mission_id"`
		Owner       string `json:"owner"`
		Name        string `json:"name"`
	}{q.OperationID, q.MissionID, q.Owner, q.Name}), nil
}
func PolicyDigestV2(profileID string) (string, error) {
	p, e := domain.FixedC4VMPolicyV2(profileID)
	if e != nil {
		return "", ErrProtocolV2
	}
	return digestV2("foundry:c4:v2:vm-policy\n", p), nil
}
func RequestDigestV2(q WireRequestV2) (string, error) {
	if ValidateRequestV2(q) != nil {
		return "", ErrProtocolV2
	}
	q.Secret = ""
	return digestV2("foundry:vm:request:v2\n", q), nil
}

// SourceMetadataV2 preserves the v1 SnapshotDigest byte algorithm, but requires
// the v2 caller's inventory to be in canonical order; it never sorts for callers.
func SourceMetadataV2(source []SnapshotFile) ([]SourceFileV2, error) {
	if _, e := SnapshotDigest(source); e != nil {
		return nil, ErrProtocolV2
	}
	out := make([]SourceFileV2, len(source))
	for i, f := range source {
		if len(f.Path) > 1024 || (i > 0 && f.Path <= source[i-1].Path) {
			return nil, ErrProtocolV2
		}
		h := sha256.Sum256(f.Data)
		out[i] = SourceFileV2{f.Path, f.Mode, int64(len(f.Data)), hex.EncodeToString(h[:])}
	}
	return out, nil
}

// mountFitsV2 counts unique ancestors, files, the mount root and 4 KiB file
// page estimates. The guest must confirm actual page size and mounted limits;
// metadata admission is not native tmpfs proof. Inode admission is independent
// of the wire file/byte bounds.
func mountFitsV2(files []SourceFileV2, mount domain.C4Tmpfs) bool {
	names := map[string]bool{".": true}
	var allocated int64
	for _, f := range files {
		if f.Size < 0 || f.Size > mount.SizeBytes || f.Size > mount.SizeBytes-allocated {
			return false
		}
		pages := (f.Size + 4095) / 4096 * 4096
		if pages > mount.SizeBytes-allocated {
			return false
		}
		allocated += pages
		for p := f.Path; p != "."; p = path.Dir(p) {
			names[p] = true
			if len(names) > mount.Inodes {
				return false
			}
		}
	}
	return true
}
func dependencyFilesV2(m dependencybundle.Manifest) []SourceFileV2 {
	files := make([]SourceFileV2, len(m.Files))
	for i, f := range m.Files {
		files[i] = SourceFileV2{f.Path, 0444, f.Size, f.SHA256}
	}
	return files
}
func ValidateRequestV2(q WireRequestV2) error {
	if q.SchemaVersion != 2 || !hexDigest(q.Nonce) || !hexDigest(q.Secret) || q.Target.Identity.SchemaVersion != 2 || q.Target.Identity.Validate() != nil || !hexDigest(q.Target.RuntimeInventoryDigest) {
		return ErrProtocolV2
	}
	policy, e := domain.FixedC4VMPolicyV2(q.Target.ProfileID)
	if e != nil {
		return ErrProtocolV2
	}
	switch q.Action {
	case "probe":
		if q.Verify != nil {
			return ErrProtocolV2
		}
	case "verify":
		v := q.Verify
		if v == nil || !hexDigest(v.OwnerDigest) || !hexDigest(v.CandidateDigest) || v.Profile.SchemaVersion != 2 || v.Inputs.ValidateProfileBinding(v.Profile) != nil || v.Profile.ID != q.Target.ProfileID || *v.Profile.EmbeddedVM != q.Target.Identity || v.Profile.V2.RuntimeInventoryDigest != q.Target.RuntimeInventoryDigest || v.Profile.ProfileDigest() != v.ProfileDigest {
			return ErrProtocolV2
		}
		pd, _ := PolicyDigestV2(q.Target.ProfileID)
		if pd != v.PolicyDigest {
			return ErrProtocolV2
		}
		b, e := v.DependencyManifest.Binding()
		if e != nil || domain.C4DependencyBindingV2(b) != v.Inputs.Dependencies {
			return ErrProtocolV2
		}
		lock := "package-lock.json"
		if b.Ecosystem == "python" {
			lock = "foundry.python.lock.json"
		}
		found := false
		for _, f := range v.Inputs.SourceFiles {
			if f.Path == lock && f.SHA256 == v.DependencyManifest.LockSHA256 {
				found = true
			}
		}
		if !found {
			return ErrProtocolV2
		}
		if v.SourceFiles == nil || len(v.SourceFiles) == 0 || len(v.SourceFiles) > policy.Test.MaxSourceFiles {
			return ErrProtocolV2
		}
		inventory := make([]domain.C4InputFileV2, len(v.SourceFiles))
		for i, f := range v.SourceFiles {
			if !safeRelative(f.Path) {
				return ErrProtocolV2
			}
			inventory[i] = domain.C4InputFileV2{Path: f.Path, Mode: f.Mode, Bytes: f.Size, SHA256: f.SHA256}
		}
		if v.Inputs.ValidateSourceInventory(inventory) != nil || !mountFitsV2(v.SourceFiles, policy.Test.SourceMount) || !mountFitsV2(dependencyFilesV2(v.DependencyManifest), policy.Test.DependencyMount) {
			return ErrProtocolV2
		}
		if raw, _ := json.Marshal(v.SourceFiles); len(raw) > MaxSourceMetadataBytesV2 {
			return ErrProtocolV2
		}
	default:
		return ErrProtocolV2
	}
	raw, e := json.Marshal(q)
	if e != nil || len(raw) > MaxRequestBytesV2 {
		return ErrProtocolV2
	}
	return nil
}
