package dependencyadmission

import (
	"regexp"
	"strconv"
	"strings"
)

// pythonVersion accepts only the policy's literal numeric release subset.
func pythonVersion(s string) ([]int, error) {
	if len(s) == 0 || len(s) > 64 {
		return nil, errInvalid
	}
	parts := strings.Split(s, ".")
	if len(parts) > 4 {
		return nil, errInvalid
	}
	out := make([]int, len(parts))
	for i, p := range parts {
		if len(p) == 0 || len(p) > 9 || len(p) > 1 && p[0] == '0' {
			return nil, errInvalid
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return nil, errInvalid
			}
		}
		n, e := strconv.Atoi(p)
		if e != nil {
			return nil, errInvalid
		}
		out[i] = n
	}
	return out, nil
}
func pythonCompare(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}
func pythonSatisfies(spec, version string) (bool, error) {
	v, e := pythonVersion(version)
	if e != nil || len(spec) == 0 || len(spec) > 256 {
		return false, errInvalid
	}
	parts := strings.Split(spec, ",")
	if len(parts) > 8 {
		return false, errInvalid
	}
	match := true
	for _, part := range parts {
		part = strings.TrimSpace(part)
		op := ""
		for _, candidate := range []string{"==", "!=", "<=", ">=", "~=", "<", ">"} {
			if strings.HasPrefix(part, candidate) {
				op = candidate
				break
			}
		}
		if op == "" {
			return false, errInvalid
		}
		raw := strings.TrimSpace(strings.TrimPrefix(part, op))
		wildcard := strings.HasSuffix(raw, ".*")
		if wildcard {
			raw = strings.TrimSuffix(raw, ".*")
			if op != "==" && op != "!=" {
				return false, errInvalid
			}
		}
		release, e := pythonVersion(raw)
		if e != nil || op == "~=" && len(release) < 2 {
			return false, errInvalid
		}
		cmp := pythonCompare(v, release)
		ok := false
		if wildcard {
			ok = len(v) >= len(release)
			if ok {
				for i := range release {
					if v[i] != release[i] {
						ok = false
						break
					}
				}
			}
			if op == "!=" {
				ok = !ok
			}
		} else {
			switch op {
			case "==":
				ok = cmp == 0
			case "!=":
				ok = cmp != 0
			case "<":
				ok = cmp < 0
			case "<=":
				ok = cmp <= 0
			case ">":
				ok = cmp > 0
			case ">=":
				ok = cmp >= 0
			case "~=":
				upper := append([]int(nil), release[:len(release)-1]...)
				upper[len(upper)-1]++
				ok = cmp >= 0 && pythonCompare(v, upper) < 0
			}
		}
		match = match && ok
	}
	return match, nil
}

var pythonRequirementPattern = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)(.*)$`)
var pythonExtraMarker = regexp.MustCompile(`^extra\s*==\s*(['"])([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)['"]$`)

func normalizePythonName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, c := range s {
		if c == '-' || c == '_' || c == '.' {
			if !dash {
				b.WriteByte('-')
			}
			dash = true
		} else {
			b.WriteRune(c)
			dash = false
		}
	}
	return b.String()
}
func pythonRequirement(s string, extras []string) (string, string, string, error) {
	if len(s) == 0 || len(s) > 1024 {
		return "", "", "", errInvalid
	}
	for _, c := range s {
		if c < 32 || c > 126 {
			return "", "", "", errInvalid
		}
	}
	body, marker, hasMarker := strings.Cut(s, ";")
	status := "active"
	if hasMarker {
		marker = strings.TrimSpace(marker)
		if strings.HasPrefix(marker, "(") && strings.HasSuffix(marker, ")") {
			marker = strings.TrimSpace(marker[1 : len(marker)-1])
		}
		m := pythonExtraMarker.FindStringSubmatch(marker)
		if m == nil || marker[len(marker)-1] != m[1][0] {
			return "", "", "", errInvalid
		}
		extra := normalizePythonName(m[2])
		found := false
		for _, x := range extras {
			if x == extra {
				found = true
				break
			}
		}
		if !found {
			return "", "", "", errInvalid
		}
		status = "inactive_extra"
	}
	m := pythonRequirementPattern.FindStringSubmatch(strings.TrimSpace(body))
	if m == nil {
		return "", "", "", errInvalid
	}
	name := normalizePythonName(m[1])
	spec := strings.TrimSpace(m[2])
	if strings.HasPrefix(spec, "(") && strings.HasSuffix(spec, ")") {
		spec = strings.TrimSpace(spec[1 : len(spec)-1])
		if spec == "" {
			return "", "", "", errInvalid
		}
	}
	if spec != "" {
		if status == "active" {
			if _, e := pythonSatisfies(spec, "0"); e != nil {
				return "", "", "", e
			}
		} else {
			for _, c := range spec {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(" .,*<>=!~+-_", c)) {
					return "", "", "", errInvalid
				}
			}
		}
	}
	return name, spec, status, nil
}
