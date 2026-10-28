package dependencyadmission

import (
	"errors"
	"path"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
)

var errSpec = errors.New("unsupported_dependency_spec")
var numeric = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){0,2}`)
var canonicalVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var namePart = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func stableVersion(s string) bool {
	if len(s) > 256 || !canonicalVersion.MatchString(s) {
		return false
	}
	_, e := semver.StrictNewVersion(s)
	return e == nil
}
func satisfies(spec, version string) (bool, error) {
	if len(spec) == 0 || len(spec) > 256 {
		return false, errSpec
	}
	for _, c := range []byte(spec) {
		if c > 126 || c < 32 && c != '\t' && c != '\r' && c != '\n' {
			return false, errSpec
		}
	}
	branches := strings.Split(spec, "||")
	if len(branches) > 4 {
		return false, errSpec
	}
	for _, branch := range branches {
		remaining := strings.TrimSpace(branch)
		if remaining == "" {
			return false, errSpec
		}
		count := 0
		for remaining != "" {
			count++
			if count > 8 {
				return false, errSpec
			}
			op := ""
			for _, candidate := range []string{">=", "<=", ">", "<", "^", "~", "="} {
				if strings.HasPrefix(remaining, candidate) {
					op = candidate
					remaining = strings.TrimLeft(remaining[len(op):], " \t\r\n")
					break
				}
			}
			endpoint := numeric.FindString(remaining)
			if endpoint == "" {
				return false, errSpec
			}
			if (op == "" || op == "=") && !canonicalVersion.MatchString(endpoint) {
				return false, errSpec
			}
			remaining = remaining[len(endpoint):]
			if remaining != "" && !strings.ContainsRune(" \t\r\n", rune(remaining[0])) {
				return false, errSpec
			}
			remaining = strings.TrimLeft(remaining, " \t\r\n")
		}
	}
	if !stableVersion(version) {
		return false, errInvalid
	}
	v, e := semver.StrictNewVersion(version)
	if e != nil {
		return false, errInvalid
	}
	c, e := semver.NewConstraint(spec)
	if e != nil {
		return false, errSpec
	}
	return c.Check(v), nil
}
func validName(n string) bool {
	if len(n) == 0 || len(n) > 214 {
		return false
	}
	if strings.HasPrefix(n, "@") {
		p := strings.Split(n[1:], "/")
		return len(p) == 2 && namePart.MatchString(p[0]) && namePart.MatchString(p[1])
	}
	return n != "node_modules" && namePart.MatchString(n)
}
func locationName(loc string) (string, error) {
	if len(loc) > 1024 || !strings.HasPrefix(loc, "node_modules/") {
		return "", errInvalid
	}
	parts := strings.Split(loc, "/")
	last := ""
	for i := 0; i < len(parts); {
		if parts[i] != "node_modules" || i+1 >= len(parts) {
			return "", errInvalid
		}
		i++
		last = parts[i]
		i++
		if strings.HasPrefix(last, "@") {
			if i >= len(parts) {
				return "", errInvalid
			}
			last += "/" + parts[i]
			i++
		}
		if !validName(last) {
			return "", errInvalid
		}
	}
	return last, nil
}

// Node searches ancestors by location before testing the selected version. In
// particular, an incompatible nearer package must never be skipped.
func nearest(from, name string, packages map[string]*nodePackage) string {
	for current := from; ; current = path.Dir(current) {
		if path.Base(current) != "node_modules" {
			candidate := path.Join(current, "node_modules", name)
			if current == "" || current == "." {
				candidate = "node_modules/" + name
			}
			if _, ok := packages[candidate]; ok {
				return candidate
			}
		}
		if current == "" || current == "." {
			break
		}
	}
	return ""
}
func nodeEdges(root *nodePackage, packages map[string]*nodePackage) ([]DependencyEdge, error) {
	edges := []DependencyEdge{}
	byFrom := map[string][]string{}
	add := func(from string, deps map[string]string) error {
		if len(edges)+len(deps) > maxEdges {
			return errInvalid
		}
		for name, spec := range deps {
			// Validate syntax even when the selected location is missing.
			if _, e := satisfies(spec, "0.0.0"); e != nil {
				return e
			}
			to := nearest(from, name, packages)
			if to == "" {
				return errors.New("missing_dependency")
			}
			ok, e := satisfies(spec, packages[to].version)
			if e != nil {
				return e
			}
			if !ok {
				return errors.New("dependency_version_mismatch")
			}
			edges = append(edges, DependencyEdge{From: from, Name: name, To: to, Requirement: spec, Status: "active"})
			byFrom[from] = append(byFrom[from], to)
		}
		return nil
	}
	if e := add("", root.deps); e != nil {
		return nil, e
	}
	if e := add("", root.devDeps); e != nil {
		return nil, e
	}
	for loc, p := range packages {
		if e := add(loc, p.deps); e != nil {
			return nil, e
		}
	}
	seen := map[string]bool{"": true}
	queue := []string{""}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range byFrom[from] {
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
			}
		}
	}
	if len(seen) != len(packages)+1 {
		return nil, errors.New("extraneous_package")
	}
	return edges, nil
}
