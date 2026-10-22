//go:build !windows

package sandbox

import (
	"io/fs"
	"syscall"
	"testing"
	"time"
)

// Entire fake filesystem: no host executable/socket path is ever read.
type c4TrustNode struct {
	mode fs.FileMode
	stat syscall.Stat_t
}

func (n c4TrustNode) Name() string       { return "node" }
func (n c4TrustNode) Size() int64        { return 0 }
func (n c4TrustNode) Mode() fs.FileMode  { return n.mode }
func (n c4TrustNode) ModTime() time.Time { return time.Time{} }
func (n c4TrustNode) IsDir() bool        { return n.mode.IsDir() }
func (n c4TrustNode) Sys() any           { return &n.stat }

type c4TrustFS struct {
	nodes map[string]c4TrustNode
	alias string
	calls int
}

func (f *c4TrustFS) Lstat(p string) (fs.FileInfo, error) {
	f.calls++
	n, ok := f.nodes[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return n, nil
}
func (f *c4TrustFS) EvalSymlinks(p string) (string, error) {
	f.calls++
	if p == f.alias {
		return "/foreign", nil
	}
	return p, nil
}
func c4TrustFixture() (*c4DockerTrust, *c4TrustFS) {
	f := &c4TrustFS{nodes: map[string]c4TrustNode{}}
	for i, p := range []string{"/", "/trusted", "/trusted/docker", "/trusted/socket"} {
		mode := fs.ModeDir | 0755
		if i == 2 {
			mode = 0755
		}
		if i == 3 {
			mode = fs.ModeSocket | 0660
		}
		f.nodes[p] = c4TrustNode{mode: mode, stat: syscall.Stat_t{Uid: 42, Ino: uint64(i + 1), Dev: 1}}
	}
	return &c4DockerTrust{trust: C4Trust{DockerPath: "/trusted/docker", Endpoint: "unix:///trusted/socket"}, fs: f, uid: 42}, f
}

// Catches accepting unsafe kinds/modes/owners/parents or following aliases.
func TestC4DockerTrustValidatesEntirePath(t *testing.T) {
	check, _ := c4TrustFixture()
	if err := check.Check(); err != nil {
		t.Fatal("safe fake paths rejected", err)
	}
	for _, tc := range []struct {
		name, path string
		mode       fs.FileMode
		uid        uint32
	}{
		{"binary writable", "/trusted/docker", 0775, 42}, {"binary not executable", "/trusted/docker", 0644, 42}, {"binary directory", "/trusted/docker", fs.ModeDir | 0755, 42}, {"binary symlink", "/trusted/docker", fs.ModeSymlink | 0755, 42}, {"foreign binary", "/trusted/docker", 0755, 77},
		{"socket file", "/trusted/socket", 0660, 42}, {"socket writable", "/trusted/socket", fs.ModeSocket | 0666, 42}, {"foreign socket", "/trusted/socket", fs.ModeSocket | 0660, 77}, {"parent writable", "/trusted", fs.ModeDir | 0775, 42}, {"parent foreign", "/trusted", fs.ModeDir | 0755, 77}, {"root writable", "/", fs.ModeDir | 0777, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			check, f := c4TrustFixture()
			n := f.nodes[tc.path]
			n.mode = tc.mode
			n.stat.Uid = tc.uid
			f.nodes[tc.path] = n
			if check.Check() == nil {
				t.Fatal("unsafe node accepted")
			}
		})
	}
	for _, p := range []string{"/trusted/docker", "/trusted/socket"} {
		check, f := c4TrustFixture()
		f.alias = p
		if check.Check() == nil {
			t.Fatal("alias accepted")
		}
	}
}

// Catches forgetting retained identity on a subsequent effect, including parents.
func TestC4DockerTrustRejectsReplacement(t *testing.T) {
	for _, p := range []string{"/trusted/docker", "/trusted/socket", "/trusted"} {
		check, f := c4TrustFixture()
		if err := check.Check(); err != nil {
			t.Fatal(err)
		}
		n := f.nodes[p]
		n.stat.Ino += 100
		f.nodes[p] = n
		if check.Check() == nil {
			t.Fatal("replacement accepted", p)
		}
	}
}

func TestC4DockerPrivateCopyDoesNotRelaxInstalledPathTrust(t *testing.T) {
	check, f := c4TrustFixture()
	f.nodes["/Applications"] = c4TrustNode{mode: fs.ModeDir | 0775, stat: syscall.Stat_t{Uid: 0, Ino: 80, Dev: 1}}
	f.nodes["/Applications/docker"] = c4TrustNode{mode: 0755, stat: syscall.Stat_t{Uid: 0, Ino: 81, Dev: 1}}
	check.trust.DockerPath = "/Applications/docker"
	if check.Check() == nil {
		t.Fatal("writable installed ancestor accepted")
	}
	// A distinct private copy is a separate transport, never an exception for
	// the installed path. Neither the fixture nor the implementation chmods it.
	check, f = c4TrustFixture()
	parent := f.nodes["/trusted"]
	parent.mode = fs.ModeDir | 0700
	f.nodes["/trusted"] = parent
	copy := f.nodes["/trusted/docker"]
	copy.mode = 0500
	f.nodes["/trusted/docker"] = copy
	if err := check.Check(); err != nil {
		t.Fatal("protected private copy rejected", err)
	}
	copy.stat.Ino++
	f.nodes["/trusted/docker"] = copy
	if check.Check() == nil {
		t.Fatal("private copy replacement accepted")
	}
}
