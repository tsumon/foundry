// Package c4verify implements the image-owned, fixed Go verifier contract.
// It does not provide host execution or assert that a suitable image exists.
package c4verify

import (
	"bytes"
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const OutputLimit = 65536

type Request struct {
	Path, Dir string
	Args, Env []string
}

// Process is the only effect seam. Run must honor context cancellation, use
// the exact request without inherited environment, and separate stdout/stderr.
type Process interface {
	Run(context.Context, Request, io.Writer, io.Writer) (int, error)
}

type Result struct {
	Class     string `json:"class"`
	ExitCode  int    `json:"exit_code"`
	Tests     int    `json:"tests"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

// Output bounds combined stdout/stderr before allocation and redacts on read.
type Output struct {
	mu        sync.Mutex
	b         bytes.Buffer
	truncated bool
}

func NewOutput() *Output { return &Output{} }
func (b *Output) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := OutputLimit - b.b.Len()
	if len(p) > left {
		p = p[:left]
		b.truncated = true
	}
	_, _ = b.b.Write(p)
	return n, nil
}

var credentials = regexp.MustCompile(`(?i)(["']?(?:token|password|passwd|secret|api[_-]?key|access[_-]?key)["']?\s*[:=]\s*["']?)[^\s,"'\\]+`)
var bearer = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=\-]+`)
var knownTokens = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|gh[pousr]_[A-Za-z0-9_]+)\b`)
var privateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
var urlCredentials = regexp.MustCompile(`(https?://)[^\s/@:]+:[^\s/@]+@`)

func (b *Output) Text() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.ToValidUTF8(b.b.String(), "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
	s = privateKey.ReplaceAllString(s, "[REDACTED PRIVATE KEY]")
	s = credentials.ReplaceAllString(s, "${1}[REDACTED]")
	s = bearer.ReplaceAllString(s, "${1}[REDACTED]")
	s = knownTokens.ReplaceAllString(s, "[REDACTED]")
	s = urlCredentials.ReplaceAllString(s, "${1}[REDACTED]@")
	truncated := b.truncated
	if len(s) > OutputLimit {
		s = s[:OutputLimit]
		truncated = true
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s, truncated
}

type event struct{ Action, Package, Test, Output string }
type events struct {
	line                              []byte
	invalid, failed                   bool
	tests, lines                      int
	running, done, packages, terminal map[string]bool
}

func newEvents() *events {
	return &events{running: map[string]bool{}, done: map[string]bool{}, packages: map[string]bool{}, terminal: map[string]bool{}}
}
func (e *events) Write(p []byte) (int, error) {
	n := len(p)
	for _, c := range p {
		if e.invalid {
			break
		}
		if c == '\n' {
			e.consume()
			e.line = e.line[:0]
		} else if len(e.line) >= OutputLimit {
			e.invalid = true
		} else {
			e.line = append(e.line, c)
		}
	}
	return n, nil
}
func (e *events) consume() {
	e.lines++
	if e.lines > 65536 {
		e.invalid = true
		return
	}
	var v event
	if json.Unmarshal(e.line, &v) != nil || v.Package == "" || len(v.Package) > 1024 || len(v.Test) > 1024 || strings.ContainsRune(v.Package, 0) || strings.ContainsRune(v.Test, 0) {
		e.invalid = true
		return
	}
	if _, exists := e.packages[v.Package]; !exists {
		if len(e.packages) >= 1024 {
			e.invalid = true
			return
		}
		e.packages[v.Package] = false
	}
	if e.terminal[v.Package] {
		e.invalid = true
		return
	}
	key := v.Package + "\x00" + v.Test
	switch v.Action {
	case "run":
		if v.Test == "" || e.running[key] || e.done[key] || len(e.running)+len(e.done) >= 4096 {
			e.invalid = true
			return
		}
		e.running[key] = true
	case "pass":
		if v.Test == "" {
			for key := range e.running {
				if strings.HasPrefix(key, v.Package+"\x00") {
					e.invalid = true
					return
				}
			}
			e.packages[v.Package] = true
			e.terminal[v.Package] = true
			return
		}
		if !e.running[key] {
			e.invalid = true
			return
		}
		delete(e.running, key)
		e.done[key] = true
		e.tests++
	case "fail":
		if v.Test != "" {
			if !e.running[key] {
				e.invalid = true
				return
			}
			e.failed = true
			delete(e.running, key)
			e.done[key] = true
		} else {
			e.packages[v.Package] = false
			e.terminal[v.Package] = true
		}
	case "skip":
		if v.Test != "" {
			if len(e.running)+len(e.done) >= 4096 {
				e.invalid = true
				return
			}
			delete(e.running, key)
			e.done[key] = true
		} else {
			e.packages[v.Package] = true
			e.terminal[v.Package] = true
		}
	case "start", "output", "pause", "cont", "bench", "build-output", "build-fail":
		// Output is data, never recursively parsed as directives or test events.
	default:
		e.invalid = true
	}
}
func (e *events) complete() bool {
	if e.invalid || len(e.line) != 0 || len(e.running) > 0 || e.tests == 0 {
		return false
	}
	for _, passed := range e.packages {
		if !passed {
			return false
		}
	}
	return true
}

func request(args ...string) Request {
	env := domain.FixedC4Policy().Environment
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	r := Request{Path: "/usr/local/go/bin/go", Dir: "/workspace", Args: args, Env: []string{}}
	for _, k := range keys {
		r.Env = append(r.Env, k+"="+env[k])
	}
	return r
}

// Run never invokes a script, downloads dependencies, or trusts test output
// strings as result metadata. The future linux/arm64 image must ship Go 1.27.3.
func Run(ctx context.Context, p Process) Result {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	r := Result{Class: "infrastructure_failed", ExitCode: -1}
	if p == nil || ctx.Err() != nil {
		return r
	}
	version := NewOutput()
	exit, err := p.Run(ctx, request("version"), version, version)
	v, truncated := version.Text()
	if err != nil || exit != 0 || truncated || v != "go version go1.27.3 linux/arm64\n" {
		return r
	}
	out, events := NewOutput(), newEvents()
	exit, err = p.Run(ctx, request("test", "-json", "-count=1", "-timeout=90s", "./..."), io.MultiWriter(out, events), out)
	r.ExitCode = exit
	r.Tests = events.tests
	r.Output, r.Truncated = out.Text()
	if ctx.Err() != nil {
		r.Class = "timeout"
		return r
	}
	if err != nil || exit == 125 || exit == 126 || exit == 127 || events.invalid || len(events.line) != 0 {
		return r
	}
	if exit != 0 && events.failed {
		r.Class = "test_failed"
		return r
	}
	if exit == 0 && !events.failed && events.complete() {
		r.Class = "passed"
	}
	return r
}
