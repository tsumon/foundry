package sandbox

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"io"
	"reflect"
	"strings"
	"testing"
)

type c4ProcessStep struct {
	args        []string
	out, stderr string
	exit        int
	err         error
}
type c4ScriptProcess struct {
	t     *testing.T
	steps []c4ProcessStep
	calls int
	trace *[]string
}

func (f *c4ScriptProcess) Run(ctx context.Context, c C4Command, out, stderr io.Writer) (int, error) {
	f.t.Helper()
	if f.calls >= len(f.steps) {
		f.t.Fatalf("unexpected process: %+v", c)
	}
	s := f.steps[f.calls]
	f.calls++
	if c.Path != "/synthetic/docker" || !reflect.DeepEqual(c.Env, []string{"DOCKER_CONFIG=/dev/null"}) || !reflect.DeepEqual(c.Args, s.args) || c.CaptureID != "" {
		f.t.Fatalf("unbound command: %+v want %v", c, s.args)
	}
	if f.trace != nil {
		*f.trace = append(*f.trace, "process:"+strings.Join(c.Args[2:4], " "))
	}
	_, _ = io.WriteString(out, s.out)
	_, _ = io.WriteString(stderr, s.stderr)
	return s.exit, s.err
}

type c4CountingTrust struct {
	calls, failAt int
	trace         *[]string
}

func (f *c4CountingTrust) Check() error {
	f.calls++
	if f.trace != nil {
		*f.trace = append(*f.trace, "trust")
	}
	if f.calls == f.failAt {
		return errors.New("private trust detail")
	}
	return nil
}
func c4DriverPlan() C4Plan {
	p := c4DockerProtocolPlan()
	p.profile.DockerPath = "/synthetic/docker"
	p.profile.Endpoint = "unix:///synthetic/docker.sock"
	return p
}
func c4Args(args ...string) []string {
	return append([]string{"--host", "unix:///synthetic/docker.sock"}, args...)
}
func c4StartedFixture() string {
	return strings.Replace(string(c4DockerContainerFixture()), "0001-01-01T00:00:00Z", "2026-10-21T00:00:01Z", 1)
}

const c4PassedWire = `{"class":"passed","exit_code":0,"tests":1,"output":"verified\n","truncated":false}`

func c4DriverSteps(plan C4Plan) []c4ProcessStep {
	id := strings.Repeat("d", 64)
	return []c4ProcessStep{
		{args: c4Args("image", "inspect", plan.profile.ImageID), out: string(c4DockerImageFixture())},
		{args: plan.Create().Args, out: id + "\n"},
		{args: c4Args("container", "inspect", id), out: string(c4DockerContainerFixture())},
		{args: c4Args("container", "start", "--attach", id), out: c4PassedWire, stderr: "benign CLI configuration warning\n"},
		{args: c4Args("container", "inspect", id), out: c4StartedFixture()},
		{args: c4Args("container", "wait", id), out: "0\n"},
		{args: c4Args("container", "inspect", id), out: c4StartedFixture()},
		{args: c4Args("container", "inspect", id), out: c4StartedFixture()},
		{args: c4Args("container", "rm", "--force", id), out: id + "\n"},
	}
}
func c4Stage(t *testing.T, p C4Plan, stage, id string) C4Command {
	t.Helper()
	c, err := p.Command(stage, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Catches missing actual driver, mixed wait/attach exits, ignored config and metadata, or CLI logs.
func TestC4DockerDriverSuccess(t *testing.T) {
	p := c4DriverPlan()
	f := &c4ScriptProcess{t: t, steps: c4DriverSteps(p)}
	trust := &c4CountingTrust{}
	d := newC4DockerDriver(p, f, trust, "")
	if f.calls != 0 || trust.calls != 0 {
		t.Fatal("constructor probed")
	}
	id, err := d.Create(context.Background(), p.Create())
	if err != nil || id != strings.Repeat("d", 64) {
		t.Fatal("create", id, err)
	}
	if err = d.Start(context.Background(), c4Stage(t, p, "start", id)); err != nil {
		t.Fatal("start", err)
	}
	if err = d.Wait(context.Background(), c4Stage(t, p, "wait", id)); err != nil {
		t.Fatal("wait", err)
	}
	v, err := d.Inspect(context.Background(), c4Stage(t, p, "inspect", id))
	if err != nil || v.Verification == nil || v.Verification.Class != "passed" || v.Verification.Tests != 1 {
		t.Fatal("verification", v, err)
	}
	var out strings.Builder
	truncated, err := d.Logs(context.Background(), c4Stage(t, p, "logs", id), &out)
	if err != nil || truncated || out.String() != "verified\nbenign CLI configuration warning\n" {
		t.Fatal("capture", out.String(), truncated, err)
	}
	if err = d.Remove(context.Background(), c4Stage(t, p, "remove", id)); err != nil {
		t.Fatal("remove", err)
	}
	if f.calls != 9 || trust.calls != 9 {
		t.Fatal("missing boundary recheck", f.calls, trust.calls)
	}
}

// Catches a caller widening authority or a failed dispatch being replayable.
func TestC4DockerDriverRejectsTamperingAndReservesCreate(t *testing.T) {
	for _, field := range []string{"path", "args", "env", "capture"} {
		t.Run(field, func(t *testing.T) {
			p := c4DriverPlan()
			f := &c4ScriptProcess{t: t}
			trust := &c4CountingTrust{}
			d := newC4DockerDriver(p, f, trust, "")
			c := p.Create()
			switch field {
			case "path":
				c.Path = "/other"
			case "args":
				c.Args = append(c.Args, "--privileged")
			case "env":
				c.Env = append(c.Env, "SECRET=x")
			case "capture":
				c.CaptureID = "x"
			}
			if _, err := d.Create(context.Background(), c); err == nil || f.calls != 0 || trust.calls != 0 {
				t.Fatal("tamper accepted")
			}
		})
	}
	p := c4DriverPlan()
	steps := c4DriverSteps(p)[:2]
	steps[1].out = "ambiguous\n"
	f := &c4ScriptProcess{t: t, steps: steps}
	d := newC4DockerDriver(p, f, &c4CountingTrust{}, "")
	if id, err := d.Create(context.Background(), p.Create()); err == nil || id != "" {
		t.Fatal("adopted ambiguous ID")
	}
	if _, err := d.Create(context.Background(), p.Create()); err == nil || f.calls != 2 {
		t.Fatal("create replayed")
	}
	if err := d.Remove(context.Background(), c4Stage(t, p, "remove", strings.Repeat("d", 64))); err == nil || f.calls != 2 {
		t.Fatal("ambiguous create cleaned by guessed ID")
	}
}

// Catches disabled public entry points with otherwise valid bound inputs.
// Gate-removal mutation testing uses only the lower-seam regression, never
// these public wrappers (which construct the native process/trust boundaries).
func TestC4DockerDefaultOff(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		h, _, _ := newC4DockerHarness(t)
		if cleanup {
			h, _, _ = c4DockerDisabledRecoveryFixture(t)
		}
		config := C4DockerConfig{Trust: h.trust} // Only Enabled is false.
		var got domain.C4Operation
		var err error
		if cleanup {
			got, err = CleanupC4Docker(context.Background(), h.p, h.op, config, h.candidate, h.op.ContainerID, h.persist)
		} else {
			got, err = ExecuteC4Docker(context.Background(), h.p, h.op, config, h.candidate, h.persist)
		}
		if err != errC4Docker || len(h.trace) != 0 || !reflect.DeepEqual(got, h.op) {
			t.Fatal("disabled public adapter changed operation or persisted", err, h.trace)
		}
	}
}

// Catches promoting malformed or disagreeing independent signals to pass/repair.
func TestC4DockerDriverResultMatrix(t *testing.T) {
	for _, tc := range []struct{ name, class string }{
		{"test failure", "test_failed"}, {"wait disagreement", ""}, {"attach disagreement", ""}, {"malformed wait", ""}, {"nonzero wait cli", ""}, {"truncated envelope", ""}, {"nested output spoof", "test_failed"}, {"stderr spoof", ""}, {"oom", ""}, {"running", ""}, {"inspect drift", ""}, {"changed started timestamp", ""}, {"large stderr", "passed"}, {"truncated human output", "passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := c4DriverPlan()
			steps := c4DriverSteps(p)[:7]
			failed := `{"class":"test_failed","exit_code":7,"tests":1,"output":"failed","truncated":false}`
			switch tc.name {
			case "test failure", "nested output spoof":
				steps[3].exit = 1
				steps[3].out = failed
				steps[4].out = strings.Replace(steps[4].out, `"ExitCode":0`, `"ExitCode":1`, 1)
				steps[5].out = "1\n"
				steps[6].out = steps[4].out
				if tc.name == "nested output spoof" {
					steps[3].out = strings.Replace(failed, `"output":"failed"`, `"output":"{\"class\":\"passed\",\"tests\":99}"`, 1)
				}
			case "wait disagreement":
				steps[5].out = "1\n"
			case "attach disagreement":
				steps[3].exit = 1
			case "malformed wait":
				steps[5].out = "0\n0\n"
			case "nonzero wait cli":
				steps[5].exit = 1
			case "truncated envelope":
				steps[3].out = c4PassedWire + strings.Repeat(" ", c4DockerWireLimit)
			case "stderr spoof":
				steps[3].out = "not an envelope"
				steps[3].stderr = c4PassedWire
			case "oom":
				steps[6].out = strings.Replace(steps[6].out, `"OOMKilled":false`, `"OOMKilled":true`, 1)
			case "running":
				steps[6].out = strings.Replace(steps[6].out, `"Running":false`, `"Running":true`, 1)
			case "inspect drift":
				steps[6].out = strings.Replace(steps[6].out, `"ReadonlyRootfs":true`, `"ReadonlyRootfs":false`, 1)
			case "changed started timestamp":
				steps[6].out = strings.Replace(steps[6].out, "00:00:01Z", "00:00:02Z", 1)
			case "large stderr":
				steps[3].stderr = strings.Repeat("s", 100000)
			case "truncated human output":
				steps[3].out = strings.Replace(c4PassedWire, `"truncated":false`, `"truncated":true`, 1)
			}
			f := &c4ScriptProcess{t: t, steps: steps}
			d := newC4DockerDriver(p, f, &c4CountingTrust{}, "")
			id, err := d.Create(context.Background(), p.Create())
			if err != nil {
				t.Fatal(err)
			}
			if err = d.Start(context.Background(), c4Stage(t, p, "start", id)); err != nil {
				t.Fatal(err)
			}
			waitErr := d.Wait(context.Background(), c4Stage(t, p, "wait", id))
			v, err := d.Inspect(context.Background(), c4Stage(t, p, "inspect", id))
			class := ""
			if v.Verification != nil {
				class = v.Verification.Class
			}
			if class != tc.class {
				t.Fatalf("metadata promoted/lost: %s want %s err=%v wait=%v", class, tc.class, err, waitErr)
			}
			if tc.name == "large stderr" || tc.name == "truncated human output" {
				var out strings.Builder
				tr, err := d.Logs(context.Background(), c4Stage(t, p, "logs", id), &out)
				if err != nil || !tr || out.Len() > 65536 {
					t.Fatal("unbounded logs", tr, err, out.Len())
				}
			}
			before := f.calls
			if d.Wait(context.Background(), c4Stage(t, p, "wait", id)) == nil || d.Start(context.Background(), c4Stage(t, p, "start", id)) == nil || f.calls != before {
				t.Fatal("replayed execution")
			}
		})
	}
}

// Catches starting drifted/already-started objects or inventing a start acknowledgment.
func TestC4DockerDriverStartFailures(t *testing.T) {
	for _, name := range []string{"foreign", "already started", "no acknowledgment", "cancel", "deadline", "transport", "startup127"} {
		t.Run(name, func(t *testing.T) {
			p := c4DriverPlan()
			steps := c4DriverSteps(p)[:5]
			switch name {
			case "foreign":
				steps = steps[:3]
				steps[2].out = strings.Replace(steps[2].out, "owner-1", "foreign", 1)
			case "already started":
				steps = steps[:3]
				steps[2].out = c4StartedFixture()
			case "no acknowledgment":
				steps[4].out = string(c4DockerContainerFixture())
			case "cancel":
				steps = steps[:4]
				steps[3].err = context.Canceled
			case "deadline":
				steps = steps[:4]
				steps[3].err = context.DeadlineExceeded
			case "transport":
				steps = steps[:4]
				steps[3].err = errors.New("private process error")
			case "startup127":
				steps[3].exit = 127
				steps[4].out = strings.Replace(string(c4DockerContainerFixture()), `"ExitCode":0`, `"ExitCode":127`, 1)
			}
			f := &c4ScriptProcess{t: t, steps: steps}
			d := newC4DockerDriver(p, f, &c4CountingTrust{}, "")
			id, err := d.Create(context.Background(), p.Create())
			if err != nil {
				t.Fatal(err)
			}
			err = d.Start(context.Background(), c4Stage(t, p, "start", id))
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("failure acknowledged/leaked", err)
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancel")
			}
			if name == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost deadline")
			}
			var startup C4StartupError
			if name == "startup127" && (!errors.As(err, &startup) || startup.ExitCode != 127) {
				t.Fatal("lost known startup failure", err)
			}
			before := f.calls
			if d.Start(context.Background(), c4Stage(t, p, "start", id)) == nil || d.Wait(context.Background(), c4Stage(t, p, "wait", id)) == nil || f.calls != before {
				t.Fatal("failed stage replayed")
			}
		})
	}
}

// Catches deletion without fresh owned config, unconfirmed removal and recovery widening authority.
func TestC4DockerDriverCleanupOnly(t *testing.T) {
	for _, name := range []string{"success", "foreign", "config drift", "wrong return", "nonzero", "cancel", "replacement"} {
		t.Run(name, func(t *testing.T) {
			p := c4DriverPlan()
			id := strings.Repeat("d", 64)
			steps := c4DriverSteps(p)[7:]
			trust := &c4CountingTrust{}
			switch name {
			case "foreign":
				steps = steps[:1]
				steps[0].out = strings.Replace(steps[0].out, "owner-1", "foreign", 1)
			case "config drift":
				steps = steps[:1]
				steps[0].out = strings.Replace(steps[0].out, `"Privileged":false`, `"Privileged":true`, 1)
			case "wrong return":
				steps[1].out = strings.Repeat("e", 64)
			case "nonzero":
				steps[1].exit = 1
			case "cancel":
				steps[1].err = context.Canceled
			case "replacement":
				steps = steps[:1]
				trust.failAt = 2
			}
			f := &c4ScriptProcess{t: t, steps: steps}
			d := newC4DockerDriver(p, f, trust, id)
			if _, err := d.Create(context.Background(), p.Create()); err == nil {
				t.Fatal("recovery created")
			}
			if d.Start(context.Background(), c4Stage(t, p, "start", id)) == nil || d.Wait(context.Background(), c4Stage(t, p, "wait", id)) == nil || f.calls != 0 {
				t.Fatal("recovery executed")
			}
			err := d.Remove(context.Background(), c4Stage(t, p, "remove", id))
			if (err == nil) != (name == "success") {
				t.Fatal("unconfirmed cleanup", err)
			}
			before := f.calls
			if d.Remove(context.Background(), c4Stage(t, p, "remove", id)) == nil || f.calls != before {
				t.Fatal("remove retried")
			}
		})
	}
}

// Catches image fallback, ID adoption from failed transports, and commands
// continuing after either initial trust failure or identity replacement.
func TestC4DockerDriverCreateFailures(t *testing.T) {
	for _, name := range []string{"image drift", "oversized image", "image error", "create error", "create nonzero", "multiple ids", "short id", "initial trust", "replacement"} {
		t.Run(name, func(t *testing.T) {
			p := c4DriverPlan()
			steps := c4DriverSteps(p)[:2]
			trust := &c4CountingTrust{}
			switch name {
			case "image drift":
				steps = steps[:1]
				steps[0].out = strings.Replace(steps[0].out, `"arm64"`, `"amd64"`, 1)
			case "oversized image":
				steps = steps[:1]
				steps[0].out += strings.Repeat(" ", c4DockerInspectLimit)
			case "image error":
				steps = steps[:1]
				steps[0].err = errors.New("private diagnostic")
			case "create error":
				steps[1].err = errors.New("private diagnostic")
			case "create nonzero":
				steps[1].exit = 1
			case "multiple ids":
				steps[1].out += steps[1].out
			case "short id":
				steps[1].out = "dddd\n"
			case "initial trust":
				steps = nil
				trust.failAt = 1
			case "replacement":
				steps = steps[:1]
				trust.failAt = 2
			}
			f := &c4ScriptProcess{t: t, steps: steps}
			d := newC4DockerDriver(p, f, trust, "")
			id, err := d.Create(context.Background(), p.Create())
			if id != "" || err == nil || strings.Contains(err.Error(), "private") || f.calls != len(steps) {
				t.Fatal("ambiguous create adopted/fallback", id, err, f.calls)
			}
			before := trust.calls
			if _, err = d.Create(context.Background(), p.Create()); err == nil || trust.calls != before || f.calls != len(steps) {
				t.Fatal("failed create retried")
			}
		})
	}
}

// Catches method command validation becoming advisory, including logs capture ID.
func TestC4DockerDriverRejectsEveryStageTampering(t *testing.T) {
	p := c4DriverPlan()
	id := strings.Repeat("d", 64)
	for _, stage := range []string{"start", "wait", "inspect", "logs", "remove"} {
		for _, field := range []string{"path", "args", "env", "capture"} {
			t.Run(stage+"/"+field, func(t *testing.T) {
				f := &c4ScriptProcess{t: t}
				trust := &c4CountingTrust{}
				d := newC4DockerDriver(p, f, trust, "")
				d.id = id
				d.started = true
				c := c4Stage(t, p, stage, id)
				switch field {
				case "path":
					c.Path = "/foreign"
				case "args":
					c.Args = append(c.Args, "--privileged")
				case "env":
					c.Env = append(c.Env, "SECRET=x")
				case "capture":
					c.CaptureID = "foreign"
				}
				var err error
				switch stage {
				case "start":
					err = d.Start(context.Background(), c)
				case "wait":
					err = d.Wait(context.Background(), c)
				case "inspect":
					_, err = d.Inspect(context.Background(), c)
				case "logs":
					_, err = d.Logs(context.Background(), c, io.Discard)
				case "remove":
					err = d.Remove(context.Background(), c)
				}
				if err == nil || f.calls != 0 || trust.calls != 0 {
					t.Fatal("tampered stage reached boundary", stage, field, err)
				}
			})
		}
	}
}

// Catches retaining malformed wire as human output: partially escaped framing
// can otherwise bypass text redaction and leak credential-shaped strings.
func TestC4DockerDriverMalformedWireIsNotHumanOutput(t *testing.T) {
	p := c4DriverPlan()
	steps := c4DriverSteps(p)[:7]
	steps[3].out = `{"class":"passed","exit_code":0,"tests":1,"output":"{\"password\":\"synthetic-secret\"}","truncated":false} trailing`
	f := &c4ScriptProcess{t: t, steps: steps}
	d := newC4DockerDriver(p, f, &c4CountingTrust{}, "")
	id, err := d.Create(context.Background(), p.Create())
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Start(context.Background(), c4Stage(t, p, "start", id)); err != nil {
		t.Fatal(err)
	}
	if err = d.Wait(context.Background(), c4Stage(t, p, "wait", id)); err != nil {
		t.Fatal(err)
	}
	v, err := d.Inspect(context.Background(), c4Stage(t, p, "inspect", id))
	if err != nil || v.Verification != nil {
		t.Fatal("invalid envelope accepted", v, err)
	}
	var out strings.Builder
	_, err = d.Logs(context.Background(), c4Stage(t, p, "logs", id), &out)
	if err != nil || strings.Contains(out.String(), "synthetic-secret") || strings.Contains(out.String(), `\"password`) {
		t.Fatal("malformed metadata exposed", out.String(), err)
	}
}
