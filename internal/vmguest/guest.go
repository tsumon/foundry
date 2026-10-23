// Package vmguest is the guest-only PID1 supervisor. It has no host execution
// fallback. Tests inject the privileged machine boundary, not result authority.
package vmguest

import (
	"bufio"
	"context"
	"errors"
	"foundry/internal/c4verify"
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"io"
	"os"
	"reflect"
	"strings"
)

type machine interface {
	Prepare(vmruntime.WireRequest) (vmruntime.SelfCheck, c4verify.Process, error)
}

// Serve owns one request and is the only writer of authenticated metadata.
// Child output goes exclusively through c4verify's bounded event parser.
func Serve(ctx context.Context, input io.Reader, output io.Writer, platform string, m machine) error {
	if _, err := io.WriteString(output, vmruntime.ReadyMarker); err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	request, err := vmruntime.ReadRequest(reader)
	if err != nil {
		return errors.New("invalid guest request")
	}
	if reader.Buffered() != 0 {
		return errors.New("multiple guest requests")
	}
	if request.GuestPlatform != platform {
		return errors.New("guest platform mismatch")
	}
	checks, p, err := m.Prepare(request)
	if err != nil {
		return errors.New("guest security controls unavailable")
	}
	if !checks.ReadOnlyWorkspace || !checks.ReadOnlyToolchain || !checks.NoNetwork || !checks.PrivateControl || !checks.Unprivileged || !checks.NoNewPrivileges || !checks.CgroupLimits || !checks.TmpfsLimits {
		return errors.New("guest self-check incomplete")
	}
	result := c4verify.Result{Class: "probe_passed", ExitCode: 0}
	if request.Action == "verify" {
		result = c4verify.RunForPlatform(ctx, p, platform)
	}
	return vmruntime.WriteResponse(output, request, checks, result)
}

func guardPID1(goos string, pid, uid int) error {
	if goos != "linux" || pid != 1 || uid != 0 {
		return errors.New("guest requires Linux root PID1")
	}
	return nil
}

type processSpec struct {
	Path, Dir  string
	Args, Env  []string
	UID, GID   uint32
	ExtraFiles []*os.File
}

func childSpec(r c4verify.Request) (processSpec, error) {
	if r.Path != "/usr/local/go/bin/go" || r.Dir != "/workspace" || (!reflect.DeepEqual(r.Args, []string{"version"}) && !reflect.DeepEqual(r.Args, []string{"test", "-json", "-count=1", "-timeout=90s", "./..."})) {
		return processSpec{}, errors.New("unapproved guest command")
	}
	expected := domain.FixedC4Policy().Environment
	if len(r.Env) != len(expected) {
		return processSpec{}, errors.New("unexpected child environment")
	}
	seen := map[string]bool{}
	for _, v := range r.Env {
		k, value, ok := strings.Cut(v, "=")
		want, exists := expected[k]
		if !ok || !exists || seen[k] || want != value {
			return processSpec{}, errors.New("unexpected child environment")
		}
		seen[k] = true
	}
	return processSpec{Path: "/init", Dir: r.Dir, Args: append([]string{"--child"}, r.Args...), Env: append([]string(nil), r.Env...), UID: 65534, GID: 65534}, nil
}
