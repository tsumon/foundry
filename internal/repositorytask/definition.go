// Package repositorytask provides data-driven local proposals, never model or
// process execution. The existing controller alone grants task authority.
package repositorytask

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/workspace"
	"io"
	"strings"
	"unicode/utf8"
)

const Descriptor = "foundry.task.json"
const MaxDescriptorBytes = 128 * 1024

type Edit struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
}
type Step struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
	Edits       []Edit   `json:"edits"`
}
type Definition struct {
	SchemaVersion int      `json:"schema_version"`
	Goal          string   `json:"goal"`
	Summary       string   `json:"summary"`
	Scope         []string `json:"scope"`
	Protected     []string `json:"protected"`
	Steps         []Step   `json:"steps"`
	Repairs       [][]Edit `json:"repairs"`
	Checks        []Edit   `json:"checks"`
}

func Parse(raw []byte) (Definition, error) {
	var d Definition
	bad := errors.New("invalid or unsupported repository task descriptor")
	if len(raw) == 0 || len(raw) > MaxDescriptorBytes || !utf8.Valid(raw) {
		return d, bad
	}
	// encoding/json otherwise accepts duplicate fields and case-insensitive aliases.
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueJSON(dec, 0); err != nil {
		return d, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return d, bad
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, err
	}
	if d.SchemaVersion != 1 || strings.TrimSpace(d.Goal) == "" || len(d.Goal) > 4096 || strings.TrimSpace(d.Summary) == "" || len(d.Summary) > 4096 || len(d.Steps) < 1 || len(d.Steps) > 6 || len(d.Repairs) > 2 || len(d.Checks) < 1 || len(d.Checks) > 30 || len(d.Scope) > 30 || len(d.Protected) > 30 {
		return d, bad
	}
	if err := workspace.ValidateRules(d.Scope); err != nil {
		return d, err
	}
	if err := workspace.ValidateRules(d.Protected); err != nil {
		return d, err
	}
	protected := false
	for _, p := range d.Protected {
		protected = protected || workspace.Matches(Descriptor, p)
	}
	if !protected {
		return d, errors.New("descriptor must be protected")
	}
	editable := map[string]string{}
	for _, s := range d.Steps {
		if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Description) == "" || len(s.Edits) == 0 || len(s.Edits) > 30 {
			return d, bad
		}
		for _, e := range s.Edits {
			if err := d.validateEdit(e); err != nil {
				return d, err
			}
			key := strings.ToLower(e.Path)
			if editable[key] != "" {
				return d, errors.New("ambiguous worker edit")
			}
			editable[key] = e.Path
		}
	}
	if len(editable) > 30 {
		return d, bad
	}
	checks := map[string]bool{}
	for _, e := range d.Checks {
		if err := d.validateEdit(e); err != nil {
			return d, err
		}
		key := strings.ToLower(e.Path)
		if checks[key] || editable[key] != e.Path {
			return d, bad
		}
		checks[key] = true
	}
	if len(checks) != len(editable) {
		return d, errors.New("every editable file needs exactly one assertion")
	}
	for _, round := range d.Repairs {
		if len(round) == 0 || len(round) > 30 {
			return d, bad
		}
		seen := map[string]bool{}
		for _, e := range round {
			if err := d.validateEdit(e); err != nil {
				return d, err
			}
			key := strings.ToLower(e.Path)
			if editable[key] != e.Path || seen[key] {
				return d, bad
			}
			seen[key] = true
		}
	}
	if err := domain.ValidatePlan(d.Plan(), domain.Spec{Scope: d.Scope, Protected: d.Protected, Acceptance: []string{"/opt/foundry/verify-go-v1"}, MaxCalls: 16}); err != nil {
		return d, err
	}
	return d, nil
}

func uniqueJSON(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("descriptor nesting exceeds bound")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("invalid JSON delimiter")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{"schema_version": true, "goal": true, "summary": true, "scope": true, "protected": true, "steps": true, "repairs": true, "checks": true, "id": true, "title": true, "description": true, "depends_on": true, "edits": true, "path": true, "content": true}
	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] || !allowed[s] {
				return errors.New("unknown or duplicate descriptor field")
			}
			seen[s] = true
		}
		if err := uniqueJSON(dec, depth+1); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func (d Definition) validateEdit(e Edit) error {
	if e.Content == nil || !utf8.ValidString(*e.Content) || strings.ContainsRune(*e.Content, 0) {
		return errors.New("edit requires explicit text content")
	}
	return workspace.ValidateEditPath(e.Path, d.Scope, d.Protected)
}

func (d Definition) Plan() domain.Plan {
	p := domain.Plan{Revision: 1, Summary: "SYNTHETIC content proposal: " + d.Summary, Steps: []domain.Step{}}
	for _, s := range d.Steps {
		files := []string{}
		for _, e := range s.Edits {
			files = append(files, e.Path)
		}
		p.Steps = append(p.Steps, domain.Step{ID: s.ID, Title: s.Title, Description: s.Description, DependsOn: s.DependsOn, Files: files})
	}
	return p
}

// ImportSpec adds the protected descriptor to readable context, never editable scope.
func (d Definition) ImportSpec() domain.RepositoryImportSpec {
	scope := append([]string{}, d.Scope...)
	included := false
	for _, p := range scope {
		included = included || workspace.Matches(Descriptor, p)
	}
	if !included {
		scope = append(scope, Descriptor)
	}
	return domain.RepositoryImportSpec{Scope: scope, Protected: append([]string{}, d.Protected...), Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "synthetic:repository-task-no-image"}
}

func (d Definition) workerEdits(files []string) ([]Edit, error) {
	available := map[string]Edit{}
	for _, s := range d.Steps {
		for _, e := range s.Edits {
			available[e.Path] = e
		}
	}
	edits := []Edit{}
	for _, p := range files {
		e, ok := available[p]
		if !ok {
			return nil, fmt.Errorf("unsupported proposal path: %s", p)
		}
		edits = append(edits, e)
	}
	if len(edits) == 0 {
		return nil, errors.New("empty approved step")
	}
	return edits, nil
}
