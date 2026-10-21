package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const FailureBundleLimit = 128 * 1024
const FailureLogLimit = 8 * 1024

type CodingFailure struct {
	CommittedOperationID string `json:"committed_operation_id,omitempty"`
	ID                   string `json:"id"`
	Code                 string `json:"code"`
	Phase                string `json:"phase"`
	AttemptID            string `json:"attempt_id,omitempty"`
	VerificationID       string `json:"verification_id,omitempty"`
	ReviewRole           Role   `json:"review_role,omitempty"`
	Summary              string `json:"summary"`
	LogExcerpt           string `json:"log_excerpt,omitempty"`
	LogTruncated         bool   `json:"log_truncated,omitempty"`
	SourceDigest         string `json:"source_digest,omitempty"`
}
type FailureBundle struct {
	SchemaVersion       int             `json:"schema_version"`
	MissionID           string          `json:"mission_id"`
	Epoch               int64           `json:"epoch"`
	PlanRevision        int64           `json:"plan_revision"`
	BaseCommit          string          `json:"base_commit"`
	CandidateGeneration GenerationRef   `json:"candidate_generation"`
	ArchiveDigest       string          `json:"archive_digest"`
	TraceDigest         string          `json:"trace_digest"`
	Digest              string          `json:"digest,omitempty"`
	Failures            []CodingFailure `json:"failures"`
}

func (b *FailureBundle) UnmarshalJSON(raw []byte) error {
	type plain FailureBundle
	var out plain
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return err
	}
	*b = FailureBundle(out)
	return nil
}

type PlanAdjustment struct {
	FailureID  string   `json:"failure_id"`
	StepIDs    []string `json:"step_ids"`
	Reason     string   `json:"reason"`
	Unresolved bool     `json:"unresolved"`
}

func codingDigest(kind string, v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(append([]byte("FOUNDRY coding "+kind+" v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}
func CodingTraceDigest(m Mission) string      { return codingDigest("trace", m.CodingAttempts) }
func CodingArchiveDigest(a RunArchive) string { return codingDigest("archive", a) }

var credentialAssignment = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|passwd|secret|authorization)\b["']?\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s&,;]+(?:\s+[^\s&,;]+)?)`)
var bearerCredential = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
var credentialURL = regexp.MustCompile(`(?i)(https?://)[^\s/@]+@`)
var queryParameter = regexp.MustCompile(`([?&])[^\s=&?#]+=[^\s&#]*`)

func boundedFailureText(text string, limit int) (string, bool) {
	text = strings.ToValidUTF8(text, "�")
	text = credentialURL.ReplaceAllString(text, "${1}[REDACTED]@")
	text = queryParameter.ReplaceAllString(text, "${1}[REDACTED]")
	text = bearerCredential.ReplaceAllString(text, "[REDACTED]")
	text = credentialAssignment.ReplaceAllString(text, "${1}=[REDACTED]")
	if len(text) <= limit {
		return text, false
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text, true
}

// BuildFailureBundle accepts only a controller trace and its exact archived run.
// Raw errors never enter the projection. All summaries are controller vocabulary;
// logs are bounded untrusted data and retain their original record digest.
func BuildFailureBundle(m Mission, a RunArchive) (*FailureBundle, error) {
	bad := errors.New("incomplete or mismatched coding failure material")
	if m.CodingVersion != 1 || a.Plan == nil || a.PlanRevision != a.Plan.Revision || a.CandidateGeneration == nil || a.CandidateGeneration.Digest != a.CandidateDigest || a.CandidateGeneration.ID == "" || len(a.CodingAttempts) == 0 {
		return nil, bad
	}
	trace := m
	trace.Epoch = a.Epoch
	trace.CodingAttempts = a.CodingAttempts
	if err := ValidateCodingTrace(trace); err != nil {
		return nil, err
	}
	b := &FailureBundle{SchemaVersion: 1, MissionID: m.ID, Epoch: a.Epoch, PlanRevision: a.PlanRevision, BaseCommit: m.BaseCommit, CandidateGeneration: *a.CandidateGeneration, ArchiveDigest: CodingArchiveDigest(a), TraceDigest: CodingTraceDigest(trace)}
	evidence := map[string]Verification{}
	for _, v := range a.Evidence {
		if v.ID == "" || evidence[v.ID].ID != "" {
			return nil, bad
		}
		evidence[v.ID] = v
	}
	used := map[string]bool{}
	if a.RecoveredSelection != nil {
		if err := ValidateRecoveredSelection(m, a); err != nil {
			return nil, err
		}
	}
	var last *GenerationRef
	for i, attempt := range a.CodingAttempts {
		if attempt.Epoch != a.Epoch || attempt.PlanRevision != a.PlanRevision || attempt.Status == "running" {
			return nil, bad
		}
		if last != nil && !reflect.DeepEqual(last, attempt.Before) {
			return nil, bad
		}
		last = attempt.Before
		if attempt.After != nil {
			last = attempt.After
		}
		if attempt.Status == "succeeded" {
			continue
		}
		f := CodingFailure{ID: "failure-" + attempt.ID, Code: attempt.FailureCode, Phase: attempt.Phase, AttemptID: attempt.ID, Summary: attempt.Phase + ": " + attempt.FailureCode, SourceDigest: codingDigest("attempt", attempt)}
		if i == len(a.CodingAttempts)-1 && a.RecoveredSelection != nil {
			last = a.RecoveredSelection.Result.Generation
			f.CommittedOperationID = a.RecoveredSelection.ID
			f.Summary += "; local candidate selection committed; execution remains interrupted"
		}
		if attempt.VerificationID != "" {
			v, ok := evidence[attempt.VerificationID]
			if !ok || v.AcceptanceRevision != a.PlanRevision || v.CandidateDigest != attempt.Before.Digest {
				return nil, bad
			}
			f.VerificationID = v.ID
			// A failed record can also be the durable result of infrastructure or
			// persistence failure. Account for its provenance without changing the
			// controller's typed outcome or making it eligible for automatic repair.
			if !v.Passed {
				if attempt.Phase != "verification" || used[v.ID] {
					return nil, bad
				}
				started, e1 := time.Parse(time.RFC3339Nano, attempt.StartedAt)
				finished, e2 := time.Parse(time.RFC3339Nano, attempt.FinishedAt)
				created, e3 := time.Parse(time.RFC3339Nano, v.CreatedAt)
				if e1 != nil || e2 != nil || e3 != nil || created.Before(started) || created.After(finished) {
					return nil, bad
				}
				switch attempt.FailureCode {
				case "verification_failed", "infrastructure_failed", "persistence_failed", "interrupted", "outcome_unknown":
				default:
					return nil, bad
				}
				used[v.ID] = true
				f.SourceDigest = codingDigest("verification", v)
				f.Summary += "; " + v.SourceLabel()
			}
			if attempt.FailureCode == "verification_failed" {
				if v.Passed || len(v.Results) == 0 {
					return nil, bad
				}
				var logs strings.Builder
				for _, r := range v.Results {
					logs.WriteString(r.Output)
					logs.WriteByte('\n')
				}
				f.LogExcerpt, f.LogTruncated = boundedFailureText(logs.String(), FailureLogLimit)
			}
		} else if attempt.FailureCode == "verification_failed" {
			return nil, bad
		}
		if attempt.FailureCode == "review_rejected" {
			found := false
			for _, r := range a.Reviews {
				if r.Role == attempt.Role && !r.Passed && r.CandidateDigest == attempt.Before.Digest {
					if found {
						return nil, bad
					}
					found = true
					f.ReviewRole = r.Role
					f.SourceDigest = codingDigest("review", r)
					f.LogExcerpt, f.LogTruncated = boundedFailureText(r.Summary, FailureLogLimit)
				}
			}
			if !found {
				return nil, bad
			}
		}
		b.Failures = append(b.Failures, f)
	}
	if !reflect.DeepEqual(last, a.CandidateGeneration) || len(b.Failures) == 0 {
		return nil, bad
	}
	for _, v := range a.Evidence {
		if !v.Passed && !used[v.ID] {
			return nil, bad
		}
	}
	b.Digest = codingDigest("failure bundle", *b)
	raw, _ := json.Marshal(b)
	if len(raw) > FailureBundleLimit {
		return nil, errors.New("failure bundle exceeds 128 KiB; explicit new task required")
	}
	return b, nil
}

// A recovered selection is a separate committed local fact, never an amendment
// to an interrupted attempt or evidence of successful execution. Only the final
// edit's missing After may be bridged, and only inside its recorded time window.
func ValidateRecoveredSelection(m Mission, a RunArchive) error {
	bad := errors.New("invalid recovered coding selection")
	op := a.RecoveredSelection
	if m.CodingVersion != 1 || op == nil || len(a.CodingAttempts) == 0 || a.CandidateGeneration == nil {
		return bad
	}
	attempt := a.CodingAttempts[len(a.CodingAttempts)-1]
	if attempt.Epoch != a.Epoch || attempt.PlanRevision != a.PlanRevision || attempt.Status != "interrupted" || attempt.FailureCode != "interrupted" || attempt.After != nil || attempt.Before == nil || (attempt.Phase != "worker" && attempt.Phase != "debugger") || attempt.VerificationID != "" {
		return bad
	}
	if op.ID == "" || op.MissionID != m.ID || op.Kind != "apply" || op.Status != "committed" || op.Error != "" || op.ExportReport != "" || op.Epoch != a.Epoch || op.PlanRevision != a.PlanRevision || op.Before != *attempt.Before || op.ExpectedRevision < 1 || op.ExpectedRevision >= a.Revision || op.BaseDigest == "" || op.IntentDigest == "" || op.ApprovalDigest == "" || op.Result == nil || op.Result.Export != nil || op.Result.Generation == nil || op.Result.Generation.ID != op.ID || *op.Result.Generation != *a.CandidateGeneration || *op.Result.Generation == op.Before || !reflect.DeepEqual(op.Result.Changes, a.Changes) {
		return bad
	}
	started, e1 := time.Parse(time.RFC3339Nano, attempt.StartedAt)
	finished, e2 := time.Parse(time.RFC3339Nano, attempt.FinishedAt)
	created, e3 := time.Parse(time.RFC3339Nano, op.CreatedAt)
	committed, e4 := time.Parse(time.RFC3339Nano, op.UpdatedAt)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || created.Before(started) || committed.Before(created) || committed.After(finished) {
		return bad
	}
	approved := false
	for _, approval := range m.Approvals {
		approved = approved || (approval.Kind == "execute" && approval.Digest == op.ApprovalDigest && approval.Revision < op.ExpectedRevision)
	}
	if !approved {
		return bad
	}
	return nil
}

func ValidateReplanContext(m Mission) error {
	if err := ValidateCodingTrace(m); err != nil {
		return err
	}
	b := m.ReplanContext
	if b == nil {
		if m.CodingVersion == 1 {
			for _, a := range m.RunArchives {
				if len(a.CodingAttempts) > 0 {
					return errors.New("missing sealed replan context")
				}
			}
		}
		return nil
	}
	if m.CodingVersion != 1 || b.SchemaVersion != 1 || b.MissionID != m.ID || b.BaseCommit != m.BaseCommit || b.Epoch >= m.Epoch {
		return errors.New("invalid replan context identity or version")
	}
	var archive *RunArchive
	for i := range m.RunArchives {
		a := &m.RunArchives[i]
		if a.Epoch == b.Epoch {
			if archive != nil {
				return errors.New("ambiguous failure archive")
			}
			archive = a
		}
		if a.Epoch > b.Epoch && len(a.CodingAttempts) > 0 {
			return errors.New("stale replan context")
		}
	}
	if archive == nil || CodingArchiveDigest(*archive) != b.ArchiveDigest {
		return errors.New("failure archive changed or missing")
	}
	trace := []CodingAttempt{}
	for _, a := range m.CodingAttempts {
		if a.Epoch == b.Epoch {
			trace = append(trace, a)
		}
	}
	if !reflect.DeepEqual(trace, archive.CodingAttempts) {
		return errors.New("failure trace changed")
	}
	expected, err := BuildFailureBundle(m, *archive)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, b) {
		return errors.New("failure bundle changed")
	}
	return nil
}
func ValidatePlanAdjustments(m Mission, plan Plan) error {
	if err := ValidateReplanContext(m); err != nil {
		return err
	}
	if m.ReplanContext == nil {
		if len(plan.Adjustments) > 0 {
			return errors.New("adjustments require sealed failures")
		}
		return nil
	}
	failures := map[string]CodingFailure{}
	for _, f := range m.ReplanContext.Failures {
		failures[f.ID] = f
	}
	steps := map[string]bool{}
	for _, s := range plan.Steps {
		steps[s.ID] = true
	}
	seen := map[string]bool{}
	for _, a := range plan.Adjustments {
		f, ok := failures[a.FailureID]
		if !ok || seen[a.FailureID] || strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 4000 || !utf8.ValidString(a.Reason) {
			return errors.New("each supplied failure requires one unique adjustment and a bounded reason")
		}
		seen[a.FailureID] = true
		if len(a.StepIDs) == 0 && !a.Unresolved {
			return errors.New("resolved adjustment requires current step IDs")
		}
		ids := map[string]bool{}
		for _, id := range a.StepIDs {
			if !steps[id] || ids[id] {
				return fmt.Errorf("adjustment references unknown or duplicate step: %s", id)
			}
			ids[id] = true
		}
		if f.Code == "outcome_unknown" && !a.Unresolved {
			return errors.New("unknown failure must remain explicitly unresolved")
		}
	}
	if len(seen) != len(failures) {
		return errors.New("every blocking failure requires an adjustment")
	}
	return nil
}
