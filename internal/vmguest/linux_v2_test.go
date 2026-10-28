//go:build linux

package vmguest

import (
	"golang.org/x/sys/unix"
	"os"
	"testing"
)

func TestV2LinuxEntryRejectsHostBeforeMutation(t *testing.T) {
	if os.Getpid() != 1 {
		if MainV2() == nil {
			t.Fatal("non-PID1 accepted")
		}
	}
}
func TestV2LinuxExtraFilesKeepContractOnFour(t *testing.T) {
	c := restrictedCommandV2(compileTypeScriptV2, 42, nil, os.Stdin)
	if len(c.ExtraFiles) != 2 || c.ExtraFiles[0] != nil || c.ExtraFiles[1] != os.Stdin || c.Dir != "/workspace" || c.Path != "/init" || len(c.Args) != 3 || c.Args[2] != "compile-typescript" || c.SysProcAttr.Credential.Uid != 65534 || c.SysProcAttr.Credential.Gid != 65534 || len(c.SysProcAttr.Credential.Groups) != 0 || !c.SysProcAttr.UseCgroupFD || c.SysProcAttr.CgroupFD != 42 || !childEnvironmentV2(c.Env) {
		t.Fatal("unsafe launch", c)
	}
}
func TestV2SealedContractReadonlyAndCanonical(t *testing.T) {
	c := childContractV2{1, probeNodeV2, []string{}, []string{}, []string{}, "/workspace", ""}
	f, e := sealedContractV2(c)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	flags, e := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if e != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		t.Fatal("contract mutable")
	}
	seals, e := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	if e != nil || seals&unix.F_SEAL_WRITE == 0 {
		t.Fatal("contract unsealed")
	}
}
