package vmruntime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFixedNativeCommandsExcludeInheritedConfiguration(t *testing.T) {
	t.Setenv("PATH", "/attacker")
	t.Setenv("DYLD_INSERT_LIBRARIES", "/attacker.dylib")
	t.Setenv("FOUNDRY_VM_HELPER", "/attacker")
	for _, host := range []string{"darwin/arm64", "windows/amd64"} {
		m := Manifest{HostPlatform: host, Runtime: "runtime/foundry-vm-helper", Kernel: "kernel", RootFS: "initramfs.cpio.gz"}
		if strings.HasPrefix(host, "windows") {
			m.Runtime = "runtime/qemu-system-x86_64.exe"
		}
		c := nativeCommand(filepath.Join(t.TempDir(), "assets"), m)
		if len(c.Env) != 2 || strings.Contains(strings.Join(c.Env, " "), "attacker") {
			t.Fatal("inherited environment")
		}
		a := strings.Join(c.Args, " ")
		if strings.HasPrefix(host, "windows") && !strings.Contains(a, "-machine q35,sata=off,usb=off") {
			t.Fatal("implicit SATA/USB controller enabled")
		}
		if strings.HasPrefix(host, "windows") {
			if !strings.Contains(a, "-accel whpx") || !strings.Contains(a, "-nic none") || !strings.Contains(a, "-monitor none") {
				t.Fatal("missing VM restrictions")
			}
			for _, bad := range []string{"tcg", "virtfs", "netdev", "hostfwd", "shell", "cmd.exe"} {
				if strings.Contains(a, bad) {
					t.Fatal("unsafe invocation")
				}
			}
		}
		if !filepath.IsAbs(c.Path) {
			t.Fatal("executable search permitted")
		}
	}
}
