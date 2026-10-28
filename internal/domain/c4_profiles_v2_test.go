package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Literal tuples are independent expectations: changing an ID, language, runner
// or verifier in the closed contract must invalidate the profile.
var c4V2Tuples = []struct{ id, language, runner, verifier string }{
	{"c4_node_model_v2", "javascript", "node-test-v2", "/opt/foundry/verify-node-v2"},
	{"c4_typescript_model_v2", "typescript", "typescript-node-test-v2", "/opt/foundry/verify-typescript-v2"},
	{"c4_python_model_v2", "python", "python-unittest-v2", "/opt/foundry/verify-python-v2"},
}

func v2Profile(t *testing.T, index int, windows bool) C4Profile {
	t.Helper()
	p := vmProfile(t)
	tuple := c4V2Tuples[index]
	p.SchemaVersion = 2
	p.ID, p.Verifier = tuple.id, tuple.verifier
	p.EmbeddedVM.SchemaVersion = 2
	p.EmbeddedVM.Protocol = "foundry-vm-v2"
	p.EmbeddedVM.PolicyVersion = "multilang-vm-v2"
	if windows {
		p.Platform = "linux/amd64"
		p.EmbeddedVM.HostPlatform = "windows/amd64"
		p.EmbeddedVM.GuestPlatform = "linux/amd64"
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["v2"] = map[string]any{
		"language": tuple.language, "runner_id": tuple.runner,
		"runtime_inventory_digest":  strings.Repeat("b", 64),
		"dependency_bundle_digest":  strings.Repeat("c", 64),
		"protected_inputs_digest":   strings.Repeat("d", 64),
		"execution_contract_digest": strings.Repeat("e", 64),
	}
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestC4V2AdmitsOnlyClosedLanguageProfiles(t *testing.T) {
	for i, tuple := range c4V2Tuples {
		for _, windows := range []bool{false, true} {
			p := v2Profile(t, i, windows)
			t.Run(tuple.language+"/"+p.Platform, func(t *testing.T) {
				if err := p.Validate(); err != nil {
					t.Fatal("valid v2 profile rejected:", err)
				}
				if err := p.EmbeddedVM.Validate(); err != nil {
					t.Fatal("valid v2 identity rejected:", err)
				}
			})
		}
	}
}

func TestC4V2RejectsInvalidBindingsWithoutNormalization(t *testing.T) {
	changes := map[string]func(*C4Profile){
		"schema1":          func(p *C4Profile) { p.SchemaVersion = 1 },
		"unknown schema":   func(p *C4Profile) { p.SchemaVersion = 3 },
		"control0":         func(p *C4Profile) { p.ControlVersion = 0 },
		"control2":         func(p *C4Profile) { p.ControlVersion = 2 },
		"unknown id":       func(p *C4Profile) { p.ID = "c4_ruby_model_v2" },
		"go id":            func(p *C4Profile) { p.ID = "c4_go_model_v1" },
		"missing v2":       func(p *C4Profile) { p.V2 = nil },
		"missing identity": func(p *C4Profile) { p.EmbeddedVM = nil },
		"docker":           func(p *C4Profile) { p.Mode = "docker" },
		"synthetic":        func(p *C4Profile) { p.Mode = "synthetic" },
		"unknown mode":     func(p *C4Profile) { p.Mode = "remote" },
		"image":            func(p *C4Profile) { p.ImageID = "sha256:" + strings.Repeat("a", 64) },
		"docker path":      func(p *C4Profile) { p.DockerPath = "/bin/docker" },
		"endpoint":         func(p *C4Profile) { p.Endpoint = "unix:///docker.sock" },
		"platform":         func(p *C4Profile) { p.Platform = "linux/s390x" },
		"guest mismatch":   func(p *C4Profile) { p.Platform = "linux/amd64" },
		"language":         func(p *C4Profile) { p.V2.Language = "ruby" },
		"missing language": func(p *C4Profile) { p.V2.Language = "" },
		"runner":           func(p *C4Profile) { p.V2.RunnerID = "caller-runner" },
		"missing runner":   func(p *C4Profile) { p.V2.RunnerID = "" },
		"verifier":         func(p *C4Profile) { p.Verifier = "/opt/foundry/anything" },
		"v1 identity":      func(p *C4Profile) { p.EmbeddedVM = vmProfile(t).EmbeddedVM },
	}
	for i, tuple := range c4V2Tuples {
		for name, change := range changes {
			t.Run(tuple.language+"/"+name, func(t *testing.T) {
				p := v2Profile(t, i, false)
				change(&p)
				before, _ := json.Marshal(p)
				if p.Validate() == nil {
					t.Fatal("invalid profile accepted")
				}
				after, _ := json.Marshal(p)
				if string(before) != string(after) {
					t.Fatal("validation normalized invalid material")
				}
			})
		}
		for j, other := range c4V2Tuples {
			if j == i {
				continue
			}
			for _, field := range []string{"language", "runner", "verifier"} {
				t.Run(tuple.language+"/mixed/"+other.language+"/"+field, func(t *testing.T) {
					p := v2Profile(t, i, false)
					switch field {
					case "language":
						p.V2.Language = other.language
					case "runner":
						p.V2.RunnerID = other.runner
					case "verifier":
						p.Verifier = other.verifier
					}
					if p.Validate() == nil {
						t.Fatal("mixed tuple accepted")
					}
				})
			}
		}
	}
	for _, p := range []C4Profile{*c4Fixture().Spec.C4, vmProfile(t)} {
		p.V2 = v2Profile(t, 0, false).V2
		if p.Validate() == nil {
			t.Fatal("v1 accepted extra v2 material")
		}
	}
	p := vmProfile(t)
	p.EmbeddedVM = v2Profile(t, 0, false).EmbeddedVM
	if p.Validate() == nil {
		t.Fatal("v1 profile accepted v2 identity")
	}
}

func v2DigestFields(p *C4Profile) map[string]*string {
	return map[string]*string{
		"runtime_inventory":  &p.V2.RuntimeInventoryDigest,
		"dependency_bundle":  &p.V2.DependencyBundleDigest,
		"protected_inputs":   &p.V2.ProtectedInputsDigest,
		"execution_contract": &p.V2.ExecutionContractDigest,
		"manifest":           &p.EmbeddedVM.ManifestDigest,
		"runtime":            &p.EmbeddedVM.RuntimeDigest,
		"kernel":             &p.EmbeddedVM.KernelDigest,
		"rootfs":             &p.EmbeddedVM.RootFSDigest,
		"verifier":           &p.EmbeddedVM.VerifierDigest,
	}
}

func TestC4V2RequiresEveryFullLowercaseDigest(t *testing.T) {
	fixture := v2Profile(t, 0, false)
	for name := range v2DigestFields(&fixture) {
		for _, bad := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64), "sha256:" + strings.Repeat("a", 64), strings.Repeat("a", 63) + "\n"} {
			t.Run(name+"/"+bad, func(t *testing.T) {
				p := v2Profile(t, 0, false)
				*v2DigestFields(&p)[name] = bad
				if p.Validate() == nil {
					t.Fatal("invalid digest accepted")
				}
			})
		}
	}
}

func TestC4VMIdentityVersionsStayClosedAndComparable(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, windows := range []bool{false, true} {
			v := *v2Profile(t, 0, windows).EmbeddedVM
			if version == 1 {
				v.SchemaVersion, v.Protocol, v.PolicyVersion = 1, "foundry-vm-v1", "go-vm-v1"
			}
			if err := v.Validate(); err != nil {
				t.Fatal(err)
			}
			_ = map[C4VMIdentity]bool{v: true}
			for name, change := range map[string]func(*C4VMIdentity){
				"schema": func(v *C4VMIdentity) { v.SchemaVersion = 0 },
				"future": func(v *C4VMIdentity) { v.SchemaVersion = 3 },
				"protocol": func(v *C4VMIdentity) {
					if version == 1 {
						v.Protocol = "foundry-vm-v2"
					} else {
						v.Protocol = "foundry-vm-v1"
					}
				},
				"policy": func(v *C4VMIdentity) {
					if version == 1 {
						v.PolicyVersion = "multilang-vm-v2"
					} else {
						v.PolicyVersion = "go-vm-v1"
					}
				},
				"host":  func(v *C4VMIdentity) { v.HostPlatform = "linux/arm64" },
				"guest": func(v *C4VMIdentity) { v.GuestPlatform = "linux/s390x" },
				"cross architecture": func(v *C4VMIdentity) {
					if windows {
						v.GuestPlatform = "linux/arm64"
					} else {
						v.GuestPlatform = "linux/amd64"
					}
				},
			} {
				t.Run(v.Protocol+"/"+v.HostPlatform+"/"+name, func(t *testing.T) {
					bad := v
					change(&bad)
					if bad.Validate() == nil {
						t.Fatal("invalid identity accepted")
					}
				})
			}
		}
	}
}

func TestC4V2PolicyIsFixedAndIndependent(t *testing.T) {
	for _, tuple := range c4V2Tuples {
		p, err := FixedC4VMPolicyV2(tuple.id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Version != "multilang-vm-v2" || p.GuestMemoryBytes != 2<<30 || p.LifetimeSeconds != 180 || p.HostShares {
			t.Fatal("wrong VM policy", p)
		}
		v := p.Test
		if v.CPUs != 1 || v.MemoryBytes != 1<<30 || v.PidsLimit != 128 || v.TimeoutSeconds != 120 || v.OutputLimitBytes != 64<<10 || v.UID != 65534 || v.GID != 65534 {
			t.Fatal("wrong fixed limits", v)
		}
		if !v.ReadOnlyRoot || !v.ReadOnlySource || !v.ReadOnlyDependencies || !v.NoNewPrivileges || v.Network != "none" || v.Pull != "never" || v.Logging != "none" || !v.DisableHealthcheck || !reflect.DeepEqual(v.DropCapabilities, []string{"ALL"}) || !reflect.DeepEqual(v.Ports, []string{}) {
			t.Fatal("unsafe isolation policy", v)
		}
		if v.RunnerID != tuple.runner || v.MaxReportBytes != 2<<20 || v.MaxCases != 4096 || v.MaxSourceFiles != 500 || v.MaxSourceBytes != 10<<20 || v.MaxDependencyFiles != 10000 || v.MaxDependencyBytes != 64<<20 || v.MaxDependencyFileBytes != 8<<20 {
			t.Fatal("wrong runner/input bounds", v)
		}
		wantTmpfs := []C4Tmpfs{{Path: "/tmp", SizeBytes: 64 << 20, Inodes: 16384, Nosuid: true, Nodev: true}, {Path: "/foundry-build", SizeBytes: 256 << 20, Inodes: 16384, Exec: true, Nosuid: true, Nodev: true}}
		if !reflect.DeepEqual(v.Tmpfs, wantTmpfs) {
			t.Fatal("wrong writable mounts", v.Tmpfs)
		}
		raw, _ := json.Marshal(p)
		for _, forbidden := range []string{"go_command", "go_version", "environment", "command"} {
			if strings.Contains(string(raw), `"`+forbidden+`"`) {
				t.Fatal("caller execution configuration in v2 policy", string(raw))
			}
		}
		p.Test.DropCapabilities[0] = "SYS_ADMIN"
		p.Test.Ports = append(p.Test.Ports, "80")
		p.Test.Tmpfs[0].Exec = true
		again, err := FixedC4VMPolicyV2(tuple.id)
		if err != nil {
			t.Fatal(err)
		}
		if again.Test.DropCapabilities[0] != "ALL" || len(again.Test.Ports) != 0 || again.Test.Tmpfs[0].Exec {
			t.Fatal("shared mutable policy")
		}
	}
	for _, id := range []string{"", "c4_go_model_v1", "c4_go_v1", "node-test-v2", "c4_ruby_model_v2"} {
		if _, err := FixedC4VMPolicyV2(id); err == nil {
			t.Fatal("unknown policy accepted", id)
		}
	}
}

func TestC4V2DigestBindsAllMaterialUnderNewDomain(t *testing.T) {
	for i := range c4V2Tuples {
		p := v2Profile(t, i, false)
		policy, err := FixedC4VMPolicyV2(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		// Independent envelope and domain verify dispatch, including canonical field order.
		profileJSON, _ := json.Marshal(p)
		policyJSON, _ := json.Marshal(policy)
		h := sha256.Sum256([]byte("foundry:c4:v2:vm-profile\n" + `{"profile":` + string(profileJSON) + `,"policy":` + string(policyJSON) + `}`))
		if p.ProfileDigest() != hex.EncodeToString(h[:]) {
			t.Fatal("wrong v2 digest domain/envelope")
		}
		original := p.ProfileDigest()
		for name := range v2DigestFields(&p) {
			changed := v2Profile(t, i, false)
			*v2DigestFields(&changed)[name] = strings.Repeat("f", 64)
			if err := changed.Validate(); err != nil {
				t.Fatal(name, err)
			}
			if changed.ProfileDigest() == original {
				t.Fatal("unbound material", name)
			}
		}
		if v2Profile(t, i, true).ProfileDigest() == original {
			t.Fatal("architecture unbound")
		}
		for name, change := range map[string]func(*C4Profile){
			"id":       func(p *C4Profile) { p.ID = "other" },
			"language": func(p *C4Profile) { p.V2.Language = "other" },
			"runner":   func(p *C4Profile) { p.V2.RunnerID = "other" },
			"verifier": func(p *C4Profile) { p.Verifier = "/opt/foundry/other" },
		} {
			changed := v2Profile(t, i, false)
			change(&changed)
			if changed.ProfileDigest() == original {
				t.Fatal("unbound tuple field", name)
			}
		}
	}
	p := v2Profile(t, 0, false)
	raw, _ := json.Marshal(p.V2)
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	if len(fields) != 6 {
		t.Fatal("unexpected v2 shape", string(raw))
	}
	for _, name := range []string{"language", "runner_id", "runtime_inventory_digest", "dependency_bundle_digest", "protected_inputs_digest", "execution_contract_digest"} {
		if _, ok := fields[name]; !ok {
			t.Fatal("missing v2 key", name)
		}
	}
	empty, _ := json.Marshal(C4ProfileV2{})
	if strings.Count(string(empty), `":""`) != 6 {
		t.Fatal("v2 bindings must not be omitted", string(empty))
	}
}

func TestC4ProfileHelpersValidateAndUseClosedDispatch(t *testing.T) {
	profiles := []C4Profile{vmProfile(t)}
	// Historical model fixtures use synthetic mode with the model profile ID.
	modelSynthetic := *c4Fixture().Spec.C4
	modelSynthetic.ID, modelSynthetic.ControlVersion = "c4_go_model_v1", 1
	modelDocker := modelSynthetic
	modelDocker.Mode = "docker"
	profiles = append(profiles, modelSynthetic, modelDocker)
	for i := range c4V2Tuples {
		profiles = append(profiles, v2Profile(t, i, false))
	}
	for _, p := range profiles {
		if !p.IsModelBacked() {
			t.Fatal("valid model profile not recognized", p.ID)
		}
		commands, err := p.AcceptanceCommands()
		if err != nil || !reflect.DeepEqual(commands, []string{p.Verifier}) {
			t.Fatal("wrong acceptance", commands, err)
		}
		commands[0] = "/opt/foundry/foreign"
		again, err := p.AcceptanceCommands()
		if err != nil || again[0] != p.Verifier {
			t.Fatal("shared acceptance storage")
		}
		p.Verifier = "/opt/foundry/foreign"
		if p.IsModelBacked() {
			t.Fatal("invalid model profile recognized")
		}
		if _, err := p.AcceptanceCommands(); err == nil {
			t.Fatal("arbitrary verifier accepted")
		}
	}
	synthetic := *c4Fixture().Spec.C4
	if synthetic.IsModelBacked() {
		t.Fatal("synthetic fixture recognized as model")
	}
	if commands, err := synthetic.AcceptanceCommands(); err != nil || !reflect.DeepEqual(commands, []string{synthetic.Verifier}) {
		t.Fatal("valid synthetic acceptance rejected", commands, err)
	}
	docker := synthetic
	docker.Mode = "docker"
	if docker.IsModelBacked() {
		t.Fatal("non-model docker recognized")
	}
	unknown := C4Profile{ID: "c4_unknown_model_v2", Verifier: "/opt/foundry/foreign"}
	if unknown.IsModelBacked() {
		t.Fatal("unknown model profile recognized")
	}
	if _, err := unknown.AcceptanceCommands(); err == nil {
		t.Fatal("unknown acceptance accepted")
	}
}

func TestC4V2UsesExistingControlAndOperationLifecycle(t *testing.T) {
	for i := range c4V2Tuples {
		m := readyC4Fixture()
		p := v2Profile(t, i, false)
		m.Spec.C4 = &p
		c := &m.C4Controls[0]
		c.SourceKind = "embedded_vm_c4_control"
		c.ProfileDigest = p.ProfileDigest()
		c.MaterialDigest = C4ControlDigest(*c)
		c.Approval.Digest = c.MaterialDigest
		o := c4Fixture().C4Operations[0]
		o.ProfileDigest = p.ProfileDigest()
		o.ReadinessID, o.ReadinessDigest = c.ID, c.MaterialDigest
		o.MaterialDigest = C4MaterialDigest(o)
		m.C4Operations = []C4Operation{o}
		if err := ValidateC4Controls(m); err != nil {
			t.Fatal("generic control rejected v2", err)
		}
		if err := ValidateC4Mission(m); err != nil {
			t.Fatal("generic mission rejected v2", err)
		}
		if err := ValidateC4Operation(p, o); err != nil {
			t.Fatal("generic operation rejected v2", err)
		}
		before := cloneC4(m)
		after := cloneC4(m)
		after.C4Operations[0].Approval = &C4Approval{Digest: o.MaterialDigest, Revision: m.Revision, At: c4Time}
		after.C4Operations[0].State = "approved"
		if err := ValidateC4Transition(before, after); err != nil {
			t.Fatal("generic transition rejected v2", err)
		}
	}
}
