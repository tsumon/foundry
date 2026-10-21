package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const c4Time = "2026-10-21T00:00:00Z"

func c4Fixture() Mission {
	p := &C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
	m := Mission{ID: "c4", Epoch: 1, Revision: 10, BaseCommit: strings.Repeat("b", 40), Spec: Spec{C4: p}}
	o := C4Operation{SchemaVersion: 1, ID: "baseline-1", Kind: "baseline", MissionID: m.ID, Epoch: 1, BaseCommit: m.BaseCommit, BaseDigest: strings.Repeat("c", 64), ProfileDigest: p.ProfileDigest(), ContainerName: "foundry-c4-baseline-1", Owner: "owner-1", State: "prepared", PreparedAt: c4Time}
	o.MaterialDigest = C4MaterialDigest(o)
	m.C4Operations = []C4Operation{o}
	return m
}

func cloneC4(m Mission) Mission {
	b, _ := json.Marshal(m)
	var out Mission
	_ = json.Unmarshal(b, &out)
	return out
}

func TestC4LegacyOmission(t *testing.T) {
	for _, v := range []any{Spec{}, Mission{}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "c4") {
			t.Fatal("legacy representation changed", string(b))
		}
	}
	if err := ValidateC4Mission(Mission{}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateC4Transition(Mission{}, Mission{Title: "legacy edit"}); err != nil {
		t.Fatal(err)
	}
}

func TestC4ProfileAndPolicyFailClosed(t *testing.T) {
	m := c4Fixture()
	if err := ValidateC4Mission(m); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*C4Profile){
		"version": func(p *C4Profile) { p.SchemaVersion = 2 }, "id": func(p *C4Profile) { p.ID = "c4_go_v2" },
		"mode": func(p *C4Profile) { p.Mode = "remote" }, "partial": func(p *C4Profile) { p.Verifier = "" },
		"tag": func(p *C4Profile) { p.ImageID = "golang:latest" }, "platform": func(p *C4Profile) { p.Platform = "linux/amd64" },
		"path": func(p *C4Profile) { p.DockerPath = "docker" }, "unclean path": func(p *C4Profile) { p.DockerPath = "/bin/../docker" },
		"tcp": func(p *C4Profile) { p.Endpoint = "tcp://localhost:2375" }, "ssh": func(p *C4Profile) { p.Endpoint = "ssh://local" },
		"host": func(p *C4Profile) { p.Endpoint = "unix://host/docker.sock" }, "query": func(p *C4Profile) { p.Endpoint = "unix:///docker.sock?x=1" },
		"verifier": func(p *C4Profile) { p.Verifier = "/repo/test.sh" },
	} {
		t.Run(name, func(t *testing.T) {
			n := cloneC4(m)
			change(n.Spec.C4)
			if ValidateC4Mission(n) == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
	p := FixedC4Policy()
	if p.CPUs != 1 || p.MemoryBytes != 1<<30 || p.PidsLimit != 128 || p.TimeoutSeconds != 120 || p.OutputLimitBytes != 64<<10 || p.UID != 65534 || p.GID != 65534 {
		t.Fatal("wrong fixed resource policy", p)
	}
	if !p.ReadOnlyRoot || !p.ReadOnlyCandidate || !p.NoNewPrivileges || p.Network != "none" || p.Pull != "never" || p.Logging != "none" || !p.DisableHealthcheck || !reflect.DeepEqual(p.DropCapabilities, []string{"ALL"}) || len(p.Ports) != 0 {
		t.Fatal("unsafe policy", p)
	}
	want := []C4Tmpfs{{Path: "/tmp", SizeBytes: 64 << 20, Inodes: 16384, Exec: false, Nosuid: true, Nodev: true}, {Path: "/foundry-build", SizeBytes: 256 << 20, Inodes: 16384, Exec: true, Nosuid: true, Nodev: true}}
	if !reflect.DeepEqual(p.Tmpfs, want) {
		t.Fatal("wrong tmpfs policy", p.Tmpfs)
	}
	if !reflect.DeepEqual(p.GoCommand, []string{"go", "test", "-json", "-count=1", "-timeout=90s", "./..."}) {
		t.Fatal("wrong verifier test command", p.GoCommand)
	}
	if _, ok := p.Environment["HOME"]; ok {
		t.Fatal("policy must not repurpose HOME")
	}
	if p.GoVersion != "1.27.3" {
		t.Fatal("unbound verifier toolchain", p.GoVersion)
	}
	for k, v := range map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "CGO_ENABLED": "0", "GOCACHE": "/foundry-build/cache", "GOTMPDIR": "/foundry-build/tmp"} {
		if p.Environment[k] != v {
			t.Fatal(k, p.Environment)
		}
	}
	p.Environment["GOPROXY"] = "evil"
	p.Tmpfs[0].Exec = true
	if FixedC4Policy().Environment["GOPROXY"] != "off" || FixedC4Policy().Tmpfs[0].Exec {
		t.Fatal("shared mutable policy")
	}
	n := cloneC4(m)
	n.Spec.C4.Mode = "docker"
	n.C4Operations = nil
	if err := ValidateC4Mission(n); err != nil {
		t.Fatal("representation must permit closed docker profile", err)
	}
}

func TestC4DigestsFreezeInputsNotLifecycle(t *testing.T) {
	m := c4Fixture()
	o := m.C4Operations[0]
	n := cloneC4(m)
	n.Spec.C4.Endpoint = "unix:///other.sock"
	if n.Spec.C4.ProfileDigest() == m.Spec.C4.ProfileDigest() {
		t.Fatal("profile drift not bound")
	}
	for name, mutate := range map[string]func(*C4Operation){"base": func(o *C4Operation) { o.BaseDigest = strings.Repeat("d", 64) }, "epoch": func(o *C4Operation) { o.Epoch++ }, "profile": func(o *C4Operation) { o.ProfileDigest = strings.Repeat("e", 64) }, "owner": func(o *C4Operation) { o.Owner = "other" }} {
		t.Run(name, func(t *testing.T) {
			n := o
			mutate(&n)
			if C4MaterialDigest(n) == o.MaterialDigest {
				t.Fatal("input drift not bound")
			}
		})
	}
	o.State = "approved"
	o.Approval = &C4Approval{Digest: o.MaterialDigest, Revision: 10, At: c4Time}
	o.Cleanup = []C4Cleanup{{State: "failed", Owner: o.Owner, ContainerID: "container", Revision: 10, At: c4Time}}
	if C4MaterialDigest(o) != m.C4Operations[0].MaterialDigest {
		t.Fatal("lifecycle changed frozen material")
	}
}

func c4Advance(m Mission, stage string) Mission {
	n := cloneC4(m)
	o := &n.C4Operations[0]
	n.Revision++
	switch stage {
	case "approved":
		o.Approval = &C4Approval{Digest: o.MaterialDigest, Revision: m.Revision, At: c4Time}
	case "dispatched":
		o.Dispatch = &C4Fact{Revision: m.Revision, At: c4Time}
	case "created":
		o.ContainerID = "container-1"
		o.Created = &C4Fact{Revision: m.Revision, At: c4Time}
	case "started":
		o.Started = &C4Fact{Revision: m.Revision, At: c4Time}
	case "finished":
		code := 0
		o.Result = &C4Result{Class: "passed", ExitCode: &code, Tests: 1, Revision: m.Revision, At: c4Time, Output: "ok"}
	}
	o.State = stage
	return n
}

func TestC4LifecycleAndCleanupAreMonotonic(t *testing.T) {
	m := c4Fixture()
	for _, stage := range []string{"approved", "dispatched", "created", "started", "finished"} {
		n := c4Advance(m, stage)
		if err := ValidateC4Transition(m, n); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		if n.C4Operations[0].Passed() {
			t.Fatal("pass before confirmed cleanup")
		}
		m = n
	}
	n := cloneC4(m)
	n.C4Operations[0].Cleanup = []C4Cleanup{{State: "confirmed", ContainerID: "container-1", Owner: "owner-1", Revision: m.Revision, At: c4Time}}
	if err := ValidateC4Transition(m, n); err != nil {
		t.Fatal(err)
	}
	if !n.C4Operations[0].Passed() || n.C4Operations[0].CleanupState() != "confirmed" {
		t.Fatal("clean passing result not recognized")
	}
	for name, mutate := range map[string]func(*Mission){
		"drop trace": func(m *Mission) { m.C4Operations = nil }, "downgrade": func(m *Mission) { m.Spec.C4 = nil; m.C4Operations = nil },
		"rewrite result": func(m *Mission) { m.C4Operations[0].Result.Output = "rewritten" }, "rewrite outcome": func(m *Mission) { m.C4Operations[0].Result.Class = "test_failed" },
		"drop approval": func(m *Mission) { m.C4Operations[0].Approval = nil }, "rewrite dispatch": func(m *Mission) { m.C4Operations[0].Dispatch.Revision++ },
		"drop cleanup": func(m *Mission) { m.C4Operations[0].Cleanup = nil }, "foreign cleanup": func(m *Mission) { m.C4Operations[0].Cleanup[0].Owner = "foreign" },
		"material": func(m *Mission) {
			o := &m.C4Operations[0]
			o.BaseDigest = strings.Repeat("f", 64)
			o.MaterialDigest = C4MaterialDigest(*o)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := cloneC4(n)
			mutate(&bad)
			if ValidateC4Transition(n, bad) == nil {
				t.Fatal("rewrite accepted")
			}
		})
	}
	replan := cloneC4(n)
	replan.Epoch++
	replan.Plan = &Plan{Revision: 4}
	replan.Approvals = nil
	if err := ValidateC4Transition(n, replan); err != nil {
		t.Fatal("historical immutable records must survive replan", err)
	}
}

func TestC4UnknownCanOnlyGainExactCleanup(t *testing.T) {
	m := c4Advance(c4Advance(c4Fixture(), "approved"), "dispatched")
	n := cloneC4(m)
	n.C4Operations[0].State = "finished"
	n.C4Operations[0].Result = &C4Result{Class: "outcome_unknown", At: c4Time, Revision: m.Revision}
	if err := ValidateC4Transition(m, n); err != nil {
		t.Fatal(err)
	}
	clean := cloneC4(n)
	clean.C4Operations[0].Cleanup = []C4Cleanup{{State: "failed", ContainerID: "container-1", Owner: "owner-1", Revision: n.Revision, At: c4Time}, {State: "confirmed", ContainerID: "container-1", Owner: "owner-1", Revision: n.Revision, At: c4Time}}
	if err := ValidateC4Transition(n, clean); err != nil {
		t.Fatal(err)
	}
	if clean.C4Operations[0].Passed() {
		t.Fatal("cleanup manufactured pass")
	}
	bad := cloneC4(clean)
	bad.C4Operations[0].ContainerID = "container-1"
	bad.C4Operations[0].Created = &C4Fact{Revision: bad.Revision, At: c4Time}
	if ValidateC4Transition(clean, bad) == nil {
		t.Fatal("terminal gained execution stage")
	}
}

func TestC4RejectsInvalidFactsAndUnsafeDispatch(t *testing.T) {
	for name, mutate := range map[string]func(*Mission){
		"prepared approval": func(m *Mission) {
			m.C4Operations[0].Approval = &C4Approval{Digest: m.C4Operations[0].MaterialDigest, Revision: 1, At: c4Time}
		},
		"unsupported record": func(m *Mission) { m.C4Operations[0].SchemaVersion = 9 }, "orphan": func(m *Mission) { m.Spec.C4 = nil },
		"duplicate":      func(m *Mission) { m.C4Operations = append(m.C4Operations, m.C4Operations[0]) },
		"material drift": func(m *Mission) { m.C4Operations[0].BaseDigest = strings.Repeat("f", 64) },
		"skip dispatch":  func(m *Mission) { *m = c4Advance(*m, "created") }, "skip start": func(m *Mission) { *m = c4Advance(*m, "finished") },
		"bad timestamp": func(m *Mission) { m.C4Operations[0].PreparedAt = "yesterday" },
	} {
		t.Run(name, func(t *testing.T) {
			m := c4Fixture()
			mutate(&m)
			if ValidateC4Mission(m) == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
	m := c4Fixture()
	n := c4Advance(m, "approved")
	n.Epoch++
	if ValidateC4Transition(m, n) == nil {
		t.Fatal("stale approval accepted")
	}
	m = c4Advance(m, "approved")
	n = c4Advance(m, "dispatched")
	n.BaseCommit = strings.Repeat("f", 40)
	if ValidateC4Transition(m, n) == nil {
		t.Fatal("changed base dispatched")
	}
	m = c4Advance(c4Advance(c4Advance(m, "dispatched"), "created"), "started")
	n = c4Advance(m, "finished")
	for _, kind := range []string{"zero_tests", "exit", "output", "control", "future_revision"} {
		t.Run(kind, func(t *testing.T) {
			bad := cloneC4(n)
			r := bad.C4Operations[0].Result
			switch kind {
			case "zero_tests":
				r.Tests = 0
			case "exit":
				*r.ExitCode = 1
			case "output":
				r.Output = strings.Repeat("a", 65537)
			case "control":
				r.Output = "bad\x1b[31m"
			case "future_revision":
				r.Revision = 1000
			}
			if ValidateC4Mission(bad) == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestC4CandidateRequiresExistingApprovalAndCurrentGeneration(t *testing.T) {
	m := c4Fixture()
	m.C4Operations = nil
	m.Plan = &Plan{Revision: 3}
	m.CandidateGeneration = &GenerationRef{ID: "candidate", Digest: strings.Repeat("d", 64)}
	n := cloneC4(m)
	o := c4Fixture().C4Operations[0]
	o.ID = "candidate-1"
	o.ContainerName = "foundry-c4-candidate-1"
	o.Kind = "candidate"
	o.PlanRevision = 3
	o.CandidateGeneration = n.CandidateGeneration
	o.CandidateDigest = n.CandidateGeneration.Digest
	o.MaterialDigest = C4MaterialDigest(o)
	n.C4Operations = []C4Operation{o}
	if ValidateC4Transition(m, n) == nil {
		t.Fatal("candidate without execute approval")
	}
	a := Approval{Kind: "execute", PlanRevision: 3, Digest: strings.Repeat("e", 64), Revision: 9, CreatedAt: c4Time}
	m.Approvals = []Approval{a}
	n.Approvals = []Approval{a}
	if err := ValidateC4Transition(m, n); err != nil {
		t.Fatal(err)
	}
	approved := cloneC4(n)
	approved.C4Operations[0].State = "approved"
	approved.C4Operations[0].Approval = &C4Approval{Digest: a.Digest, Revision: a.Revision, At: a.CreatedAt}
	if err := ValidateC4Transition(n, approved); err != nil {
		t.Fatal(err)
	}
	dispatched := c4Advance(approved, "dispatched")
	dispatched.CandidateGeneration = &GenerationRef{ID: "other", Digest: strings.Repeat("f", 64)}
	if ValidateC4Transition(approved, dispatched) == nil {
		t.Fatal("stale candidate dispatched")
	}
	legacy := Mission{}
	if ValidateC4Transition(legacy, c4Fixture()) == nil {
		t.Fatal("legacy upgraded silently")
	}
}

func TestC4RejectsDuplicateMaterialAndTraceOverflow(t *testing.T) {
	m := c4Fixture()
	empty := cloneC4(m)
	empty.C4Operations = nil
	n := cloneC4(m)
	other := n.C4Operations[0]
	other.ID = "different"
	other.ContainerName = "different"
	other.MaterialDigest = C4MaterialDigest(other)
	n.C4Operations = append(n.C4Operations, other)
	if ValidateC4Transition(empty, n) == nil {
		t.Fatal("same material appended twice in one transaction")
	}
	m.Epoch = 65
	m.C4Operations = nil
	for i := 0; i < 65; i++ {
		o := c4Fixture().C4Operations[0]
		o.ID = fmt.Sprintf("attempt-%d", i)
		o.ContainerName = o.ID
		o.Epoch = int64(i)
		o.MaterialDigest = C4MaterialDigest(o)
		m.C4Operations = append(m.C4Operations, o)
	}
	if ValidateC4Mission(m) == nil {
		t.Fatal("unbounded operation history")
	}
}

func TestC4PassedNeverPromotesForeignCleanup(t *testing.T) {
	m := c4Fixture()
	for _, stage := range []string{"approved", "dispatched", "created", "started", "finished"} {
		m = c4Advance(m, stage)
	}
	o := m.C4Operations[0]
	o.Cleanup = []C4Cleanup{{State: "confirmed", ContainerID: o.ContainerID, Owner: "foreign", Revision: m.Revision, At: c4Time}}
	if o.Passed() {
		t.Fatal("foreign cleanup reported as pass")
	}
}
