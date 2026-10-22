package sandbox

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// C4Trust is supplied by trusted process configuration, never request JSON.
// Plan building validates declarations only: no executable, endpoint or image
// is probed. The dormant adapter checks trust only after durable dispatch.
type C4Trust struct{ DockerPath, Endpoint, ManagedRoot string }

// C4Command is a reviewable argv/environment, not a shell string. The dormant
// Docker adapter binds it to one plan and uses Env verbatim.
// CaptureID instead identifies a bounded in-memory attach buffer: Path/Args/Env
// are empty and there is no executable command for that stage.
type C4Command struct {
	Path      string
	Args, Env []string
	CaptureID string
}

// C4Plan freezes its inputs and returns fresh command slices on every call.
type C4Plan struct {
	profile   domain.C4Profile
	operation domain.C4Operation
	candidate string
}

func validateC4Binding(p domain.C4Profile, o domain.C4Operation, trust C4Trust) error {
	if p.DockerPath != trust.DockerPath || p.Endpoint != trust.Endpoint {
		return errors.New("untrusted C4 transport declaration")
	}
	return domain.ValidateC4Operation(p, o)
}

func BuildC4Plan(p domain.C4Profile, o domain.C4Operation, trust C4Trust, candidate string) (C4Plan, error) {
	if err := validateC4Binding(p, o, trust); err != nil {
		return C4Plan{}, err
	}
	if err := validateC4Candidate(trust.ManagedRoot, candidate); err != nil {
		return C4Plan{}, err
	}
	return C4Plan{profile: p, operation: o, candidate: candidate}, nil
}

func validateC4Candidate(root, candidate string) error {
	for _, p := range []string{root, candidate} {
		forbidden := ",\x00\r\n\t\\"
		if runtime.GOOS == "windows" {
			forbidden = ",\x00\r\n\t"
		}
		if !filepath.IsAbs(p) || p == "/" || filepath.Clean(p) != p || strings.ContainsAny(p, forbidden) {
			return errors.New("unsafe managed candidate path")
		}
		if err := platformfs.ValidateDirectory(p); err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil || resolved != p {
			return errors.New("candidate paths must be canonical and symlink-free")
		}
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("candidate must be below managed root")
	}
	// Never traverse symlinks. Managed snapshots must exclude Git and special
	// files; Task 3 owns content/protected-file admission and digest fencing.
	return filepath.WalkDir(candidate, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" || d.Type()&fs.ModeSymlink != 0 || (!d.IsDir() && !d.Type().IsRegular()) || (path == candidate && !d.IsDir()) {
			return errors.New("unsafe candidate entry")
		}
		return nil
	})
}

func (p C4Plan) command(args ...string) C4Command {
	return C4Command{Path: p.profile.DockerPath, Args: append([]string{"--host", p.profile.Endpoint}, args...), Env: []string{"DOCKER_CONFIG=/dev/null"}}
}

// Raw CLI JSON may omit empty Cmd/Volumes. Map indexing emits null only for
// absent fields, including under missingkey=error. Struct input selects the
// CLI's buffered raw-JSON template fallback, not another Docker request.
// Preserve all existing metadata (including case aliases) for the unchanged
// bounded strict parser; never erase a nonempty or malformed security value.
func (p C4Plan) imageInspection() C4Command {
	const projection = `{{$volumes := index .Config "Volumes"}}{{$cmd := index .Config "Cmd"}}[{ ` +
		`{{$sep := ""}}{{range $key, $value := .}}{{$sep}}{{$sep = ","}}{{json $key}}:` +
		`{{if eq $key "Config"}}{"Volumes":{{json $volumes}},"Cmd":{{json $cmd}}` +
		`{{range $k, $v := $value}}{{if and (ne $k "Volumes") (ne $k "Cmd")}},{{json $k}}:{{json $v}}{{end}}{{end}}}` +
		`{{else}}{{json $value}}{{end}}{{end}}}]`
	return p.command("image", "inspect", "--format", projection, p.profile.ImageID)
}

func (p C4Plan) Create() C4Command {
	o := p.operation
	args := []string{"create", "--pull=never", "--platform=linux/arm64", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--no-healthcheck", "--pids-limit=128", "--memory=1073741824", "--memory-swap=1073741824", "--cpus=1", "--user=65534:65534", "--entrypoint=/opt/foundry/verify-go-v1", "--log-driver=none", "--name", o.ContainerName,
		"--label", "foundry.owner=" + o.Owner, "--label", "foundry.mission=" + o.MissionID, "--label", "foundry.operation=" + o.ID, "--label", "foundry.name=" + o.ContainerName,
		"--mount", "type=bind,src=" + p.candidate + ",dst=/workspace,readonly",
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=67108864,nr_inodes=16384,mode=1777",
		"--tmpfs", "/foundry-build:rw,nosuid,nodev,exec,size=268435456,nr_inodes=16384,mode=1777", "--workdir=/workspace"}
	env := domain.FixedC4Policy().Environment
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--env", key+"="+env[key])
	}
	args = append(args, p.profile.ImageID)
	return p.command(args...)
}

var c4ContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (p C4Plan) Command(stage, id string) (C4Command, error) {
	if !c4ContainerID.MatchString(id) {
		return C4Command{}, errors.New("exact full container ID required")
	}
	var args []string
	switch stage {
	case "start":
		args = []string{"container", "start", "--attach", id}
	case "wait":
		args = []string{"container", "wait", id}
	case "inspect":
		args = []string{"container", "inspect", id}
	case "logs":
		return C4Command{CaptureID: id}, nil
	case "remove":
		args = []string{"container", "rm", "--force", id}
	default:
		return C4Command{}, errors.New("unsupported C4 driver stage")
	}
	return p.command(args...), nil
}
