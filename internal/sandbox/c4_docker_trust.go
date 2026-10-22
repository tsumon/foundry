//go:build !windows

package sandbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type c4DockerTrustCheck interface{ Check() error }
type c4DockerFS interface {
	Lstat(string) (fs.FileInfo, error)
	EvalSymlinks(string) (string, error)
}
type c4DockerIdentity struct {
	dev  uint64
	ino  uint64
	uid  uint32
	mode fs.FileMode
}
type c4DockerTrust struct {
	trust    C4Trust
	fs       c4DockerFS
	uid      uint32
	retained map[string]c4DockerIdentity
}
type c4DockerOSFS struct{}

func (c4DockerOSFS) Lstat(p string) (fs.FileInfo, error)   { return os.Lstat(p) }
func (c4DockerOSFS) EvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }

var errC4DockerTrust = errors.New("C4 Docker trust not established")

// Identity checks reduce accidental path replacement, but cannot close the
// check/exec or check/connect TOCTOU windows against a compromised same-user
// host or daemon. No socket connection or readiness query is performed here.
func (t *c4DockerTrust) Check() error {
	if t.fs == nil || !strings.HasPrefix(t.trust.Endpoint, "unix:///") {
		return errC4DockerTrust
	}
	next := map[string]c4DockerIdentity{}
	for i, path := range []string{t.trust.DockerPath, strings.TrimPrefix(t.trust.Endpoint, "unix://")} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return errC4DockerTrust
		}
		resolved, err := t.fs.EvalSymlinks(path)
		if err != nil || resolved != path {
			return errC4DockerTrust
		}
		for p := path; ; p = filepath.Dir(p) {
			info, err := t.fs.Lstat(p)
			if err != nil {
				return errC4DockerTrust
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (stat.Uid != 0 && stat.Uid != t.uid) {
				return errC4DockerTrust
			}
			mode := info.Mode()
			switch {
			case p != path:
				if !mode.IsDir() || mode.Perm()&0022 != 0 {
					return errC4DockerTrust
				}
			case i == 0:
				if !mode.IsRegular() || mode.Perm()&0111 == 0 || mode.Perm()&0022 != 0 {
					return errC4DockerTrust
				}
			case i == 1:
				if mode.Type() != fs.ModeSocket || mode.Perm()&0002 != 0 {
					return errC4DockerTrust
				}
			}
			identity := c4DockerIdentity{dev: uint64(stat.Dev), ino: stat.Ino, uid: stat.Uid, mode: mode}
			if old, exists := t.retained[p]; t.retained != nil && (!exists || old != identity) {
				return errC4DockerTrust
			}
			next[p] = identity
			if p == "/" {
				break
			}
		}
	}
	t.retained = next
	return nil
}
