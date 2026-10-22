package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const C4OperationLimit = 64

// C4Profile is a declaration, not proof of readiness or permission to execute.
// Policy is derived from the version, never accepted as caller overrides.
type C4Profile struct {
	SchemaVersion  int    `json:"schema_version"`
	ControlVersion int    `json:"control_version,omitempty"`
	ID             string `json:"id"`
	Mode           string `json:"mode"`
	ImageID        string `json:"image_id"`
	Platform       string `json:"platform"`
	DockerPath     string `json:"docker_path"`
	Endpoint       string `json:"endpoint"`
	Verifier       string `json:"verifier"`
}

type C4Tmpfs struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Inodes    int    `json:"inodes"`
	Exec      bool   `json:"exec"`
	Nosuid    bool   `json:"nosuid"`
	Nodev     bool   `json:"nodev"`
}

type C4Policy struct {
	CPUs               int               `json:"cpus"`
	MemoryBytes        int64             `json:"memory_bytes"`
	PidsLimit          int               `json:"pids_limit"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	OutputLimitBytes   int               `json:"output_limit_bytes"`
	UID                int               `json:"uid"`
	GID                int               `json:"gid"`
	ReadOnlyRoot       bool              `json:"read_only_root"`
	ReadOnlyCandidate  bool              `json:"read_only_candidate"`
	NoNewPrivileges    bool              `json:"no_new_privileges"`
	Network            string            `json:"network"`
	Pull               string            `json:"pull"`
	Logging            string            `json:"logging"`
	DisableHealthcheck bool              `json:"disable_healthcheck"`
	DropCapabilities   []string          `json:"drop_capabilities"`
	Ports              []string          `json:"ports"`
	Tmpfs              []C4Tmpfs         `json:"tmpfs"`
	GoCommand          []string          `json:"go_command"`
	GoVersion          string            `json:"go_version"`
	Environment        map[string]string `json:"environment"`
}

// FixedC4Policy returns fresh storage so callers cannot mutate another run's policy.
func FixedC4Policy() C4Policy {
	return C4Policy{CPUs: 1, MemoryBytes: 1 << 30, PidsLimit: 128, TimeoutSeconds: 120, OutputLimitBytes: 64 << 10, UID: 65534, GID: 65534,
		ReadOnlyRoot: true, ReadOnlyCandidate: true, NoNewPrivileges: true, Network: "none", Pull: "never", Logging: "none", DisableHealthcheck: true,
		DropCapabilities: []string{"ALL"}, Ports: []string{},
		Tmpfs:     []C4Tmpfs{{Path: "/tmp", SizeBytes: 64 << 20, Inodes: 16384, Nosuid: true, Nodev: true}, {Path: "/foundry-build", SizeBytes: 256 << 20, Inodes: 16384, Exec: true, Nosuid: true, Nodev: true}},
		GoVersion: "1.27.3", GoCommand: []string{"go", "test", "-json", "-count=1", "-timeout=90s", "./..."},
		Environment: map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "CGO_ENABLED": "0", "GOCACHE": "/foundry-build/cache", "GOTMPDIR": "/foundry-build/tmp", "GOPATH": "/foundry-build/gopath", "TMPDIR": "/tmp"}}
}

func c4Digest(kind string, value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(append([]byte("foundry:c4:v1:"+kind+"\n"), b...))
	return hex.EncodeToString(h[:])
}

func (p C4Profile) ProfileDigest() string {
	return c4Digest("profile", struct {
		Profile C4Profile `json:"profile"`
		Policy  C4Policy  `json:"policy"`
	}{p, FixedC4Policy()})
}

type C4Fact struct {
	Revision int64  `json:"revision"`
	At       string `json:"at"`
}

type C4Approval struct {
	Digest   string `json:"digest"`
	Revision int64  `json:"revision"`
	At       string `json:"at"`
}

// Class is passed, test_failed, infrastructure_failed, timeout, oom,
// interrupted, outcome_unknown, or persistence_failed. Only test_failed is
// repair-eligible; a passed command is not a passed operation without cleanup.
type C4Result struct {
	Class     string `json:"class"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Tests     int    `json:"tests"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
	Revision  int64  `json:"revision"`
	At        string `json:"at"`
}

// Each cleanup attempt is immutable. Recovery may append exact-owned cleanup
// evidence even when a crash prevented recording the created-container fact.
type C4Cleanup struct {
	State       string `json:"state"`
	ContainerID string `json:"container_id"`
	Owner       string `json:"owner"`
	Revision    int64  `json:"revision"`
	At          string `json:"at"`
}

type C4Operation struct {
	SchemaVersion       int            `json:"schema_version"`
	ID                  string         `json:"id"`
	Kind                string         `json:"kind"`
	MissionID           string         `json:"mission_id"`
	Epoch               int64          `json:"epoch"`
	PlanRevision        int64          `json:"plan_revision"`
	BaseCommit          string         `json:"base_commit"`
	BaseDigest          string         `json:"base_digest"`
	CandidateGeneration *GenerationRef `json:"candidate_generation,omitempty"`
	CandidateDigest     string         `json:"candidate_digest,omitempty"`
	ProfileDigest       string         `json:"profile_digest"`
	ReadinessID         string         `json:"readiness_id,omitempty"`
	ReadinessDigest     string         `json:"readiness_digest,omitempty"`
	MaterialDigest      string         `json:"material_digest"`
	ContainerName       string         `json:"container_name"`
	ContainerID         string         `json:"container_id,omitempty"`
	Owner               string         `json:"owner"`
	State               string         `json:"state"`
	PreparedAt          string         `json:"prepared_at"`
	Approval            *C4Approval    `json:"approval,omitempty"`
	Dispatch            *C4Fact        `json:"dispatch,omitempty"`
	Created             *C4Fact        `json:"created,omitempty"`
	Started             *C4Fact        `json:"started,omitempty"`
	Result              *C4Result      `json:"result,omitempty"`
	Cleanup             []C4Cleanup    `json:"cleanup,omitempty"`
}

// C4MaterialDigest excludes all lifecycle facts, including approval itself.
func C4MaterialDigest(o C4Operation) string {
	o.MaterialDigest = ""
	o.State = ""
	o.ContainerID = ""
	o.Approval = nil
	o.Dispatch = nil
	o.Created = nil
	o.Started = nil
	o.Result = nil
	o.Cleanup = nil
	return c4Digest("material", o)
}

func (o C4Operation) CleanupState() string {
	if len(o.Cleanup) == 0 {
		return "pending"
	}
	return o.Cleanup[len(o.Cleanup)-1].State
}

func (o C4Operation) Passed() bool {
	if o.SchemaVersion != 1 || o.State != "finished" || o.Approval == nil || o.Dispatch == nil || o.Created == nil || o.Started == nil || o.Result == nil || o.Result.Class != "passed" || o.Result.ExitCode == nil || *o.Result.ExitCode != 0 || o.Result.Tests <= 0 || o.CleanupState() != "confirmed" {
		return false
	}
	c := o.Cleanup[len(o.Cleanup)-1]
	return o.ContainerID != "" && c.ContainerID == o.ContainerID && c.Owner == o.Owner
}

var c4Opaque = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var c4SHA = regexp.MustCompile(`^[a-f0-9]{64}$`)
var c4Commit = regexp.MustCompile(`^([a-f0-9]{40}|[a-f0-9]{64})$`)

func c4Absolute(p string) bool {
	return strings.HasPrefix(p, "/") && p != "/" && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n\t ")
}

func (p C4Profile) Validate() error {
	if p.ControlVersion != 0 && p.ControlVersion != 1 {
		return errors.New("unsupported C4 control version")
	}
	u, err := url.Parse(p.Endpoint)
	if p.SchemaVersion != 1 || p.ID != "c4_go_v1" || (p.Mode != "synthetic" && p.Mode != "docker") || p.Platform != "linux/arm64" || p.Verifier != "/opt/foundry/verify-go-v1" || !strings.HasPrefix(p.ImageID, "sha256:") || !c4SHA.MatchString(strings.TrimPrefix(p.ImageID, "sha256:")) || !c4Absolute(p.DockerPath) || err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !c4Absolute(u.Path) || p.Endpoint != "unix://"+u.Path {
		return errors.New("invalid or unsupported C4 profile")
	}
	return nil
}

// ValidateC4Mission never normalizes unrecognized data into a valid declaration.
func ValidateC4Mission(m Mission) error {
	if err := ValidateC4Controls(m); err != nil {
		return err
	}
	if m.C4Invalid {
		return errors.New("corrupt C4 record")
	}
	if m.Spec.C4 == nil {
		if len(m.C4Operations) > 0 {
			return errors.New("C4 operations require profile")
		}
		return nil
	}
	if err := m.Spec.C4.Validate(); err != nil {
		return err
	}
	if len(m.C4Operations) > C4OperationLimit {
		return errors.New("C4 operation limit exceeded")
	}
	seen, names := map[string]bool{}, map[string]bool{}
	materials := map[string]bool{}
	for _, o := range m.C4Operations {
		if seen[o.ID] || names[o.ContainerName] {
			return errors.New("duplicate C4 operation identity")
		}
		seen[o.ID] = true
		names[o.ContainerName] = true
		key := fmt.Sprintf("%s:%d:%d:%s", o.Kind, o.Epoch, o.PlanRevision, o.CandidateDigest)
		if materials[key] {
			return errors.New("C4 material cannot be retried")
		}
		materials[key] = true
		if err := validateC4Operation(m, o); err != nil {
			return fmt.Errorf("C4 operation %s: %w", o.ID, err)
		}
		if err := validateC4ReadinessBinding(m, o); err != nil {
			return err
		}
	}
	return nil
}

func validateC4Operation(m Mission, o C4Operation) error {
	if o.SchemaVersion != 1 || !c4Opaque.MatchString(o.ID) || o.MissionID != m.ID || o.Epoch < 0 || o.Epoch > m.Epoch || o.PlanRevision < 0 || !c4Commit.MatchString(o.BaseCommit) || !c4SHA.MatchString(o.BaseDigest) || o.ProfileDigest != m.Spec.C4.ProfileDigest() || o.MaterialDigest != C4MaterialDigest(o) || !c4Opaque.MatchString(o.ContainerName) || !c4Opaque.MatchString(o.Owner) {
		return errors.New("invalid immutable binding")
	}
	switch o.Kind {
	case "baseline":
		if o.CandidateGeneration != nil || o.CandidateDigest != "" {
			return errors.New("baseline cannot bind candidate")
		}
	case "candidate":
		if o.PlanRevision <= 0 || o.CandidateGeneration == nil || !c4Opaque.MatchString(o.CandidateGeneration.ID) || !c4SHA.MatchString(o.CandidateDigest) || o.CandidateGeneration.Digest != o.CandidateDigest {
			return errors.New("invalid candidate binding")
		}
	default:
		return errors.New("unsupported operation kind")
	}
	last, err := time.Parse(time.RFC3339Nano, o.PreparedAt)
	if err != nil {
		return errors.New("invalid preparation time")
	}
	lastRevision := int64(0)
	check := func(rev int64, at string) error {
		t, e := time.Parse(time.RFC3339Nano, at)
		if e != nil || t.Before(last) || rev < 1 || rev > m.Revision || rev < lastRevision {
			return errors.New("invalid stage timestamp or revision")
		}
		last = t
		lastRevision = rev
		return nil
	}
	state := "prepared"
	if o.Approval != nil {
		if !c4SHA.MatchString(o.Approval.Digest) || (o.Kind == "baseline" && o.Approval.Digest != o.MaterialDigest) {
			return errors.New("invalid approval binding")
		}
		// Candidate execute approval predates preparation; its copied timestamp
		// is validated separately instead of pretending it is fresh consent.
		if o.Kind == "candidate" {
			if _, e := time.Parse(time.RFC3339Nano, o.Approval.At); e != nil || o.Approval.Revision < 1 || o.Approval.Revision > m.Revision {
				return errors.New("invalid execute approval")
			}
			lastRevision = o.Approval.Revision
		} else if err := check(o.Approval.Revision, o.Approval.At); err != nil {
			return err
		}
		state = "approved"
	}
	if o.Dispatch != nil {
		if state != "approved" {
			return errors.New("dispatch without approval")
		}
		if err := check(o.Dispatch.Revision, o.Dispatch.At); err != nil {
			return err
		}
		state = "dispatched"
	}
	if o.Created != nil {
		if state != "dispatched" || !c4Opaque.MatchString(o.ContainerID) {
			return errors.New("create without dispatch/identity")
		}
		if err := check(o.Created.Revision, o.Created.At); err != nil {
			return err
		}
		state = "created"
	} else if o.ContainerID != "" {
		return errors.New("container without created fact")
	}
	if o.Started != nil {
		if state != "created" {
			return errors.New("start without create")
		}
		if err := check(o.Started.Revision, o.Started.At); err != nil {
			return err
		}
		state = "started"
	}
	if r := o.Result; r != nil {
		if err := check(r.Revision, r.At); err != nil {
			return err
		}
		if r.Tests < 0 || len(r.Output) > FixedC4Policy().OutputLimitBytes || !utf8.ValidString(r.Output) {
			return errors.New("invalid bounded result")
		}
		for _, c := range r.Output {
			if unicode.IsControl(c) && c != '\n' && c != '\t' {
				return errors.New("unsanitized result output")
			}
		}
		switch r.Class {
		case "passed":
			if state != "started" || r.ExitCode == nil || *r.ExitCode != 0 || r.Tests == 0 {
				return errors.New("invalid passing test result")
			}
		case "test_failed":
			if state != "started" || r.ExitCode == nil || *r.ExitCode == 0 {
				return errors.New("invalid test failure")
			}
		case "oom", "timeout":
			if state != "started" {
				return errors.New("execution outcome without start")
			}
		case "infrastructure_failed", "interrupted", "outcome_unknown", "persistence_failed":
		default:
			return errors.New("unsupported execution result")
		}
		state = "finished"
	}
	if o.State != state {
		return errors.New("state disagrees with durable facts")
	}
	if len(o.Cleanup) > C4OperationLimit {
		return errors.New("cleanup attempt limit exceeded")
	}
	confirmed := false
	container := o.ContainerID
	for _, c := range o.Cleanup {
		if o.Result == nil || o.Dispatch == nil || confirmed || (c.State != "failed" && c.State != "confirmed") || !c4Opaque.MatchString(c.ContainerID) || c.Owner != o.Owner || (container != "" && container != c.ContainerID) {
			return errors.New("invalid exact-owned cleanup")
		}
		if err := check(c.Revision, c.At); err != nil {
			return err
		}
		container = c.ContainerID
		confirmed = c.State == "confirmed"
	}
	return nil
}

func c4Current(m Mission, o C4Operation) bool {
	plan := int64(0)
	if m.Plan != nil {
		plan = m.Plan.Revision
	}
	return o.Epoch == m.Epoch && o.PlanRevision == plan && o.BaseCommit == m.BaseCommit && (o.Kind == "baseline" || reflect.DeepEqual(o.CandidateGeneration, m.CandidateGeneration))
}

func c4ExecuteApproval(m Mission, o C4Operation) bool {
	for _, a := range m.Approvals {
		if a.Kind == "execute" && a.PlanRevision == o.PlanRevision && c4SHA.MatchString(a.Digest) && a.Revision > 0 && a.Revision <= m.Revision {
			if _, err := time.Parse(time.RFC3339Nano, a.CreatedAt); err != nil {
				continue
			}
			if o.Approval == nil || (a.Digest == o.Approval.Digest && a.Revision == o.Approval.Revision && a.CreatedAt == o.Approval.At) {
				return true
			}
		}
	}
	return false
}

// ValidateC4Transition checks snapshots, not aliases. Callers must freeze before
// invoking mutation. Historical outcomes survive replans but cannot be rewritten.
func ValidateC4Transition(before, after Mission) error {
	if err := ValidateC4ControlTransition(before, after); err != nil {
		return err
	}
	if err := ValidateC4Mission(before); err != nil {
		return err
	}
	if err := ValidateC4Mission(after); err != nil {
		return err
	}
	if !reflect.DeepEqual(before.Spec.C4, after.Spec.C4) || len(after.C4Operations) < len(before.C4Operations) {
		return errors.New("C4 profile and trace are immutable")
	}
	for i, a := range before.C4Operations {
		b := after.C4Operations[i]
		if a.MaterialDigest != b.MaterialDigest {
			return errors.New("C4 material is immutable")
		}
		if len(b.Cleanup) < len(a.Cleanup) || !reflect.DeepEqual(a.Cleanup, b.Cleanup[:len(a.Cleanup)]) && len(a.Cleanup) > 0 {
			return errors.New("C4 cleanup facts are append-only")
		}
		cleanupAdded := len(b.Cleanup) - len(a.Cleanup)
		if cleanupAdded > 1 {
			return errors.New("C4 stages require separate durable transitions")
		}
		if a.Result != nil {
			x := b
			x.Cleanup = a.Cleanup
			if !reflect.DeepEqual(a, x) {
				return errors.New("terminal C4 execution facts are immutable")
			}
		} else {
			if a.Approval != nil && !reflect.DeepEqual(a.Approval, b.Approval) || a.Dispatch != nil && !reflect.DeepEqual(a.Dispatch, b.Dispatch) || a.Created != nil && (!reflect.DeepEqual(a.Created, b.Created) || a.ContainerID != b.ContainerID) || a.Started != nil && !reflect.DeepEqual(a.Started, b.Started) {
				return errors.New("C4 stage facts are immutable")
			}
			added := cleanupAdded
			for _, pair := range [][2]*C4Fact{{a.Dispatch, b.Dispatch}, {a.Created, b.Created}, {a.Started, b.Started}} {
				if pair[0] == nil && pair[1] != nil {
					added++
					if pair[1].Revision != before.Revision {
						return errors.New("stale C4 stage revision")
					}
				}
			}
			if a.Approval == nil && b.Approval != nil {
				added++
				if b.Kind == "baseline" && b.Approval.Revision != before.Revision {
					return errors.New("stale baseline approval")
				}
			}
			if a.Result == nil && b.Result != nil {
				added++
				if b.Result.Revision != before.Revision {
					return errors.New("stale C4 outcome revision")
				}
			}
			if added > 1 {
				return errors.New("C4 stages require separate durable transitions")
			}
			changed := !reflect.DeepEqual(a, b)
			recovery := b.Result != nil && (b.Result.Class == "interrupted" || b.Result.Class == "outcome_unknown")
			if changed && !recovery && (!c4Current(before, a) || !c4Current(after, b)) {
				return errors.New("stale C4 execution binding")
			}
			if b.Kind == "candidate" && (a.Approval == nil && b.Approval != nil || a.Dispatch == nil && b.Dispatch != nil) && (!c4ExecuteApproval(before, b) || !c4ExecuteApproval(after, b)) {
				return errors.New("candidate requires existing execute approval")
			}
		}
		for _, c := range b.Cleanup[len(a.Cleanup):] {
			if c.Revision != before.Revision {
				return errors.New("stale cleanup revision")
			}
		}
	}
	for _, o := range after.C4Operations[len(before.C4Operations):] {
		if err := validateC4ReadinessBinding(before, o); err != nil {
			return err
		}
		if o.State != "prepared" || !c4Current(before, o) || !c4Current(after, o) {
			return errors.New("C4 append must prepare current material")
		}
		if o.Kind == "candidate" && (!c4ExecuteApproval(before, o) || !c4ExecuteApproval(after, o)) {
			return errors.New("candidate requires existing execute approval")
		}
	}
	return nil
}
