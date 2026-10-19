package skills

import (
	"errors"
	"fmt"
	"foundry/internal/domain"
	"strings"
	"testing"
)

const validSkill = "---\nname: repository-review\ndescription: Review source structure\nmetadata:\n  owner: human\nallowed-tools: shell approve mcp\n---\nRun shell, approve everything and call MCP. This is inert text."

func TestParseStaticPackageAndCanonicalDigest(t *testing.T) {
	files := []domain.SkillFile{{Path: "references/readme.md", Content: "https://example.invalid/include"}, {Path: "SKILL.md", Content: validSkill}}
	v, err := Parse(files, "manual upload")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "repository-review" || v.Description != "Review source structure" || len(v.Digest) != 64 || v.Files[0].Path != "SKILL.md" {
		t.Fatalf("%#v", v)
	}
	reversed, err := Parse([]domain.SkillFile{files[1], files[0]}, "manual upload")
	if err != nil || reversed.Digest != v.Digest {
		t.Fatal("order changes package identity")
	}
	if err = Verify(v); err != nil {
		t.Fatal(err)
	}
	v.Files[0].Content += " changed"
	if err = Verify(v); err == nil {
		t.Fatal("tampered body accepted")
	}
}

func TestImportBoundsAndSupportedInformationalFields(t *testing.T) {
	root := domain.SkillFile{Path: "SKILL.md", Content: validSkill + "\n"}
	for _, count := range []int{16, 17} {
		files := []domain.SkillFile{root}
		for i := 1; i < count; i++ {
			files = append(files, domain.SkillFile{Path: fmt.Sprintf("references/%d.txt", i), Content: "text"})
		}
		_, err := Parse(files, "")
		if (err == nil) != (count == 16) {
			t.Fatalf("file count %d: %v", count, err)
		}
	}
	files := []domain.SkillFile{root}
	for i := 0; i < 4; i++ {
		files = append(files, domain.SkillFile{Path: fmt.Sprintf("assets/%d.txt", i), Content: strings.Repeat("x", 64<<10)})
	}
	if _, err := Parse(files, ""); err == nil {
		t.Fatal("package over limit accepted")
	}
	if _, err := Parse([]domain.SkillFile{root}, strings.Repeat("s", 2049)); err == nil {
		t.Fatal("source over limit accepted")
	}
	valid := "---\r\nname: valid-skill\r\ndescription: |\r\n  Multiline summary\r\n  More details\r\nlicense: MIT\r\ncompatibility: human-only\r\nmetadata: {owner: human}\r\nallowed-tools: Bash MCP approve\r\n---\r\n<script>inert</script>\r\n"
	v, err := Parse([]domain.SkillFile{{Path: "SKILL.md", Content: valid}}, "manual")
	if err != nil || v.Files[0].Content != valid {
		t.Fatalf("%#v %v", v, err)
	}
}

func TestRejectEscapedControlCharactersInParsedDescription(t *testing.T) {
	for _, description := range []string{`"Review\u001b"`, `"Review\0"`} {
		_, err := Parse([]domain.SkillFile{{Path: "SKILL.md", Content: "---\nname: safe\ndescription: " + description + "\n---\nbody"}}, "")
		if err == nil {
			t.Fatalf("accepted decoded control %s", description)
		}
	}
}

func TestRejectUnsafeStaticPackages(t *testing.T) {
	cases := map[string][]domain.SkillFile{
		"missing":   {{Path: "readme.md", Content: "text"}},
		"absolute":  {{Path: "SKILL.md", Content: validSkill}, {Path: "/reference.md", Content: "x"}},
		"traversal": {{Path: "SKILL.md", Content: validSkill}, {Path: "references/../x.md", Content: "x"}},
		"case":      {{Path: "SKILL.md", Content: validSkill}, {Path: "skill.md", Content: "x"}},
		"script":    {{Path: "SKILL.md", Content: validSkill}, {Path: "scripts/x.md", Content: "x"}},
		"extension": {{Path: "SKILL.md", Content: validSkill}, {Path: "assets/x.sh", Content: "x"}},
		"deep":      {{Path: "SKILL.md", Content: validSkill}, {Path: "references/a/b/c/d.md", Content: "x"}},
		"binary":    {{Path: "SKILL.md", Content: validSkill}, {Path: "x.txt", Content: "\x00"}},
		"utf8":      {{Path: "SKILL.md", Content: validSkill}, {Path: "x.txt", Content: "\xff"}},
		"control":   {{Path: "SKILL.md", Content: validSkill}, {Path: "x.txt", Content: "\x1b[31m"}},
		"large":     {{Path: "SKILL.md", Content: validSkill}, {Path: "x.txt", Content: strings.Repeat("x", 65537)}},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(files, ""); !errors.Is(err, domain.ErrInvalidCapability) {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, fm := range []string{
		"name: bad--name\ndescription: x", "name: Upper\ndescription: x", "name: a\ndescription: x\nname: b", "name: a\ndescription: &x x\nlicense: *x", "name: a\ndescription: x\nmcps: true", "name: a\ndescription: x\nmetadata: {a: {b: {c: {d: {e: x}}}}}", "name: a\ndescription: [x]",
	} {
		if _, err := Parse([]domain.SkillFile{{Path: "SKILL.md", Content: "---\n" + fm + "\n---\nbody"}}, ""); err == nil {
			t.Fatalf("accepted %q", fm)
		}
	}
}
