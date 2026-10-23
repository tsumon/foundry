package vmacceptance

import (
	"bufio"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"regexp"
	"strings"
)

const SummaryKind = "foundry-native-vm-acceptance"
const ModelProvenance = "local_protocol_fixture/not_real_provider"

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

type OperationEvidence struct {
	Kind             string `json:"kind"`
	Class            string `json:"class"`
	Tests            int    `json:"tests"`
	InstanceID       string `json:"instance_id"`
	CleanupConfirmed bool   `json:"cleanup_confirmed"`
	CandidateDigest  string `json:"candidate_digest"`
}
type Checks struct {
	ApprovalGates      bool `json:"approval_gates"`
	ProtectedUnchanged bool `json:"protected_unchanged"`
	SourceUnchanged    bool `json:"source_unchanged"`
	DeliveryFrozen     bool `json:"delivery_frozen"`
	ExactInstances     bool `json:"exact_instances"`
	LocalModelOnly     bool `json:"local_model_only"`
	QAAuditorBound     bool `json:"qa_auditor_bound"`
}
type Summary struct {
	SchemaVersion    int                 `json:"schema_version"`
	Kind             string              `json:"kind"`
	Passed           bool                `json:"passed"`
	Source           string              `json:"source"`
	Models           string              `json:"models"`
	HostPlatform     string              `json:"host_platform"`
	GuestPlatform    string              `json:"guest_platform"`
	ManifestDigest   string              `json:"manifest_digest"`
	Acceleration     string              `json:"acceleration"`
	MissionID        string              `json:"mission_id"`
	RoleCounts       map[domain.Role]int `json:"role_counts"`
	Operations       []OperationEvidence `json:"operations"`
	Checks           Checks              `json:"checks"`
	Stage            string              `json:"stage"`
	FailureCode      string              `json:"failure_code,omitempty"`
	ProtectedDigests map[string]string   `json:"protected_digests,omitempty"`
	SourceDigest     string              `json:"source_digest,omitempty"`
	PatchDigest      string              `json:"patch_digest,omitempty"`
	ReportDigest     string              `json:"report_digest,omitempty"`
}

func operationEvidence(ops []domain.C4Operation) ([]OperationEvidence, error) {
	if len(ops) != 3 {
		return nil, errors.New("exactly_three_vm_operations_required")
	}
	seen := map[string]bool{}
	out := make([]OperationEvidence, 0, 3)
	for i, o := range ops {
		kind, class := "candidate", "passed"
		if i == 0 {
			kind = "baseline"
		}
		if i == 1 {
			class = "test_failed"
		}
		if o.SchemaVersion != 1 || o.Kind != kind || o.State != "finished" || o.Approval == nil || o.Dispatch == nil || o.Created == nil || o.Started == nil || o.Result == nil || o.Result.Class != class || o.Result.Tests != 2 || o.Result.Truncated || o.Result.ExitCode == nil || !sha256Hex.MatchString(o.ContainerID) || seen[o.ContainerID] || o.CleanupState() != "confirmed" || len(o.Cleanup) != 1 {
			return nil, errors.New("native_vm_evidence_incomplete")
		}
		if class == "passed" && !o.Passed() || class == "test_failed" && *o.Result.ExitCode == 0 {
			return nil, errors.New("native_vm_result_inconsistent")
		}
		if !exactTestEvents(o.Result.Output, i == 1) {
			return nil, errors.New("native_test_events_not_exact")
		}
		cleanup := o.Cleanup[0]
		if cleanup.ContainerID != o.ContainerID || cleanup.Owner != o.Owner {
			return nil, errors.New("native_vm_cleanup_not_owned")
		}
		digest := o.CandidateDigest
		if i == 0 {
			digest = o.BaseDigest
		}
		if !sha256Hex.MatchString(digest) {
			return nil, errors.New("native_vm_candidate_digest_invalid")
		}
		seen[o.ContainerID] = true
		out = append(out, OperationEvidence{Kind: kind, Class: class, Tests: o.Result.Tests, InstanceID: o.ContainerID, CleanupConfirmed: true, CandidateDigest: digest})
	}
	if ops[1].CandidateDigest == ops[2].CandidateDigest {
		return nil, errors.New("repair_generation_not_changed")
	}
	return out, nil
}

func exactTestEvents(output string, negativeFailure bool) bool {
	seen := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		var event struct{ Action, Test, Package string }
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		if event.Test == "" || event.Action != "pass" && event.Action != "fail" && event.Action != "skip" {
			continue
		}
		if event.Package != "foundry-native-acceptance" || seen[event.Test] != "" {
			return false
		}
		seen[event.Test] = event.Action
	}
	want := "pass"
	if negativeFailure {
		want = "fail"
	}
	return scanner.Err() == nil && len(seen) == 2 && seen["TestTriplePositive"] == "pass" && seen["TestTripleNegative"] == want
}

func verificationEvidence(m domain.Mission) error {
	if len(m.C4Operations) != 3 || len(m.Evidence) != 2 {
		return errors.New("verification_evidence_count_mismatch")
	}
	for i, v := range m.Evidence {
		o := m.C4Operations[i+1]
		if v.SourceVersion != 1 || v.SourceKind != "isolated_execution" || v.ID != o.ID || v.CandidateDigest != o.CandidateDigest || v.ImageID != m.Spec.ImageID || v.Passed != o.Passed() {
			return errors.New("verification_evidence_not_native_correlated")
		}
	}
	return nil
}
