package vmruntime

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/c4verify"
	"strings"
	"testing"
)

func checksV2Test() SelfCheckV2 {
	return SelfCheckV2{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true, ReadOnlyDependencies: true, RuntimeInventoryMatches: true, InputBindingValid: true, PhaseCleanupConfirmed: true}
}
func phasesV2Test(q WireRequestV2) []PhaseEvidenceV2 {
	names := []string{"test-node"}
	if q.Target.ProfileID == "c4_python_model_v2" {
		names = []string{"test-python"}
	}
	if q.Target.ProfileID == "c4_typescript_model_v2" {
		names = []string{"compile-typescript", "test-node"}
	}
	if q.Action == "probe" {
		names = []string{"probe-node"}
		if q.Target.ProfileID == "c4_python_model_v2" {
			names = []string{"probe-python"}
		}
		if q.Target.ProfileID == "c4_typescript_model_v2" {
			names = []string{"probe-typescript"}
		}
	}
	out := []PhaseEvidenceV2{}
	for _, name := range names {
		p := PhaseEvidenceV2{Phase: name, Exited: true, ResourceLimitsOK: true, CleanupConfirmed: true}
		if name == "compile-typescript" {
			p.CompiledOutputDigest = strings.Repeat("e", 64)
		}
		out = append(out, p)
	}
	return out
}

// Independently implemented MAC lets reader tests inject authenticated invalid
// evidence without relying on the writer's validation or private MAC helper.
func signedResponseV2Test(q WireRequestV2, r WireResponseV2) []byte {
	r.MAC = ""
	raw, _ := json.Marshal(r)
	key, _ := hex.DecodeString(q.Secret)
	h := hmac.New(sha256.New, key)
	h.Write([]byte("foundry:vm:response:v2\n"))
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(raw)))
	h.Write(n[:])
	h.Write(raw)
	r.MAC = hex.EncodeToString(h.Sum(nil))
	raw, _ = json.Marshal(r)
	return rawV2Test(raw)
}
func responseV2Test(q WireRequestV2) WireResponseV2 {
	d, _ := RequestDigestV2(q)
	r := c4verify.Result{Class: "passed", Tests: 1}
	if q.Action == "probe" {
		r = c4verify.Result{Class: "probe_passed"}
	}
	return WireResponseV2{SchemaVersion: 2, Action: q.Action, Nonce: q.Nonce, RequestDigest: d, Checks: checksV2Test(), Result: r, Phases: phasesV2Test(q)}
}
func TestV2ResponsesAuthenticateExactSelectedPhaseSequences(t *testing.T) {
	for _, id := range []string{"c4_node_model_v2", "c4_typescript_model_v2", "c4_python_model_v2"} {
		verify, _, _ := fixtureV2Test(t, id)
		for _, q := range []WireRequestV2{probeV2Test(id), verify} {
			r := responseV2Test(q)
			var b bytes.Buffer
			if e := WriteResponseV2(&b, q, r.Checks, r.Result, r.Phases); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(b.Bytes(), signedResponseV2Test(q, r)) {
				t.Fatal("MAC domain/length/key mismatch")
			}
			got, e := ReadResponseV2(&b, q)
			if e != nil || got.Result != r.Result {
				t.Fatal("response", e)
			}
			if q.Action == "verify" {
				r.Result = c4verify.Result{Class: "test_failed", Tests: 1, ExitCode: 1}
				r.Phases[len(r.Phases)-1].ExitCode = 1
				if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e != nil {
					t.Fatal("legitimate assertion failure", e)
				}
			}
		}
	}
}
func TestV2ResponsesRejectAuthenticatedUnsafeEvidence(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_typescript_model_v2")
	changes := map[string]func(*WireResponseV2){
		"schema": func(r *WireResponseV2) { r.SchemaVersion = 1 }, "nonce": func(r *WireResponseV2) { r.Nonce = strings.Repeat("d", 64) }, "action": func(r *WireResponseV2) { r.Action = "probe" }, "request": func(r *WireResponseV2) { r.RequestDigest = strings.Repeat("f", 64) },
		"missing phase": func(r *WireResponseV2) { r.Phases = r.Phases[:1] }, "reverse": func(r *WireResponseV2) { r.Phases[0], r.Phases[1] = r.Phases[1], r.Phases[0] }, "other profile phase": func(r *WireResponseV2) { r.Phases[1].Phase = "test-python" }, "duplicate": func(r *WireResponseV2) { r.Phases = append(r.Phases, r.Phases[1]) }, "null phases": func(r *WireResponseV2) { r.Phases = nil },
		"compiler missing output": func(r *WireResponseV2) { r.Phases[0].CompiledOutputDigest = "" }, "compiler failed then test": func(r *WireResponseV2) {
			r.Result.Class = "infrastructure_failed"
			r.Phases[0].ExitCode = 1
			r.Phases[0].CompiledOutputDigest = ""
		}, "test output digest": func(r *WireResponseV2) { r.Phases[1].CompiledOutputDigest = strings.Repeat("e", 64) },
		"unobserved exit": func(r *WireResponseV2) { r.Phases[1].Exited = false }, "signal": func(r *WireResponseV2) { r.Phases[1].ExitCode = -1 }, "resource": func(r *WireResponseV2) { r.Phases[1].ResourceLimitsOK = false }, "cleanup": func(r *WireResponseV2) { r.Phases[1].CleanupConfirmed = false }, "deadline": func(r *WireResponseV2) { r.Phases[1].DeadlineExceeded = true }, "cancel": func(r *WireResponseV2) { r.Phases[1].Canceled = true },
		"zero tests": func(r *WireResponseV2) { r.Result.Tests = 0 }, "too many tests": func(r *WireResponseV2) { r.Result.Tests = 4097 }, "negative tests": func(r *WireResponseV2) { r.Result.Tests = -1 }, "exit mismatch": func(r *WireResponseV2) { r.Result.ExitCode = 1 }, "truncated": func(r *WireResponseV2) { r.Result.Truncated = true }, "output limit": func(r *WireResponseV2) { r.Result.Output = strings.Repeat("x", 65537) }, "class": func(r *WireResponseV2) { r.Result.Class = "success" },
		"incomplete positive count": func(r *WireResponseV2) { r.Result.Class = "infrastructure_failed"; r.Phases = r.Phases[:1] }, "timeout without deadline": func(r *WireResponseV2) { r.Result.Class = "timeout" }, "timeout and cancellation": func(r *WireResponseV2) {
			r.Result.Class = "timeout"
			r.Phases[1].DeadlineExceeded = true
			r.Phases[1].Canceled = true
		}, "deadline infrastructure": func(r *WireResponseV2) { r.Result.Class = "infrastructure_failed"; r.Phases[1].DeadlineExceeded = true },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			r := responseV2Test(q)
			change(&r)
			if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e == nil {
				t.Fatal("unsafe evidence accepted")
			}
			var b bytes.Buffer
			if WriteResponseV2(&b, q, r.Checks, r.Result, r.Phases) == nil && r.SchemaVersion == 2 && r.Nonce == q.Nonce && r.Action == q.Action && r.RequestDigest == responseV2Test(q).RequestDigest {
				t.Fatal("writer emitted unsafe evidence")
			}
		})
	}
	raw, _ := json.Marshal(checksV2Test())
	var checks map[string]bool
	json.Unmarshal(raw, &checks)
	for name := range checks {
		t.Run(name, func(t *testing.T) {
			r := responseV2Test(q)
			copy := map[string]bool{}
			for k, v := range checks {
				copy[k] = v
			}
			copy[name] = false
			raw, _ := json.Marshal(copy)
			json.Unmarshal(raw, &r.Checks)
			if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e == nil {
				t.Fatal("failed check accepted")
			}
		})
	}
}
func TestV2ResponsesAllowOnlyConsistentFailurePrefixes(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_typescript_model_v2")
	for _, phases := range [][]PhaseEvidenceV2{{}, {{Phase: "compile-typescript", Exited: true, ExitCode: 1, ResourceLimitsOK: true, CleanupConfirmed: true}}, {{Phase: "compile-typescript", DeadlineExceeded: true, ResourceLimitsOK: true, CleanupConfirmed: true}}} {
		r := responseV2Test(q)
		r.Phases = phases
		r.Result = c4verify.Result{Class: "infrastructure_failed"}
		if len(phases) > 0 {
			r.Result.ExitCode = phases[0].ExitCode
			if phases[0].DeadlineExceeded {
				r.Result.Class = "timeout"
			}
		}
		if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e != nil {
			t.Fatal("valid infrastructure prefix", e)
		}
	}
	// Failed self checks must remain authenticated and nonpassing.
	r := responseV2Test(q)
	r.Phases = []PhaseEvidenceV2{}
	r.Checks.InputBindingValid = false
	r.Result = c4verify.Result{Class: "infrastructure_failed"}
	if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e != nil {
		t.Fatal(e)
	}
}
func TestV2ResponsesRejectMovedTranscriptAndMACErrors(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	r := responseV2Test(q)
	wire := signedResponseV2Test(q, r)
	changes := map[string]func(*WireRequestV2){
		"owner": func(q *WireRequestV2) { q.Verify.OwnerDigest = strings.Repeat("e", 64) }, "nonce": func(q *WireRequestV2) { q.Nonce = strings.Repeat("e", 64) }, "key": func(q *WireRequestV2) { q.Secret = strings.Repeat("e", 64) }, "candidate": func(q *WireRequestV2) { q.Verify.CandidateDigest = strings.Repeat("e", 64) },
		"inventory": func(q *WireRequestV2) {
			q.Target.RuntimeInventoryDigest = strings.Repeat("e", 64)
			q.Verify.Profile.V2.RuntimeInventoryDigest = q.Target.RuntimeInventoryDigest
			q.Verify.ProfileDigest = q.Verify.Profile.ProfileDigest()
		},
		"source": func(q *WireRequestV2) { q.Verify.SourceFiles[2].SHA256 = strings.Repeat("e", 64) },
		"admission": func(q *WireRequestV2) {
			q.Verify.Inputs.AdmissionDigest = strings.Repeat("e", 64)
			c, e := q.Verify.Inputs.WithDigests()
			if e != nil {
				t.Fatal(e)
			}
			q.Verify.Inputs = c
			q.Verify.Profile.V2.ExecutionContractDigest = c.ExecutionContractDigest
			q.Verify.ProfileDigest = q.Verify.Profile.ProfileDigest()
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(q)
			var changed WireRequestV2
			json.Unmarshal(raw, &changed)
			change(&changed)
			if ValidateRequestV2(changed) != nil {
				t.Fatal("mutation must be independently valid")
			}
			if _, e := ReadResponseV2(bytes.NewReader(wire), changed); e == nil {
				t.Fatal("moved transcript accepted")
			}
		})
	}
	for _, mutate := range []func([]byte) []byte{func(b []byte) []byte { b[len(b)-3] ^= 1; return b }, func(b []byte) []byte { return b[:len(b)-1] }, func(b []byte) []byte { binary.BigEndian.PutUint32(b[len(HeaderV2):], 128<<10+1); return b }} {
		if _, e := ReadResponseV2(bytes.NewReader(mutate(bytes.Clone(wire))), q); e == nil {
			t.Fatal("invalid MAC/frame accepted")
		}
	}
	// Secret does not enter RequestDigest, but still keys the response.
	other := q
	other.Secret = strings.Repeat("f", 64)
	a, _ := RequestDigestV2(q)
	b, _ := RequestDigestV2(other)
	if a != b {
		t.Fatal("request digest includes secret")
	}
}
func TestV2ResponsesRequireAllExactKeys(t *testing.T) {
	q := probeV2Test("c4_node_model_v2")
	wire := signedResponseV2Test(q, responseV2Test(q))
	raw := string(wire[len(HeaderV2)+4:])
	for _, changed := range []string{strings.Replace(raw, `"phase_cleanup_confirmed":true`, `"phase_cleanup_confirmed":null`, 1), strings.Replace(raw, `"exited":true`, `"Exited":true`, 1), strings.Replace(raw, `"exit_code":0`, `"exit_code":-0`, 1), strings.Replace(raw, `"canceled":false,`, "", 1), strings.Replace(raw, `"exited":true`, `"exited":true,"exited":true`, 1), strings.Replace(raw, `"phases":[`, `"untrusted":1,"phases":[`, 1)} {
		if _, e := ReadResponseV2(bytes.NewReader(rawV2Test([]byte(changed))), q); e == nil {
			t.Fatal("bad schema accepted")
		}
	}
}

func TestV2ResponsesRejectInconsistentCleanupClaim(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	r := responseV2Test(q)
	r.Result.Class = "infrastructure_failed"
	r.Phases[0].CleanupConfirmed = false
	if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e == nil {
		t.Fatal("aggregate phase cleanup claims success over an unclean phase")
	}
	r.Checks.PhaseCleanupConfirmed = false
	if _, e := ReadResponseV2(bytes.NewReader(signedResponseV2Test(q, r)), q); e != nil {
		t.Fatal("consistent nonpassing cleanup failure rejected", e)
	}
}

func TestV2ResponsesRejectReboundLockTestsDependenciesAndIdentity(t *testing.T) {
	q, _, _ := fixtureV2Test(t, "c4_node_model_v2")
	wire := signedResponseV2Test(q, responseV2Test(q))
	for _, boundary := range []string{"lock", "tests", "dependencies", "identity"} {
		t.Run(boundary, func(t *testing.T) {
			raw, _ := json.Marshal(q)
			var other WireRequestV2
			json.Unmarshal(raw, &other)
			v := other.Verify
			newHash := strings.Repeat("f", 64)
			switch boundary {
			case "dependencies":
				v.DependencyManifest.Files[0].SHA256 = newHash
			case "identity":
				other.Target.Identity.RootFSDigest = newHash
				*v.Profile.EmbeddedVM = other.Target.Identity
			case "lock", "tests":
				selected := "package-lock.json"
				if boundary == "tests" {
					selected = "tests/a.test.js"
				}
				for i := range v.SourceFiles {
					if v.SourceFiles[i].Path == selected {
						v.SourceFiles[i].SHA256 = newHash
					}
				}
				for i := range v.Inputs.Protected {
					if v.Inputs.Protected[i].Path == selected {
						v.Inputs.Protected[i].SHA256 = newHash
					}
				}
				if boundary == "lock" {
					v.DependencyManifest.LockSHA256 = newHash
					for i := range v.Inputs.SourceFiles {
						if v.Inputs.SourceFiles[i].Path == selected {
							v.Inputs.SourceFiles[i].SHA256 = newHash
						}
					}
				}
			}
			rebindV2Test(t, &other)
			if ValidateRequestV2(other) != nil {
				t.Fatal("rebound request is not independently valid")
			}
			if _, e := ReadResponseV2(bytes.NewReader(wire), other); e == nil {
				t.Fatal("transcript crossed immutable boundary")
			}
		})
	}
	for _, id := range []string{"c4_python_model_v2", "c4_typescript_model_v2"} {
		other, _, _ := fixtureV2Test(t, id)
		if _, e := ReadResponseV2(bytes.NewReader(wire), other); e == nil {
			t.Fatal("cross-profile replay")
		}
	}
	other := probeV2Test("c4_node_model_v2")
	if _, e := ReadResponseV2(bytes.NewReader(wire), other); e == nil {
		t.Fatal("cross-action replay")
	}
}
func TestV2ResponseLeavesTrailingBytesForLifecycleEOFCheck(t *testing.T) {
	q := probeV2Test("c4_node_model_v2")
	wire := signedResponseV2Test(q, responseV2Test(q))
	r := bytes.NewReader(append(wire, 42))
	if _, e := ReadResponseV2(r, q); e != nil || r.Len() != 1 {
		t.Fatal("response reader hid trailing bytes", e)
	}
}
