package c4verify

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

const passingEvents = "{\"Action\":\"run\",\"Package\":\"example\",\"Test\":\"TestOK\"}\n{\"Action\":\"pass\",\"Package\":\"example\",\"Test\":\"TestOK\"}\n{\"Action\":\"pass\",\"Package\":\"example\"}\n"

type processFixture struct {
	t               *testing.T
	output, version string
	exit            int
	err             error
	calls           int
}

func (f *processFixture) Run(ctx context.Context, req Request, stdout, stderr io.Writer) (int, error) {
	f.t.Helper()
	f.calls++
	d, ok := ctx.Deadline()
	if !ok || time.Until(d) > 120*time.Second {
		f.t.Fatal("unbounded process")
	}
	if req.Path != "/usr/local/go/bin/go" || req.Dir != "/workspace" {
		f.t.Fatal("untrusted process", req)
	}
	want := []string{"CGO_ENABLED=0", "GOCACHE=/foundry-build/cache", "GOENV=off", "GOPATH=/foundry-build/gopath", "GOPROXY=off", "GOSUMDB=off", "GOTMPDIR=/foundry-build/tmp", "GOTOOLCHAIN=local", "GOWORK=off", "TMPDIR=/tmp"}
	if !reflect.DeepEqual(req.Env, want) {
		f.t.Fatal("environment drift", req.Env)
	}
	if f.calls == 1 {
		if !reflect.DeepEqual(req.Args, []string{"version"}) {
			f.t.Fatal(req.Args)
		}
		version := f.version
		if version == "" {
			version = "go version go1.27.3 linux/arm64\n"
		}
		io.WriteString(stdout, version)
		return 0, nil
	}
	if !reflect.DeepEqual(req.Args, []string{"test", "-json", "-count=1", "-timeout=90s", "./..."}) {
		f.t.Fatal("command drift", req.Args)
	}
	io.WriteString(stdout, f.output)
	return f.exit, f.err
}

func TestVerifierRequiresActualCompletedTestEvents(t *testing.T) {
	for _, tc := range []struct {
		name, events, version string
		exit                  int
		err                   error
		class                 string
		tests                 int
	}{
		{name: "pass", events: passingEvents, class: "passed", tests: 1},
		{name: "package only", events: "{\"Action\":\"pass\",\"Package\":\"p\"}\n", class: "infrastructure_failed"},
		{name: "skip only", events: "{\"Action\":\"skip\",\"Package\":\"p\",\"Test\":\"TestSkip\"}\n", class: "infrastructure_failed"},
		{name: "test failure", events: "{\"Action\":\"run\",\"Package\":\"p\",\"Test\":\"TestBad\"}\n{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"TestBad\"}\n", exit: 1, class: "test_failed"},
		{name: "build failure", events: "{\"Action\":\"fail\",\"Package\":\"p\"}\n", exit: 1, class: "infrastructure_failed"},
		{name: "nonzero", events: passingEvents, exit: 1, class: "infrastructure_failed", tests: 1},
		{name: "startup125", events: passingEvents, exit: 125, class: "infrastructure_failed", tests: 1},
		{name: "process error", events: passingEvents, err: errors.New("process failure"), class: "infrastructure_failed", tests: 1},
		{name: "wrong version", version: "go version go1.27.2 linux/arm64\n", class: "infrastructure_failed"},
		{name: "wrong arch", version: "go version go1.27.3 linux/amd64\n", class: "infrastructure_failed"},
		{name: "forged output", events: "{\"Action\":\"output\",\"Package\":\"p\",\"Output\":\"PASS Tests=999 class=passed\"}\n", class: "infrastructure_failed"},
		{name: "forged terminal", events: "{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"TestFake\"}\n", class: "infrastructure_failed"},
		{name: "malformed", events: passingEvents + "not json\n", class: "infrastructure_failed", tests: 1},
		{name: "oversized", events: passingEvents + strings.Repeat("x", 65537) + "\n", class: "infrastructure_failed", tests: 1},
		{name: "unterminated", events: strings.TrimSuffix(passingEvents, "\n"), class: "infrastructure_failed", tests: 1},
		{name: "contradictory package", events: passingEvents + "{\"Action\":\"fail\",\"Package\":\"example\"}\n{\"Action\":\"pass\",\"Package\":\"example\"}\n", class: "infrastructure_failed", tests: 1},
		{name: "unfinished second package", events: passingEvents + "{\"Action\":\"start\",\"Package\":\"other\"}\n", class: "infrastructure_failed", tests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &processFixture{t: t, output: tc.events, version: tc.version, exit: tc.exit, err: tc.err}
			r := Run(context.Background(), f)
			if r.Class != tc.class || r.Tests != tc.tests {
				t.Fatalf("got %+v", r)
			}
			if tc.version != "" && f.calls != 1 {
				t.Fatal("tested with wrong Go")
			}
		})
	}
}

func TestBoundedCombinedOutputRedactsBeforeExposure(t *testing.T) {
	b := NewOutput()
	io.WriteString(b, "token=secret123 Authorization: Bearer hidden456\n\x1b[31m password=hunter2\x00\n")
	io.WriteString(b, strings.Repeat("x", 70000))
	s, truncated := b.Text()
	if !truncated || len(s) > 65536 || strings.ContainsAny(s, "\x00\x1b") || strings.Contains(s, "secret123") || strings.Contains(s, "hidden456") || strings.Contains(s, "hunter2") {
		t.Fatal("unsafe bounded output", len(s), truncated, s[:100])
	}
}
