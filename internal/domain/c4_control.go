package domain

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"time"
)

const C4ControlLimit = 64
const C4ControlTTL = 10 * time.Minute

// C4Control is immutable authority material followed by an append-only lifecycle.
// SourceKind records a trusted runtime capability, never a request-selected mode.
type C4Control struct {
	SchemaVersion       int              `json:"schema_version"`
	ID                  string           `json:"id"`
	Kind                string           `json:"kind"`
	MissionID           string           `json:"mission_id"`
	Epoch               int64            `json:"epoch"`
	PlanRevision        int64            `json:"plan_revision"`
	BaseCommit          string           `json:"base_commit"`
	BaseDigest          string           `json:"base_digest"`
	ProfileDigest       string           `json:"profile_digest"`
	ActivationDigest    string           `json:"activation_digest"`
	ActivationExpiresAt string           `json:"activation_expires_at"`
	SourceKind          string           `json:"source_kind"`
	PreparedRevision    int64            `json:"prepared_revision"`
	PreparedAt          string           `json:"prepared_at"`
	ExpiresAt           string           `json:"expires_at"`
	OperationID         string           `json:"operation_id,omitempty"`
	OperationDigest     string           `json:"operation_digest,omitempty"`
	ContainerID         string           `json:"container_id,omitempty"`
	MaterialDigest      string           `json:"material_digest"`
	State               string           `json:"state"`
	Approval            *C4Approval      `json:"approval,omitempty"`
	Dispatch            *C4Fact          `json:"dispatch,omitempty"`
	Result              *C4ControlResult `json:"result,omitempty"`
}

type C4ControlResult struct {
	Status      string `json:"status"`
	FailureCode string `json:"failure_code,omitempty"`
	Revision    int64  `json:"revision"`
	At          string `json:"at"`
	ValidUntil  string `json:"valid_until,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func C4ControlDigest(c C4Control) string {
	c.MaterialDigest = ""
	c.State = ""
	c.Approval = nil
	c.Dispatch = nil
	c.Result = nil
	return c4Digest("control", c)
}

var errC4Control = errors.New("invalid C4 control authority or lifecycle")

// ValidateC4Operation checks structure only, for an already authorized sandbox
// plan. It cannot establish mission authority; store/engine use ValidateC4Mission.
func ValidateC4Operation(p C4Profile, o C4Operation) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ControlVersion == 1 {
		if !c4Opaque.MatchString(o.ReadinessID) || !c4SHA.MatchString(o.ReadinessDigest) {
			return errC4Control
		}
	} else if o.ReadinessID != "" || o.ReadinessDigest != "" {
		return errC4Control
	}
	return validateC4Operation(Mission{ID: o.MissionID, Epoch: o.Epoch, Revision: math.MaxInt64, Spec: Spec{C4: &p}}, o)
}

func ValidateC4Controls(m Mission) error {
	if m.C4Invalid || len(m.C4Controls) > C4ControlLimit {
		return errC4Control
	}
	if m.Spec.C4 == nil || m.Spec.C4.ControlVersion == 0 {
		if len(m.C4Controls) > 0 {
			return errC4Control
		}
		return nil
	}
	if m.Spec.C4.ControlVersion != 1 {
		return errC4Control
	}
	if err := m.Spec.C4.Validate(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range m.C4Controls {
		if seen[c.ID] {
			return errC4Control
		}
		seen[c.ID] = true
		if err := validateC4Control(m, c); err != nil {
			return err
		}
	}
	return nil
}

func validateC4Control(m Mission, c C4Control) error {
	source := "synthetic_c4_control"
	if m.Spec.C4.Mode == "docker" {
		source = "docker_c4_control"
	}
	if c.SchemaVersion != 1 || !c4Opaque.MatchString(c.ID) || c.MissionID != m.ID || c.Epoch < 0 || c.Epoch > m.Epoch || c.PlanRevision < 0 || !c4Commit.MatchString(c.BaseCommit) || !c4SHA.MatchString(c.BaseDigest) || c.ProfileDigest != m.Spec.C4.ProfileDigest() || !c4SHA.MatchString(c.ActivationDigest) || c.SourceKind != source || c.MaterialDigest != C4ControlDigest(c) || c.PreparedRevision < 1 || c.PreparedRevision > m.Revision {
		return errC4Control
	}
	prepared, e1 := time.Parse(time.RFC3339Nano, c.PreparedAt)
	expires, e2 := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	activation, e3 := time.Parse(time.RFC3339Nano, c.ActivationExpiresAt)
	if e1 != nil || e2 != nil || e3 != nil || !expires.After(prepared) || expires.After(prepared.Add(C4ControlTTL)) || expires.After(activation) {
		return errC4Control
	}
	switch c.Kind {
	case "readiness":
		if c.OperationID != "" || c.OperationDigest != "" || c.ContainerID != "" {
			return errC4Control
		}
	case "cleanup":
		if !c4Opaque.MatchString(c.OperationID) || !c4SHA.MatchString(c.OperationDigest) || !c4SHA.MatchString(c.ContainerID) {
			return errC4Control
		}
		target := c4CleanupTarget(m, c)
		if target == nil || target.Result == nil || target.Dispatch == nil {
			return errC4Control
		}
	default:
		return errC4Control
	}
	last, rev := prepared, c.PreparedRevision
	check := func(r int64, at string, bounded bool) bool {
		t, e := time.Parse(time.RFC3339Nano, at)
		if e != nil || t.Before(last) || r < rev || r > m.Revision || (bounded && (!t.Before(expires) || !t.Before(activation))) {
			return false
		}
		last, rev = t, r
		return true
	}
	state := "prepared"
	if c.Approval != nil {
		if c.Approval.Digest != c.MaterialDigest || !check(c.Approval.Revision, c.Approval.At, true) {
			return errC4Control
		}
		state = "approved"
	}
	if c.Dispatch != nil {
		if state != "approved" || c.Dispatch.Revision <= rev || !check(c.Dispatch.Revision, c.Dispatch.At, true) {
			return errC4Control
		}
		state = "dispatched"
	}
	if r := c.Result; r != nil {
		if state != "dispatched" || r.Revision <= rev || !check(r.Revision, r.At, false) {
			return errC4Control
		}
		switch r.Status {
		case "ready":
			until, e := time.Parse(time.RFC3339Nano, r.ValidUntil)
			if c.Kind != "readiness" || r.FailureCode != "" || !c4SHA.MatchString(r.Fingerprint) || e != nil || !until.After(last) || until.After(last.Add(C4ControlTTL)) || until.After(activation) {
				return errC4Control
			}
		case "cleaned":
			if c.Kind != "cleanup" || r.FailureCode != "" || r.ValidUntil != "" || r.Fingerprint != "" {
				return errC4Control
			}
		case "failed", "outcome_unknown":
			if r.ValidUntil != "" || r.Fingerprint != "" || !c4ControlFailureCode(r.FailureCode) {
				return errC4Control
			}
			if C4ReadinessFailureCode(r.FailureCode) && (c.Kind != "readiness" || r.Status != "failed") {
				return errC4Control
			}
		default:
			return errC4Control
		}
		state = "completed"
	}
	if state != c.State {
		return errC4Control
	}
	return nil
}

// Only bounded, static codes are journal-safe; transport text is never a code.
func c4ControlFailureCode(code string) bool {
	switch code {
	case "unavailable", "failed", "timeout", "canceled", "outcome_unknown", "persistence_failed", "interrupted", "trust_failed", "protocol_failed", "stale_authority":
		return true
	}
	return C4ReadinessFailureCode(code)
}

// C4ReadinessFailureCode admits only the fixed query stage and static reason.
// It validates evidence, not readiness/approval authority; old generic failures
// stay readable and must never be retroactively assigned an inferred phase.
func C4ReadinessFailureCode(code string) bool {
	if code == "readiness_gate_failed" {
		return true
	}
	stage, reason, ok := strings.Cut(code, "_")
	if !ok || (stage != "version" && stage != "info" && stage != "image") {
		return false
	}
	switch reason {
	case "trust_failed", "process_failed", "command_failed", "output_limit", "protocol_failed", "timeout", "canceled":
		return true
	}
	return false
}

func c4CleanupTarget(m Mission, c C4Control) *C4Operation {
	for i := range m.C4Operations {
		o := &m.C4Operations[i]
		if o.ID == c.OperationID && o.MaterialDigest == c.OperationDigest && o.ContainerID == c.ContainerID && o.BaseCommit == c.BaseCommit && o.BaseDigest == c.BaseDigest && o.ProfileDigest == c.ProfileDigest {
			for _, r := range m.C4Controls {
				// Recovery may use a newly confirmed activation after the original
				// expires. Preserve target/profile/base/provenance, not old consent.
				// The engine matches this new material to current trusted activation.
				if r.ID == o.ReadinessID && r.MaterialDigest == o.ReadinessDigest && r.SourceKind == c.SourceKind {
					return o
				}
			}
		}
	}
	return nil
}

func c4ControlCurrent(m Mission, c C4Control) bool {
	plan := int64(0)
	if m.Plan != nil {
		plan = m.Plan.Revision
	}
	return c.Epoch == m.Epoch && c.PlanRevision == plan && c.BaseCommit == m.BaseCommit && m.Spec.C4 != nil && c.ProfileDigest == m.Spec.C4.ProfileDigest()
}

func ValidateC4ControlTransition(before, after Mission) error {
	if ValidateC4Controls(before) != nil || ValidateC4Controls(after) != nil || !reflect.DeepEqual(before.Spec.C4, after.Spec.C4) || len(after.C4Controls) < len(before.C4Controls) {
		return errC4Control
	}
	for i, a := range before.C4Controls {
		b := after.C4Controls[i]
		if a.MaterialDigest != b.MaterialDigest || a.Approval != nil && !reflect.DeepEqual(a.Approval, b.Approval) || a.Dispatch != nil && !reflect.DeepEqual(a.Dispatch, b.Dispatch) || a.Result != nil && !reflect.DeepEqual(a.Result, b.Result) {
			return errC4Control
		}
		added := 0
		authority := false
		if a.Approval == nil && b.Approval != nil {
			added++
			authority = true
			if b.Approval.Revision != before.Revision {
				return errC4Control
			}
		}
		if a.Dispatch == nil && b.Dispatch != nil {
			added++
			authority = true
			if b.Dispatch.Revision != before.Revision {
				return errC4Control
			}
		}
		if a.Result == nil && b.Result != nil {
			added++
			if b.Result.Revision != before.Revision {
				return errC4Control
			}
		}
		if added > 1 {
			return errC4Control
		}
		if authority && (!c4ControlCurrent(before, a) || !c4ControlCurrent(after, b) || !c4CleanupAvailable(before, a) || !c4CleanupAvailable(after, b)) {
			return errC4Control
		}
	}
	for _, c := range after.C4Controls[len(before.C4Controls):] {
		if c.State != "prepared" || c.PreparedRevision != before.Revision || !c4ControlCurrent(before, c) || !c4ControlCurrent(after, c) || !c4CleanupAvailable(before, c) || !c4CleanupAvailable(after, c) {
			return errC4Control
		}
	}
	return nil
}
func c4CleanupAvailable(m Mission, c C4Control) bool {
	if c.Kind != "cleanup" {
		return true
	}
	o := c4CleanupTarget(m, c)
	return o != nil && o.Dispatch != nil && o.Result != nil && o.CleanupState() != "confirmed"
}

func validateC4ReadinessBinding(m Mission, o C4Operation) error {
	if m.Spec.C4.ControlVersion == 0 {
		if o.ReadinessID != "" || o.ReadinessDigest != "" {
			return errC4Control
		}
		return nil
	}
	if !c4Opaque.MatchString(o.ReadinessID) || !c4SHA.MatchString(o.ReadinessDigest) {
		return errC4Control
	}
	for _, c := range m.C4Controls {
		if c.ID != o.ReadinessID || c.MaterialDigest != o.ReadinessDigest {
			continue
		}
		if c.Kind != "readiness" || c.Result == nil || c.Result.Status != "ready" || c.BaseCommit != o.BaseCommit || c.BaseDigest != o.BaseDigest || c.ProfileDigest != o.ProfileDigest {
			return errC4Control
		}
		ready, _ := time.Parse(time.RFC3339Nano, c.Result.At)
		until, _ := time.Parse(time.RFC3339Nano, c.Result.ValidUntil)
		fresh := func(at string) bool {
			t, e := time.Parse(time.RFC3339Nano, at)
			return e == nil && !t.Before(ready) && t.Before(until)
		}
		if !fresh(o.PreparedAt) || o.Dispatch != nil && (!fresh(o.Dispatch.At) || o.Dispatch.Revision < c.Result.Revision) {
			return errC4Control
		}
		return nil
	}
	return errC4Control
}
