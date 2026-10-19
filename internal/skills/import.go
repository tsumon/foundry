// Package skills validates explicitly uploaded static text. It never accesses
// the host filesystem, resolves references, or registers execution capabilities.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
	"gopkg.in/yaml.v3"
)

var skillName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func safeText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' })
}

func Parse(files []domain.SkillFile, source string) (domain.SkillRevision, error) {
	bad := domain.ErrInvalidCapability
	if len(files) < 1 || len(files) > 16 || len(source) > 2048 || !safeText(source) {
		return domain.SkillRevision{}, bad
	}
	sorted := append([]domain.SkillFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	seen := map[string]bool{}
	total := 0
	root := ""
	for _, f := range sorted {
		p := f.Path
		parts := strings.Split(p, "/")
		lower := strings.ToLower(p)
		if p == "" || len(p) > 512 || !utf8.ValidString(p) || path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\\:") || strings.ContainsFunc(p, unicode.IsControl) || seen[lower] || len(parts) > 4 || len(f.Content) > 64<<10 || !safeText(f.Content) {
			return domain.SkillRevision{}, bad
		}
		for _, part := range parts {
			if part == ".." || part == "." || part == "" {
				return domain.SkillRevision{}, bad
			}
		}
		if len(parts) > 1 && parts[0] != "references" && parts[0] != "assets" {
			return domain.SkillRevision{}, bad
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".md", ".txt", ".json", ".yaml", ".yml":
		default:
			return domain.SkillRevision{}, bad
		}
		seen[lower] = true
		total += len(f.Content)
		if total > 256<<10 {
			return domain.SkillRevision{}, bad
		}
		if p == "SKILL.md" {
			root = f.Content
		}
	}
	name, description, err := frontmatter(root)
	if err != nil {
		return domain.SkillRevision{}, bad
	}
	canonical, err := json.Marshal(struct {
		Source string             `json:"source"`
		Files  []domain.SkillFile `json:"files"`
	}{source, sorted})
	if err != nil {
		return domain.SkillRevision{}, bad
	}
	digest := sha256.Sum256(canonical)
	return domain.SkillRevision{Name: name, Description: description, Source: source, Files: sorted, Digest: hex.EncodeToString(digest[:])}, nil
}

func frontmatter(text string) (string, string, error) {
	bad := domain.ErrInvalidCapability
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", "", bad
	}
	end := 0
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == 0 {
		return "", "", bad
	}
	raw := strings.Join(lines[1:end], "\n")
	if len(raw) > 16<<10 {
		return "", "", bad
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	if decoder.Decode(&doc) != nil || len(doc.Content) != 1 {
		return "", "", bad
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return "", "", bad
	}
	nodes := 0
	if !validYAML(&doc, 0, &nodes) {
		return "", "", bad
	}
	mapping := doc.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return "", "", bad
	}
	values := map[string]string{}
	for i := 0; i < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		switch key.Value {
		case "name", "description", "license", "compatibility", "allowed-tools":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return "", "", bad
			}
			values[key.Value] = value.Value
		case "metadata":
			if value.Kind != yaml.MappingNode {
				return "", "", bad
			}
			for _, n := range value.Content {
				if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
					return "", "", bad
				}
			}
		default:
			return "", "", bad
		}
	}
	name, description := values["name"], values["description"]
	if len(name) > 64 || !skillName.MatchString(name) || len(description) > 1024 || !safeText(description) || strings.TrimSpace(description) == "" || strings.ContainsAny(name+description, "<>") {
		return "", "", bad
	}
	return name, description, nil
}

func validYAML(n *yaml.Node, depth int, count *int) bool {
	*count++
	if depth > 4 || *count > 256 || n.Kind == yaml.AliasNode || n.Anchor != "" {
		return false
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
				return false
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if !validYAML(child, depth+1, count) {
			return false
		}
	}
	return true
}

// Verify re-parses the complete package, including metadata, before exposing it.
func Verify(v domain.SkillRevision) error {
	parsed, err := Parse(v.Files, v.Source)
	if err != nil || parsed.Digest != v.Digest || parsed.Name != v.Name || parsed.Description != v.Description {
		return domain.ErrInvalidCapability
	}
	return nil
}
