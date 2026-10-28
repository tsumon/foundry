package vmruntime

import (
	"encoding/json"
	"foundry/internal/domain"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestV1ReceiptAndNativeCommandGolden(t *testing.T) {
	s := &Session{id: "id", owner: Request{OperationID: "op", MissionID: "mission", Owner: "owner", Name: "name", CandidateDigest: "candidate", CandidatePath: "private"}, runtime: &Runtime{identity: domain.C4VMIdentity{ManifestDigest: "manifest"}}}
	b, e := s.receiptBytes("allocated", nil)
	want := `{"schema_version":1,"ID":"id","OperationID":"op","MissionID":"mission","Owner":"owner","Name":"name","CandidateDigest":"candidate","ManifestDigest":"manifest","Stage":"allocated"}`
	if e != nil || string(b) != want {
		t.Fatalf("v1 receipt changed: %s %v", b, e)
	}
	root := filepath.Join(string(filepath.Separator), "owned", "assets")
	kernel := filepath.Join(root, "kernel")
	initrd := filepath.Join(root, "initramfs.cpio.gz")
	for _, host := range []string{"darwin/arm64", "windows/amd64"} {
		exe := "runtime/foundry-vm-helper"
		args := []string{"--kernel", kernel, "--initrd", initrd}
		if host == "windows/amd64" {
			exe = "runtime/qemu-system-x86_64.exe"
			args = []string{"-no-user-config", "-nodefaults", "-machine", "q35,sata=off,usb=off", "-accel", "whpx", "-smp", "1", "-m", "2048", "-display", "none", "-monitor", "none", "-nic", "none", "-no-reboot", "-L", filepath.Join(root, "runtime", "share", "qemu"), "-chardev", "stdio,id=control,signal=off", "-serial", "chardev:control", "-kernel", kernel, "-initrd", initrd, "-append", "console=ttyS0 init=/init panic=1 quiet loglevel=0"}
		}
		m := Manifest{HostPlatform: host, Runtime: exe, Kernel: "kernel", RootFS: "initramfs.cpio.gz"}
		expected := command{Path: filepath.Join(root, filepath.FromSlash(exe)), Args: args, Env: []string{"LANG=C", "LC_ALL=C"}, Dir: filepath.Join(root, "runtime")}
		if !reflect.DeepEqual(nativeCommand(root, m), expected) {
			t.Fatal("v1 command changed")
		}
		m2 := ManifestV2{HostPlatform: host, Runtime: exe, Kernel: m.Kernel, RootFS: m.RootFS}
		if !reflect.DeepEqual(nativeBootCommand(root, m2.boot()), expected) {
			t.Fatal("v2 introduced language command")
		}
	}
}
func TestV1ManifestRawGolden(t *testing.T) {
	// Expected bytes are independently spelled; v2 fields must never enter v1.
	m := Manifest{SchemaVersion: 1, HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1", Runtime: "runtime/foundry-vm-helper", Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest", Assets: []Asset{{Path: "kernel", SHA256: strings.Repeat("a", 64), Size: 7}}}
	b, e := json.Marshal(m)
	want := `{"schema_version":1,"host_platform":"darwin/arm64","guest_platform":"linux/arm64","protocol":"foundry-vm-v1","policy_version":"go-vm-v1","runtime":"runtime/foundry-vm-helper","kernel":"kernel","rootfs":"initramfs.cpio.gz","verifier":"foundry-vm-guest","assets":[{"path":"kernel","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":7}]}`
	if e != nil || string(b) != want {
		t.Fatal("v1 manifest changed")
	}
}
