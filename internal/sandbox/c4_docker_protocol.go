package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/jsontext"
	"io"
	"strconv"
	"strings"
	"time"
)

const c4DockerWireLimit = 400 * 1024
const c4DockerInspectLimit = 1024 * 1024

type c4DockerInspection struct {
	C4Inspection
	StartedAt time.Time
}

var errC4DockerProtocol = errors.New("invalid C4 Docker protocol")

// All errors are static: untrusted inspect fields and verifier output must not
// leak through diagnostics. These pure parsers neither probe nor invoke Docker.
// A token walk checks duplicate, nesting and collection limits as it builds a
// bounded tree. Unknown Docker metadata is subject to the same limits.
func c4DockerJSON(raw []byte, limit int) (any, error) {
	if len(raw) > limit || !jsontext.Lossless(raw) {
		return nil, errC4DockerProtocol
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		nodes++
		if depth > 32 || nodes > 8192 {
			return nil, errC4DockerProtocol
		}
		t, err := d.Token()
		if err != nil {
			return nil, errC4DockerProtocol
		}
		switch t {
		case json.Delim('{'):
			m := c4DockerObject{}
			seen := map[string]bool{}
			for d.More() {
				if len(m) >= 256 {
					return nil, errC4DockerProtocol
				}
				key, err := d.Token()
				k, ok := key.(string)
				if err != nil || !ok || seen[strings.ToLower(k)] {
					return nil, errC4DockerProtocol
				}
				seen[strings.ToLower(k)] = true
				v, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				m[k] = v
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return nil, errC4DockerProtocol
			}
			return m, nil
		case json.Delim('['):
			a := []any{}
			for d.More() {
				if len(a) >= 1024 {
					return nil, errC4DockerProtocol
				}
				v, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return nil, errC4DockerProtocol
			}
			return a, nil
		default:
			return t, nil
		}
	}
	v, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errC4DockerProtocol
	}
	return v, nil
}

type c4DockerObject map[string]any

// Required fields are checked by exact lookup. Optional security fields also
// need an explicit spelling check so an alias cannot pass as unrelated data.
func (m c4DockerObject) exactCase(keys ...string) bool {
	for actual := range m {
		for _, key := range keys {
			if strings.EqualFold(actual, key) && actual != key {
				return false
			}
		}
	}
	return true
}

func (m c4DockerObject) object(key string) c4DockerObject {
	v, _ := m[key].(c4DockerObject)
	return v
}
func (m c4DockerObject) str(key string) (string, bool) { v, ok := m[key].(string); return v, ok }
func (m c4DockerObject) integer(key string) (int64, bool) {
	v, ok := m[key].(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	return n, err == nil
}
func (m c4DockerObject) equal(key string, want any) bool {
	v, exists := m[key]
	if !exists {
		return false
	}
	switch w := want.(type) {
	case int:
		n, ok := m.integer(key)
		return ok && n == int64(w)
	case string:
		s, ok := v.(string)
		return ok && s == w
	case bool:
		b, ok := v.(bool)
		return ok && b == w
	default:
		return false
	}
}
func (m c4DockerObject) list(key string, want ...string) bool {
	v, exists := m[key]
	if !exists {
		return false
	}
	if v == nil {
		return len(want) == 0
	}
	a, ok := v.([]any)
	if !ok || len(a) != len(want) {
		return false
	}
	for i, w := range want {
		if s, ok := a[i].(string); !ok || s != w {
			return false
		}
	}
	return true
}
func (m c4DockerObject) emptyObject(key string) bool {
	v, exists := m[key]
	if !exists {
		return false
	}
	if v == nil {
		return true
	}
	o, ok := v.(c4DockerObject)
	return ok && len(o) == 0
}

func c4DockerInspectObject(raw []byte) (c4DockerObject, error) {
	v, err := c4DockerJSON(raw, c4DockerInspectLimit)
	if err != nil {
		return nil, err
	}
	a, ok := v.([]any)
	if !ok || len(a) != 1 {
		return nil, errC4DockerProtocol
	}
	m, ok := a[0].(c4DockerObject)
	if !ok {
		return nil, errC4DockerProtocol
	}
	return m, nil
}

// PATH and GOLANG_VERSION are the only benign image defaults. Container env
// must additionally contain every fixed policy value; no compiler/loader
// overrides or duplicate assignments are allowed. PATH may be absent because
// both verifier and compiler commands are absolute.
func c4DockerEnvironment(m c4DockerObject, required bool) bool {
	v, exists := m["Env"]
	if !exists {
		return false
	}
	a, ok := v.([]any)
	if !ok && v != nil {
		return false
	}
	fixed := domain.FixedC4Policy().Environment
	allowed := domain.FixedC4Policy().Environment
	allowed["PATH"] = "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	allowed["GOLANG_VERSION"] = "1.27.3"
	seen := map[string]bool{}
	for _, item := range a {
		s, ok := item.(string)
		if !ok {
			return false
		}
		k, value, ok := strings.Cut(s, "=")
		want, known := allowed[k]
		if !ok || !known || value != want || seen[k] {
			return false
		}
		seen[k] = true
	}
	if required {
		for k := range fixed {
			if !seen[k] {
				return false
			}
		}
	}
	return true
}

func validateC4DockerImage(raw []byte, p domain.C4Profile) error {
	m, err := c4DockerInspectObject(raw)
	if err != nil {
		return err
	}
	c := m.object("Config")
	if !strings.HasPrefix(p.ImageID, "sha256:") || !c4ContainerID.MatchString(strings.TrimPrefix(p.ImageID, "sha256:")) || p.Platform != "linux/arm64" ||
		!m.equal("Id", p.ImageID) || !m.equal("Os", "linux") || !m.equal("Architecture", "arm64") || !c.emptyObject("Volumes") || !c.list("Cmd") || !c4DockerEnvironment(c, false) {
		return errC4DockerProtocol
	}
	return nil
}

func decodeC4Verifier(raw []byte, containerExit int) (*c4verify.Result, error) {
	v, err := c4DockerJSON(raw, c4DockerWireLimit)
	if err != nil {
		return nil, err
	}
	m, ok := v.(c4DockerObject)
	if !ok || len(m) != 5 {
		return nil, errC4DockerProtocol
	}
	class, okClass := m.str("class")
	exit, okExit := m.integer("exit_code")
	tests, okTests := m.integer("tests")
	output, okOutput := m.str("output")
	truncated, okTruncated := m["truncated"].(bool)
	if !okClass || !okExit || !okTests || !okOutput || !okTruncated || tests < 0 || tests > 4096 || len(output) > c4verify.OutputLimit || exit < -1 || exit > 255 {
		return nil, errC4DockerProtocol
	}
	switch class {
	case "passed":
		if exit != 0 || containerExit != 0 || tests == 0 {
			return nil, errC4DockerProtocol
		}
	case "test_failed":
		if exit <= 0 || exit == 125 || exit == 126 || exit == 127 || containerExit != 1 {
			return nil, errC4DockerProtocol
		}
	case "infrastructure_failed", "timeout":
		if containerExit != 2 {
			return nil, errC4DockerProtocol
		}
	default:
		return nil, errC4DockerProtocol
	}
	b := c4verify.NewOutput()
	_, _ = b.Write([]byte(output))
	output, clipped := b.Text()
	// The envelope exit is the inner Go exit. The lifecycle classifier compares
	// Verification.ExitCode to the verifier container/process exit, not Go's or
	// the wait CLI's exit; validate the mapping before normalizing it here.
	return &c4verify.Result{Class: class, ExitCode: containerExit, Tests: int(tests), Output: output, Truncated: truncated || clipped}, nil
}
