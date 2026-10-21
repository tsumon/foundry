package sandbox

import (
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func c4PlanFixture(t *testing.T) (domain.C4Profile, domain.C4Operation, C4Trust, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(root, "candidate")
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	p := domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
	o := domain.C4Operation{SchemaVersion: 1, ID: "op-1", Kind: "baseline", MissionID: "mission-1", Epoch: 1, BaseCommit: strings.Repeat("b", 40), BaseDigest: strings.Repeat("c", 64), ProfileDigest: p.ProfileDigest(), ContainerName: "foundry-op-1", Owner: "owner-1", State: "approved", PreparedAt: "2026-10-21T00:00:00Z"}
	o.MaterialDigest = domain.C4MaterialDigest(o)
	o.Approval = &domain.C4Approval{Digest: o.MaterialDigest, Revision: 1, At: o.PreparedAt}
	return p, o, C4Trust{DockerPath: p.DockerPath, Endpoint: p.Endpoint, ManagedRoot: root}, candidate
}

func TestC4PlanConfinement(t *testing.T) {
	p, o, trust, candidate := c4PlanFixture(t)
	plan, err := BuildC4Plan(p, o, trust, candidate)
	if err != nil {
		t.Fatal(err)
	}
	c := plan.Create()
	joined := strings.Join(c.Args, " ")
	for _, want := range []string{"--host unix:///synthetic/docker.sock create", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=1073741824", "--memory-swap=1073741824", "--cpus=1", "--pids-limit=128", "--user=65534:65534", "--entrypoint=/opt/foundry/verify-go-v1", "--no-healthcheck", "--log-driver=none", "type=bind,src=" + candidate + ",dst=/workspace,readonly", "/tmp:rw,nosuid,nodev,noexec,size=67108864,nr_inodes=16384,mode=1777", "/foundry-build:rw,nosuid,nodev,exec,size=268435456,nr_inodes=16384,mode=1777", "foundry.operation=op-1", "foundry.name=foundry-op-1", "--env GOPROXY=off"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %s", want, joined)
		}
	}
	if strings.Count(joined, "--mount") != 1 || strings.Count(joined, "--tmpfs") != 2 || strings.Contains(joined, "sh -c") || strings.Contains(joined, "--publish") || c.Args[len(c.Args)-1] != p.ImageID {
		t.Fatal("unsafe create command", joined)
	}
	if !reflect.DeepEqual(c.Env, []string{"DOCKER_CONFIG=/dev/null"}) {
		t.Fatal("uncontrolled client environment", c.Env)
	}
	id := strings.Repeat("d", 64)
	for _, stage := range []string{"start", "wait", "inspect", "remove"} {
		cmd, e := plan.Command(stage, id)
		if e != nil {
			t.Fatal(e)
		}
		if cmd.Path != p.DockerPath || !reflect.DeepEqual(cmd.Args[:2], []string{"--host", p.Endpoint}) || cmd.Args[len(cmd.Args)-1] != id || !reflect.DeepEqual(cmd.Env, c.Env) {
			t.Fatal("unbound command", cmd)
		}
		if stage == "start" && !strings.Contains(strings.Join(cmd.Args, " "), "start --attach") {
			t.Fatal("logging disabled without attached capture", cmd)
		}
	}
	logs, e := plan.Command("logs", id)
	if e != nil || logs.Path != "" || len(logs.Args) != 0 || len(logs.Env) != 0 {
		t.Fatal("log-driver=none cannot use docker logs", logs, e)
	}
	c.Args[0] = "bad"
	c.Env[0] = "SECRET=bad"
	if plan.Create().Args[0] != "--host" || plan.Create().Env[0] != "DOCKER_CONFIG=/dev/null" {
		t.Fatal("mutable plan")
	}
}

func TestC4PlanRejectsTransportAndMountEscapes(t *testing.T) {
	for _, name := range []string{"relative binary", "untrusted binary", "remote endpoint", "untrusted socket", "tag", "digest", "relative candidate", "root mount", "outside root", "comma", "symlink", "nested symlink", "git", "special file"} {
		t.Run(name, func(t *testing.T) {
			p, o, trust, candidate := c4PlanFixture(t)
			switch name {
			case "relative binary":
				p.DockerPath = "docker"
			case "untrusted binary":
				p.DockerPath = "/untrusted/docker"
			case "remote endpoint":
				p.Endpoint = "tcp://localhost:2375"
			case "untrusted socket":
				p.Endpoint = "unix:///other.sock"
			case "tag":
				p.ImageID = "go:latest"
			case "digest":
				o.MaterialDigest = strings.Repeat("f", 64)
			case "relative candidate":
				candidate = "candidate"
			case "root mount":
				candidate = trust.ManagedRoot
			case "outside root":
				candidate = t.TempDir()
			case "comma":
				candidate += ",rw"
				_ = os.Mkdir(candidate, 0700)
			case "symlink":
				target := candidate
				candidate = filepath.Join(trust.ManagedRoot, "link")
				if err := os.Symlink(target, candidate); err != nil {
					t.Fatal(err)
				}
			case "nested symlink":
				if err := os.Symlink("/etc", filepath.Join(candidate, "escape")); err != nil {
					t.Fatal(err)
				}
			case "git":
				if err := os.Mkdir(filepath.Join(candidate, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			case "special file":
				candidate = filepath.Join(candidate, "missing")
			}
			if _, err := BuildC4Plan(p, o, trust, candidate); err == nil {
				t.Fatal("unsafe plan accepted")
			}
		})
	}
}
