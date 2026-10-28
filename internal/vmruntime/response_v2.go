package vmruntime

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/c4verify"
	"io"
	"unicode/utf8"
)

type SelfCheckV2 struct {
	ReadOnlyWorkspace       bool `json:"read_only_workspace"`
	ReadOnlyToolchain       bool `json:"read_only_toolchain"`
	NoNetwork               bool `json:"no_network"`
	PrivateControl          bool `json:"private_control"`
	Unprivileged            bool `json:"unprivileged"`
	NoNewPrivileges         bool `json:"no_new_privileges"`
	CgroupLimits            bool `json:"cgroup_limits"`
	TmpfsLimits             bool `json:"tmpfs_limits"`
	ReadOnlyDependencies    bool `json:"read_only_dependencies"`
	RuntimeInventoryMatches bool `json:"runtime_inventory_matches"`
	InputBindingValid       bool `json:"input_binding_valid"`
	PhaseCleanupConfirmed   bool `json:"phase_cleanup_confirmed"`
}

func (c SelfCheckV2) Valid() bool {
	return c.ReadOnlyWorkspace && c.ReadOnlyToolchain && c.NoNetwork && c.PrivateControl && c.Unprivileged && c.NoNewPrivileges && c.CgroupLimits && c.TmpfsLimits && c.ReadOnlyDependencies && c.RuntimeInventoryMatches && c.InputBindingValid && c.PhaseCleanupConfirmed
}

type PhaseEvidenceV2 struct {
	Phase                string `json:"phase"`
	Exited               bool   `json:"exited"`
	ExitCode             int    `json:"exit_code"`
	DeadlineExceeded     bool   `json:"deadline_exceeded"`
	Canceled             bool   `json:"canceled"`
	ResourceLimitsOK     bool   `json:"resource_limits_ok"`
	CleanupConfirmed     bool   `json:"cleanup_confirmed"`
	CompiledOutputDigest string `json:"compiled_output_digest"`
}
type WireResponseV2 struct {
	SchemaVersion int               `json:"schema_version"`
	Action        string            `json:"action"`
	Nonce         string            `json:"nonce"`
	RequestDigest string            `json:"request_digest"`
	Checks        SelfCheckV2       `json:"checks"`
	Result        c4verify.Result   `json:"result"`
	Phases        []PhaseEvidenceV2 `json:"phases"`
	MAC           string            `json:"mac"`
}

func expectedPhasesV2(q WireRequestV2) []string {
	if q.Action == "probe" {
		switch q.Target.ProfileID {
		case "c4_node_model_v2":
			return []string{"probe-node"}
		case "c4_python_model_v2":
			return []string{"probe-python"}
		case "c4_typescript_model_v2":
			return []string{"probe-typescript"}
		}
	}
	switch q.Target.ProfileID {
	case "c4_node_model_v2":
		return []string{"test-node"}
	case "c4_python_model_v2":
		return []string{"test-python"}
	case "c4_typescript_model_v2":
		return []string{"compile-typescript", "test-node"}
	}
	return nil
}
func phaseSucceededV2(p PhaseEvidenceV2) bool {
	return p.Exited && p.ExitCode == 0 && !p.DeadlineExceeded && !p.Canceled && p.ResourceLimitsOK && p.CleanupConfirmed
}
func validateResponseV2(q WireRequestV2, v WireResponseV2) error {
	digest, e := RequestDigestV2(q)
	if e != nil || v.SchemaVersion != 2 || v.Action != q.Action || v.Nonce != q.Nonce || v.RequestDigest != digest || v.Phases == nil || len(v.Result.Output) > c4verify.OutputLimit || !utf8.ValidString(v.Result.Output) || v.Result.Tests < 0 || v.Result.Tests > 4096 {
		return ErrProtocolV2
	}
	want := expectedPhasesV2(q)
	if len(v.Phases) > len(want) {
		return ErrProtocolV2
	}
	positive := v.Result.Class == "passed" || v.Result.Class == "test_failed" || v.Result.Class == "probe_passed"
	switch v.Result.Class {
	case "passed", "test_failed":
		if q.Action != "verify" {
			return ErrProtocolV2
		}
	case "probe_passed":
		if q.Action != "probe" {
			return ErrProtocolV2
		}
	case "infrastructure_failed", "timeout":
	default:
		return ErrProtocolV2
	}
	if positive && (!v.Checks.Valid() || len(v.Phases) != len(want) || v.Result.Truncated) {
		return ErrProtocolV2
	}
	deadline := false
	for i, p := range v.Phases {
		if p.Phase != want[i] || p.ExitCode < -1 || p.ExitCode > 255 || (!p.Exited && p.ExitCode != 0) || (p.DeadlineExceeded && p.Canceled) {
			return ErrProtocolV2
		}
		if p.DeadlineExceeded {
			deadline = true
		}
		if v.Checks.PhaseCleanupConfirmed && !p.CleanupConfirmed {
			return ErrProtocolV2
		}
		if p.Phase != "compile-typescript" {
			if p.CompiledOutputDigest != "" {
				return ErrProtocolV2
			}
		} else if p.CompiledOutputDigest != "" && (!hexDigest(p.CompiledOutputDigest) || !phaseSucceededV2(p)) {
			return ErrProtocolV2
		}
		// A later phase proves compilation, output validation/freeze and group cleanup
		// completed. It cannot follow a failed, canceled or incompletely frozen prefix.
		if i < len(v.Phases)-1 || positive && p.Phase == "compile-typescript" {
			if !phaseSucceededV2(p) || (p.Phase == "compile-typescript" && !hexDigest(p.CompiledOutputDigest)) {
				return ErrProtocolV2
			}
		}
		if positive && (!p.Exited || p.DeadlineExceeded || p.Canceled || !p.ResourceLimitsOK || !p.CleanupConfirmed) {
			return ErrProtocolV2
		}
	}
	if (v.Result.Class == "timeout") != deadline {
		return ErrProtocolV2
	}
	// Compile and probe cannot invent completed framework cases. Failure prefixes
	// may describe preparation/launch failures, but have no observed test result.
	fullTest := q.Action == "verify" && len(v.Phases) == len(want)
	if !fullTest && v.Result.Tests != 0 {
		return ErrProtocolV2
	}
	if len(v.Phases) == 0 {
		if v.Result.ExitCode != 0 {
			return ErrProtocolV2
		}
	} else {
		last := v.Phases[len(v.Phases)-1]
		if v.Result.ExitCode != last.ExitCode {
			return ErrProtocolV2
		}
	}
	switch v.Result.Class {
	case "probe_passed":
		if v.Result.ExitCode != 0 || v.Result.Tests != 0 || !phaseSucceededV2(v.Phases[0]) {
			return ErrProtocolV2
		}
	case "passed":
		if v.Result.Tests == 0 || v.Result.ExitCode != 0 {
			return ErrProtocolV2
		}
	case "test_failed":
		if v.Result.Tests == 0 || v.Result.ExitCode < 1 || v.Result.ExitCode > 124 {
			return ErrProtocolV2
		}
	}
	return nil
}
func responseMACV2(q WireRequestV2, v WireResponseV2) []byte {
	v.MAC = ""
	raw, _ := json.Marshal(v)
	key, _ := hex.DecodeString(q.Secret)
	h := hmac.New(sha256.New, key)
	h.Write([]byte("foundry:vm:response:v2\n"))
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(raw)))
	h.Write(n[:])
	h.Write(raw)
	return h.Sum(nil)
}
func WriteResponseV2(w io.Writer, q WireRequestV2, checks SelfCheckV2, result c4verify.Result, phases []PhaseEvidenceV2) error {
	digest, e := RequestDigestV2(q)
	if e != nil {
		return ErrProtocolV2
	}
	v := WireResponseV2{SchemaVersion: 2, Action: q.Action, Nonce: q.Nonce, RequestDigest: digest, Checks: checks, Result: result, Phases: phases}
	if validateResponseV2(q, v) != nil {
		return ErrProtocolV2
	}
	v.MAC = hex.EncodeToString(responseMACV2(q, v))
	return writeMetadataV2(w, v, MaxResponseBytesV2)
}

// ReadResponseV2 authenticates exactly one frame without read-ahead. The host
// lifecycle caller must additionally require exact transport EOF and confirmed
// native process termination/cleanup before committing this result.
func ReadResponseV2(r io.Reader, q WireRequestV2) (WireResponseV2, error) {
	if ValidateRequestV2(q) != nil {
		return WireResponseV2{}, ErrProtocolV2
	}
	var v WireResponseV2
	if _, e := readMetadataV2(r, MaxResponseBytesV2, &v); e != nil {
		return WireResponseV2{}, ErrProtocolV2
	}
	mac, e := hex.DecodeString(v.MAC)
	if e != nil || !hexDigest(v.MAC) || !hmac.Equal(mac, responseMACV2(q, v)) {
		return WireResponseV2{}, ErrProtocolV2
	}
	if validateResponseV2(q, v) != nil {
		return WireResponseV2{}, ErrProtocolV2
	}
	return v, nil
}
