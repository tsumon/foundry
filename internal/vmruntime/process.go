package vmruntime

import "path/filepath"

// The command is entirely application-owned. No candidate or model string is
// part of argv, and no ambient executable search/environment is inherited.
func nativeCommand(root string, m Manifest) command { return nativeBootCommand(root, m.boot()) }
func nativeBootCommand(root string, m bootAssets) command {
	c := command{Path: filepath.Join(root, filepath.FromSlash(m.Runtime)), Dir: filepath.Join(root, "runtime"), Env: []string{"LANG=C", "LC_ALL=C"}}
	kernel, initrd := filepath.Join(root, m.Kernel), filepath.Join(root, m.RootFS)
	if m.HostPlatform == "darwin/arm64" {
		c.Args = []string{"--kernel", kernel, "--initrd", initrd}
		return c
	}
	c.Args = []string{"-no-user-config", "-nodefaults", "-machine", "q35,sata=off,usb=off", "-accel", "whpx", "-smp", "1", "-m", "2048", "-display", "none", "-monitor", "none", "-nic", "none", "-no-reboot", "-L", filepath.Join(root, "runtime", "share", "qemu"), "-chardev", "stdio,id=control,signal=off", "-serial", "chardev:control", "-kernel", kernel, "-initrd", initrd, "-append", "console=ttyS0 init=/init panic=1 quiet loglevel=0"}
	return c
}
