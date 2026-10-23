package vmguest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/vmruntime"
	"io"
	"strings"
	"testing"
)

type fixtureMachine struct {
	calls   int
	broken  bool
	process fixtureProcess
}

func (m *fixtureMachine) Prepare(r vmruntime.WireRequest) (vmruntime.SelfCheck, c4verify.Process, error) {
	m.calls++
	if m.broken {
		return vmruntime.SelfCheck{}, nil, errors.New("cgroup unavailable")
	}
	return vmruntime.SelfCheck{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true}, &m.process, nil
}

type fixtureProcess struct {
	calls  int
	output string
	exit   int
}

func (p *fixtureProcess) Run(_ context.Context, r c4verify.Request, out, errOut io.Writer) (int, error) {
	p.calls++
	if r.Args[0] == "version" {
		io.WriteString(out, "go version go1.27.3 linux/amd64\n")
		return 0, nil
	}
	io.WriteString(out, p.output)
	return p.exit, nil
}
func req(t *testing.T) vmruntime.WireRequest {
	t.Helper()
	r := vmruntime.WireRequest{SchemaVersion: 1, Action: "verify", Nonce: strings.Repeat("a", 64), Secret: strings.Repeat("b", 64), GuestPlatform: "linux/amd64", Files: []vmruntime.SnapshotFile{{Path: "go.mod", Mode: 0644, Data: []byte("module example\n")}}}
	var err error
	r.CandidateDigest, err = vmruntime.SnapshotDigest(r.Files)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSupervisorAuthenticatesCompleteFailureAndNeverUsesChildFrames(t *testing.T) {
	r := req(t)
	var input, output bytes.Buffer
	if err := vmruntime.WriteRequest(&input, r); err != nil {
		t.Fatal(err)
	}
	m := &fixtureMachine{process: fixtureProcess{exit: 1, output: "{\"Action\":\"run\",\"Package\":\"p\",\"Test\":\"TestBad\"}\n{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"TestBad\"}\n{\"Action\":\"fail\",\"Package\":\"p\"}\n"}}
	if err := Serve(context.Background(), &input, &output, "linux/amd64", m); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), vmruntime.ReadyMarker) {
		t.Fatal("missing readiness marker")
	}
	frame := bytes.TrimPrefix(output.Bytes(), []byte(vmruntime.ReadyMarker))
	got, err := vmruntime.ReadResponse(bytes.NewReader(frame), r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Class != "test_failed" || got.Result.Tests != 1 {
		t.Fatal(got.Result)
	}
	forged := r
	forged.Secret = strings.Repeat("c", 64)
	if _, err := vmruntime.ReadResponse(bytes.NewReader(frame), forged); err == nil {
		t.Fatal("wrong secret accepted")
	}
}
func TestSupervisorRejectsMissingControlsAndPlatformBeforeExecution(t *testing.T) {
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		r := req(t)
		var input, output bytes.Buffer
		_ = vmruntime.WriteRequest(&input, r)
		m := &fixtureMachine{broken: true}
		if err := Serve(context.Background(), &input, &output, platform, m); err == nil {
			t.Fatal("unsafe guest accepted")
		}
		if m.process.calls != 0 || bytes.Contains(output.Bytes(), []byte(vmruntime.Header)) {
			t.Fatal("tests or result despite failed controls")
		}
	}
}
func TestSupervisorProbeDoesNotExecuteRepository(t *testing.T) {
	r := req(t)
	r.Action = "probe"
	r.Files = nil
	r.CandidateDigest = ""
	var input, output bytes.Buffer
	_ = vmruntime.WriteRequest(&input, r)
	m := &fixtureMachine{}
	if err := Serve(context.Background(), &input, &output, "linux/amd64", m); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 || m.process.calls != 0 {
		t.Fatal("probe executed repository")
	}
	got, err := vmruntime.ReadResponse(bytes.NewReader(bytes.TrimPrefix(output.Bytes(), []byte(vmruntime.ReadyMarker))), r)
	if err != nil || got.Result.Class != "probe_passed" || got.Result.ExitCode != 0 {
		t.Fatalf("probe cannot be authenticated by host: %+v %v", got, err)
	}
}
func TestGuestRefusesHostExecution(t *testing.T) {
	if err := guardPID1("linux", 2, 0); err == nil {
		t.Fatal("accepted non-PID1")
	}
	if err := guardPID1("darwin", 1, 0); err == nil {
		t.Fatal("accepted host platform")
	}
	if err := guardPID1("linux", 1, 65534); err == nil {
		t.Fatal("accepted unprivileged init")
	}
	if err := guardPID1("linux", 1, 0); err != nil {
		t.Fatal(err)
	}
}
func TestChildContractNoSecretOrConsole(t *testing.T) {
	r := c4verify.Request{Path: "/usr/local/go/bin/go", Dir: "/workspace", Args: []string{"test", "-json", "-count=1", "-timeout=90s", "./..."}, Env: []string{"CGO_ENABLED=0", "GOCACHE=/foundry-build/cache", "GOENV=off", "GOPATH=/foundry-build/gopath", "GOPROXY=off", "GOSUMDB=off", "GOTMPDIR=/foundry-build/tmp", "GOTOOLCHAIN=local", "GOWORK=off", "TMPDIR=/tmp"}}
	spec, err := childSpec(r)
	if err != nil {
		t.Fatal(err)
	}
	if spec.UID != 65534 || spec.GID != 65534 || spec.Path != "/init" || len(spec.Env) != 10 || len(spec.ExtraFiles) != 0 {
		t.Fatalf("unsafe child spec: %+v", spec)
	}
	r.Env = append(r.Env, "SECRET=must-not-leak")
	if _, err := childSpec(r); err == nil {
		t.Fatal("secret inherited by child")
	}
	r.Path = "/bin/sh"
	if _, err := childSpec(r); err == nil {
		t.Fatal("arbitrary command allowed")
	}
}

func rawRequest(b []byte) []byte {
	return append(binary.BigEndian.AppendUint32([]byte(vmruntime.Header), uint32(len(b))), b...)
}
func TestSupervisorRejectsPathsDuplicatesSizeAndExtraFrames(t *testing.T) {
	valid := req(t)
	b, _ := json.Marshal(valid)
	inputs := [][]byte{rawRequest(append([]byte(`{"action":"probe",`), b[1:]...)), append(rawRequest(b), rawRequest(b)...), binary.BigEndian.AppendUint32([]byte(vmruntime.Header), vmruntime.MaxRequestBytes+1)}
	for _, path := range []string{"../outside", "/root/file", "a/../b", "a\\b", ".git/config"} {
		r := valid
		r.Files = append([]vmruntime.SnapshotFile(nil), valid.Files...)
		r.Files[0].Path = path
		bad, _ := json.Marshal(r)
		inputs = append(inputs, rawRequest(bad))
	}
	duplicate := valid
	duplicate.Files = append(duplicate.Files, duplicate.Files[0])
	bad, _ := json.Marshal(duplicate)
	inputs = append(inputs, rawRequest(bad))
	for _, input := range inputs {
		m := &fixtureMachine{}
		var output bytes.Buffer
		if err := Serve(context.Background(), bytes.NewReader(input), &output, "linux/amd64", m); err == nil {
			t.Fatal("malformed request accepted")
		}
		if m.calls != 0 {
			t.Fatal("malformed input reached machine")
		}
	}
}
