package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

const (
	maxMissionCalls = 16
	maxPlanSteps    = 6
)

func ValidateSpec(spec Spec) error {
	if len(spec.Acceptance) == 0 {
		return errors.New("acceptance commands are required")
	}
	for _, command := range spec.Acceptance {
		if strings.TrimSpace(command) == "" {
			return errors.New("acceptance commands cannot be empty")
		}
	}
	if spec.MaxCalls < 1 || spec.MaxCalls > maxMissionCalls {
		return fmt.Errorf("max_calls must be between 1 and %d", maxMissionCalls)
	}
	return nil
}

// ParseProviderBaseURL applies the provider endpoint policy shared by
// persistence and dispatch. The returned URL is freshly parsed and safe for
// the caller to extend with a protocol path.
func ParseProviderBaseURL(rawURL string) (*url.URL, bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" {
		return nil, false, errors.New("provider base URL must be absolute")
	}
	if u.User != nil {
		return nil, false, errors.New("provider base URL cannot contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, false, errors.New("provider base URL cannot contain query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, false, errors.New("provider base URL requires HTTPS unless it is loopback")
	}
	return u, loopback, nil
}

func ValidatePlan(plan Plan, spec Spec) error {
	if err := ValidateSpec(spec); err != nil {
		return fmt.Errorf("invalid spec: %w", err)
	}
	if len(plan.Steps) == 0 {
		return errors.New("plan must contain at least one step")
	}
	if len(plan.Steps) > maxPlanSteps {
		return fmt.Errorf("plan exceeds %d steps", maxPlanSteps)
	}

	steps := make(map[string]Step, len(plan.Steps))
	for _, step := range plan.Steps {
		if strings.TrimSpace(step.ID) == "" {
			return errors.New("plan step ID is required")
		}
		if _, exists := steps[step.ID]; exists {
			return fmt.Errorf("duplicate plan step %q", step.ID)
		}
		if strings.TrimSpace(step.Title) == "" {
			return fmt.Errorf("plan step %q has no title", step.ID)
		}
		for _, file := range step.Files {
			if err := validatePlanPath(file, spec); err != nil {
				return fmt.Errorf("plan step %q: %w", step.ID, err)
			}
		}
		steps[step.ID] = step
	}

	for _, step := range plan.Steps {
		for _, dependency := range step.DependsOn {
			if dependency == step.ID {
				return fmt.Errorf("plan step %q depends on itself", step.ID)
			}
			if _, exists := steps[dependency]; !exists {
				return fmt.Errorf("plan step %q has unknown dependency %q", step.ID, dependency)
			}
		}
	}

	visiting := make(map[string]bool, len(steps))
	visited := make(map[string]bool, len(steps))
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("plan contains a cycle at step %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range steps[id].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range steps {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// VerificationDigest binds all raw evidence, including unsupported provenance,
// for new reviews. Empty preserves the historical review representation.
func VerificationDigest(evidence []Verification) string {
	versioned := false
	for _, v := range evidence {
		versioned = versioned || v.SourceVersion != 0 || v.SourceKind != ""
	}
	if !versioned {
		return ""
	}
	b, _ := json.Marshal(evidence)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func CanDeliver(mission Mission, currentDigest string) error {
	if mission.State != "awaiting_delivery" {
		return fmt.Errorf("mission state %q cannot deliver", mission.State)
	}
	if currentDigest == "" || mission.CandidateDigest == "" || currentDigest != mission.CandidateDigest {
		return errors.New("candidate digest does not match the current mission")
	}
	if mission.Plan == nil || mission.Plan.Revision < 1 {
		return errors.New("approved plan revision is missing")
	}
	if len(mission.Evidence) == 0 {
		return errors.New("verification evidence is missing")
	}
	latest := mission.Evidence[len(mission.Evidence)-1]
	if latest.ID == "" || !latest.Passed {
		return errors.New("latest verification did not pass")
	}
	if latest.CandidateDigest != currentDigest {
		return errors.New("latest verification is for a different candidate")
	}
	if latest.AcceptanceRevision != mission.Plan.Revision {
		return errors.New("latest verification is for a different acceptance revision")
	}
	if latest.ImageID == "" || latest.ImageID != mission.Spec.ImageID {
		return errors.New("latest verification is for a different image")
	}
	if err := validateCommandResults(latest.Results, mission.Spec.Acceptance); err != nil {
		return err
	}

	latestReviews := make(map[Role]Review, 2)
	for _, review := range mission.Reviews {
		if review.Role == QA || review.Role == Auditor {
			latestReviews[review.Role] = review
		}
	}
	for _, role := range []Role{QA, Auditor} {
		review, ok := latestReviews[role]
		if !ok || !review.Passed || review.CandidateDigest != currentDigest {
			return fmt.Errorf("passing %s review for current candidate is required", role)
		}
		if review.EvidenceDigest != VerificationDigest(mission.Evidence) {
			return fmt.Errorf("%s review evidence binding changed", role)
		}
	}
	return nil
}

func validateCommandResults(results []CommandResult, acceptance []string) error {
	if len(results) == 0 || len(results) != len(acceptance) {
		return errors.New("latest verification has incomplete command results")
	}
	for i, result := range results {
		if result.Command != acceptance[i] {
			return errors.New("latest verification commands do not match acceptance")
		}
		if result.TimedOut || result.ExitCode != 0 {
			return errors.New("latest verification contains a failed command")
		}
	}
	return nil
}

func validatePlanPath(file string, spec Spec) error {
	if file == "" || strings.HasPrefix(file, "/") || strings.Contains(file, "\\") {
		return fmt.Errorf("invalid file path %q", file)
	}
	cleaned := path.Clean(file)
	if cleaned != file || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("invalid file path %q", file)
	}
	for _, protected := range spec.Protected {
		if pathMatches(cleaned, protected) {
			return fmt.Errorf("file %q is protected", file)
		}
	}
	for _, scope := range spec.Scope {
		if pathMatches(cleaned, scope) {
			return nil
		}
	}
	return fmt.Errorf("file %q is outside approved scope", file)
}

func pathMatches(file, rule string) bool {
	rule = strings.TrimPrefix(path.Clean(rule), "./")
	if rule == "." || rule == "" || strings.HasPrefix(rule, "../") {
		return false
	}
	return file == rule || strings.HasPrefix(file, strings.TrimSuffix(rule, "/")+"/")
}
