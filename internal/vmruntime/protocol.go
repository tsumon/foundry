// Package vmruntime owns verified VM bundles and their private serial protocol.
// No exported constructor can substitute a process implementation for a VM.
package vmruntime

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/c4verify"
	"io"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const Header = "FOUNDRY_VM_V1\n"
const ReadyMarker = "FOUNDRY_VM_READY_V1\n"
const MaxRequestBytes = 16 * 1024 * 1024
const MaxResponseBytes = 128 * 1024
const MaxSnapshotBytes = 10 * 1024 * 1024

type SnapshotFile struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
	Data []byte `json:"data"`
}
type WireRequest struct {
	SchemaVersion   int            `json:"schema_version"`
	Action          string         `json:"action"`
	Nonce           string         `json:"nonce"`
	Secret          string         `json:"secret"`
	GuestPlatform   string         `json:"guest_platform"`
	CandidateDigest string         `json:"candidate_digest"`
	Files           []SnapshotFile `json:"files"`
}
type SelfCheck struct {
	ReadOnlyWorkspace bool `json:"read_only_workspace"`
	ReadOnlyToolchain bool `json:"read_only_toolchain"`
	NoNetwork         bool `json:"no_network"`
	PrivateControl    bool `json:"private_control"`
	Unprivileged      bool `json:"unprivileged"`
	NoNewPrivileges   bool `json:"no_new_privileges"`
	CgroupLimits      bool `json:"cgroup_limits"`
	TmpfsLimits       bool `json:"tmpfs_limits"`
}

func (c SelfCheck) Valid() bool {
	return c.ReadOnlyWorkspace && c.ReadOnlyToolchain && c.NoNetwork && c.PrivateControl && c.Unprivileged && c.NoNewPrivileges && c.CgroupLimits && c.TmpfsLimits
}

type WireResponse struct {
	SchemaVersion   int             `json:"schema_version"`
	Action          string          `json:"action"`
	Nonce           string          `json:"nonce"`
	CandidateDigest string          `json:"candidate_digest"`
	Checks          SelfCheck       `json:"checks"`
	Result          c4verify.Result `json:"result"`
	MAC             string          `json:"mac"`
}

func hexDigest(s string) bool {
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
func safeRelative(p string) bool {
	if p == "" || p == "." || path.IsAbs(p) || path.Clean(p) != p || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\:") {
		return false
	}
	for _, c := range p {
		if c < 32 || c > 126 {
			return false
		}
	}
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		if part == ".git" || part == ".hg" || part == ".svn" || part == ".." || strings.HasPrefix(part, ".env") || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.Contains(part, "token") {
			return false
		}
	}
	return true
}
func SnapshotDigest(files []SnapshotFile) (string, error) {
	if len(files) == 0 || len(files) > 500 {
		return "", errors.New("invalid snapshot file count")
	}
	sorted := append([]SnapshotFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	seen := map[string]bool{}
	total := 0
	for _, f := range sorted {
		key := strings.ToLower(f.Path)
		total += len(f.Data)
		if !safeRelative(f.Path) || seen[key] || (f.Mode != 0644 && f.Mode != 0755) || total > MaxSnapshotBytes || !utf8.Valid(f.Data) || bytes.IndexByte(f.Data, 0) >= 0 {
			return "", errors.New("invalid snapshot entry")
		}
		seen[key] = true
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return "", errors.New("snapshot file directory collision")
			}
		}
		fmt.Fprintf(h, "%d:%s:%d:%o:", len(f.Path), f.Path, len(f.Data), f.Mode)
		h.Write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func ValidateRequest(q WireRequest) error {
	if q.SchemaVersion != 1 || !hexDigest(q.Nonce) || !hexDigest(q.Secret) || (q.GuestPlatform != "linux/arm64" && q.GuestPlatform != "linux/amd64") {
		return errors.New("invalid VM request identity")
	}
	switch q.Action {
	case "probe":
		if q.CandidateDigest != "" || len(q.Files) != 0 {
			return errors.New("probe contains candidate")
		}
	case "verify":
		d, e := SnapshotDigest(q.Files)
		if e != nil || d != q.CandidateDigest {
			return errors.New("candidate digest mismatch")
		}
	default:
		return errors.New("unsupported VM action")
	}
	return nil
}

// strictJSON rejects duplicate keys at every depth as well as unknown fields,
// case aliases, trailing values and excessive nesting. Case aliases are caught
// by re-marshalling typed data and comparing the exact key sets recursively.
func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	if e := uniqueValue(d, 0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return errors.New("invalid JSON schema")
	}
	canonical, e := json.Marshal(v)
	if e != nil {
		return e
	}
	var original, normal any
	json.Unmarshal(b, &original)
	json.Unmarshal(canonical, &normal)
	if !sameKeys(original, normal) {
		return errors.New("missing or aliased JSON field")
	}
	return nil
}
func sameKeys(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !sameKeys(v, w) {
				return false
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !sameKeys(x[i], y[i]) {
				return false
			}
		}
	}
	return true
}
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeded")
	}
	t, e := d.Token()
	if e != nil {
		return errors.New("incomplete JSON")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			s, ok := k.(string)
			if e != nil || !ok || seen[s] {
				return errors.New("duplicate JSON field")
			}
			seen[s] = true
			if e := uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := uniqueValue(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return errors.New("invalid JSON")
	}
	_, e = d.Token()
	return e
}
func readFrame(r io.Reader, limit int, v any) error {
	h := make([]byte, len(Header)+4)
	if _, e := io.ReadFull(r, h); e != nil {
		return errors.New("missing VM frame")
	}
	if string(h[:len(Header)]) != Header {
		return errors.New("invalid VM frame header")
	}
	n := binary.BigEndian.Uint32(h[len(Header):])
	if n == 0 || n > uint32(limit) {
		return errors.New("VM frame exceeds bound")
	}
	b := make([]byte, int(n))
	if _, e := io.ReadFull(r, b); e != nil {
		return errors.New("partial VM frame")
	}
	return strictJSON(b, v)
}
func writeFrame(w io.Writer, v any, limit int) error {
	b, e := json.Marshal(v)
	if e != nil || len(b) > limit {
		return errors.New("VM frame exceeds bound")
	}
	h := binary.BigEndian.AppendUint32([]byte(Header), uint32(len(b)))
	_, e = io.Copy(w, io.MultiReader(bytes.NewReader(h), bytes.NewReader(b)))
	return e
}
func ReadRequest(r io.Reader) (WireRequest, error) {
	var q WireRequest
	if e := readFrame(r, MaxRequestBytes, &q); e != nil {
		return q, e
	}
	return q, ValidateRequest(q)
}
func WriteRequest(w io.Writer, q WireRequest) error {
	if e := ValidateRequest(q); e != nil {
		return e
	}
	return writeFrame(w, q, MaxRequestBytes)
}
func responseMAC(q WireRequest, r WireResponse) string {
	r.MAC = ""
	b, _ := json.Marshal(r)
	key, _ := hex.DecodeString(q.Secret)
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}
func WriteResponse(w io.Writer, q WireRequest, c SelfCheck, r c4verify.Result) error {
	if e := ValidateRequest(q); e != nil {
		return e
	}
	v := WireResponse{SchemaVersion: 1, Action: q.Action, Nonce: q.Nonce, CandidateDigest: q.CandidateDigest, Checks: c, Result: r}
	v.MAC = responseMAC(q, v)
	return writeFrame(w, v, MaxResponseBytes)
}
func ReadResponse(r io.Reader, q WireRequest) (WireResponse, error) {
	var v WireResponse
	if e := ValidateRequest(q); e != nil {
		return v, e
	}
	if e := readFrame(r, MaxResponseBytes, &v); e != nil {
		return v, e
	}
	if v.SchemaVersion != 1 || v.Action != q.Action || v.Nonce != q.Nonce || v.CandidateDigest != q.CandidateDigest || !hexDigest(v.MAC) || !hmac.Equal([]byte(v.MAC), []byte(responseMAC(q, v))) || !v.Checks.Valid() || len(v.Result.Output) > c4verify.OutputLimit {
		return WireResponse{}, errors.New("unauthenticated or unsafe VM response")
	}
	if q.Action == "probe" {
		if v.Result.Class != "probe_passed" || v.Result.ExitCode != 0 || v.Result.Tests != 0 || v.Result.Truncated {
			return WireResponse{}, errors.New("VM self-check failed")
		}
	} else {
		switch v.Result.Class {
		case "passed":
			if v.Result.Tests <= 0 || v.Result.Tests > 4096 || v.Result.ExitCode != 0 || v.Result.Truncated {
				return WireResponse{}, errors.New("invalid passing VM result")
			}
		case "test_failed":
			if v.Result.Tests <= 0 || v.Result.Tests > 4096 || v.Result.ExitCode <= 0 || v.Result.ExitCode >= 125 || v.Result.Truncated {
				return WireResponse{}, errors.New("invalid failing VM result")
			}
		case "infrastructure_failed", "timeout":
		default:
			return WireResponse{}, errors.New("invalid VM result class")
		}
	}
	return v, nil
}
