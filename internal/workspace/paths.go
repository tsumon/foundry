package workspace

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

func safePath(p string) error {
	if p == "" || p == "." || path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, "\\") || strings.HasPrefix(p, "../") {
		return errors.New("invalid relative path")
	}
	for _, r := range p {
		if unicode.IsControl(r) || r > 127 {
			return errors.New("unsupported path character")
		}
	}
	for _, part := range strings.Split(strings.ToLower(p), "/") {
		switch part {
		case ".npmrc", ".netrc", ".pypirc", ".aws", ".ssh", ".docker", ".kube", "auth.json":
			return errors.New("credential configuration refused")
		}
		if part == ".git" || part == ".hg" || part == ".svn" || strings.HasPrefix(part, ".env") || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.Contains(part, "token") || part == "id_rsa" || part == "id_ed25519" || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
			return errors.New("sensitive path refused")
		}
	}
	return nil
}
func Matches(p, rule string) bool {
	r := strings.TrimSuffix(rule, "/")
	return p == r || strings.HasPrefix(p, r+"/")
}
func Protected(p string) bool {
	p = strings.ToLower(p)
	base := path.Base(p)
	for _, part := range strings.Split(p, "/") {
		if part == "test" || part == "tests" || part == "__tests__" || part == "spec" || part == "specs" || part == ".github" || part == ".husky" {
			return true
		}
	}
	if strings.Contains(base, "test") || strings.Contains(base, "spec.") || strings.Contains(base, "config") || strings.Contains(base, "lock") {
		return true
	}
	switch base {
	case "package.json", "go.mod", "go.sum", "makefile", "gnumakefile", "dockerfile", "gemfile", "rakefile", "cargo.toml", "pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "pom.xml", "build.gradle", "build.gradle.kts", "gradlew", "tsconfig.json", ".npmrc", ".yarnrc", ".gitignore", ".gitattributes":
		return true
	}
	return strings.HasPrefix(base, ".") || strings.HasPrefix(base, "docker-compose") || strings.HasPrefix(base, "compose.")
}
func ValidateEditPath(p string, scope, protected []string) error {
	if err := safePath(p); err != nil {
		return err
	}
	if Protected(p) {
		return fmt.Errorf("protected acceptance input: %s", p)
	}
	for _, r := range protected {
		if Matches(p, r) {
			return fmt.Errorf("protected path: %s", p)
		}
	}
	for _, r := range scope {
		if safePath(strings.TrimSuffix(r, "/")) == nil && Matches(p, r) {
			return nil
		}
	}
	return fmt.Errorf("outside approved scope: %s", p)
}
func ValidateRules(rules []string) error {
	if len(rules) == 0 {
		return errors.New("explicit scope required")
	}
	for _, r := range rules {
		if e := safePath(strings.TrimSuffix(r, "/")); e != nil {
			return e
		}
	}
	return nil
}
