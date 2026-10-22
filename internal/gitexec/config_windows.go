package gitexec

import (
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func New() (Config, error) {
	var c Config
	programs, e := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if e != nil {
		return c, e
	}
	c.Path = filepath.Join(programs, "Git", "cmd", "git.exe")
	f, e := platformfs.OpenRegular(c.Path)
	if e != nil {
		return c, e
	}
	f.Close()
	outer, e := os.MkdirTemp("", "foundry-git-")
	if e != nil {
		return c, e
	}
	c.cleanup = func() { os.RemoveAll(outer) }
	private := filepath.Join(outer, "private")
	if e = platformfs.PrivateDirectory(private); e != nil {
		c.Close()
		return c, e
	}
	c.Hooks = filepath.Join(private, "hooks")
	if e = platformfs.PrivateDirectory(c.Hooks); e != nil {
		c.Close()
		return c, e
	}
	empty := filepath.Join(private, "empty-config")
	if e = os.WriteFile(empty, nil, 0600); e != nil {
		c.Close()
		return c, e
	}
	system, e := windows.KnownFolderPath(windows.FOLDERID_Windows, 0)
	if e != nil {
		c.Close()
		return c, e
	}
	c.Root = system
	c.Environment = []string{"PATH=" + filepath.Join(programs, "Git", "cmd") + ";" + filepath.Join(system, "System32"), "SYSTEMROOT=" + system, "WINDIR=" + system, "HOME=" + private, "XDG_CONFIG_HOME=" + private, "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=" + empty, "GIT_CONFIG_GLOBAL=" + empty, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=0", "GIT_ATTR_NOSYSTEM=1"}
	return c, nil
}
