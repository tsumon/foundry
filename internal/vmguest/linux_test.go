//go:build linux

package vmguest

import (
	"os"
	"testing"
)

func TestLinuxMainRefusesNonPID1BeforeMounts(t *testing.T) {
	if os.Getpid() == 1 {
		t.Skip("unit tests must not run as guest PID1")
	}
	if err := Main(); err == nil {
		t.Fatal("host process accepted as init")
	}
}

func TestUnprivilegedLaunchClosesDescriptorsAndEntersCgroupBeforeExec(t *testing.T) {
	c := restrictedCommand("/init", []string{"--check"}, nil, 42)
	if c.SysProcAttr.Credential.Uid != 65534 || c.SysProcAttr.Credential.Gid != 65534 || c.SysProcAttr.Credential.NoSetGroups || len(c.SysProcAttr.Credential.Groups) != 0 || !c.SysProcAttr.UseCgroupFD || c.SysProcAttr.CgroupFD != 42 || len(c.ExtraFiles) != 0 || c.Stdin != nil {
		t.Fatalf("unsafe launch %+v", c)
	}
}
