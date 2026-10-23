package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func vmProfile(t *testing.T) C4Profile {
	t.Helper()
	var p C4Profile
	raw := `{"schema_version":1,"control_version":1,"id":"c4_go_model_v1","mode":"embedded_vm","platform":"linux/arm64","verifier":"/opt/foundry/verify-go-v1","embedded_vm":{"schema_version":1,"manifest_digest":"DIGEST","host_platform":"darwin/arm64","guest_platform":"linux/arm64","runtime_digest":"DIGEST","kernel_digest":"DIGEST","rootfs_digest":"DIGEST","verifier_digest":"DIGEST","protocol":"foundry-vm-v1","policy_version":"go-vm-v1"}}`
	if err := json.Unmarshal([]byte(strings.ReplaceAll(raw, "DIGEST", strings.Repeat("a", 64))), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestC4VMProfileAdmission(t *testing.T) {
	p := vmProfile(t)
	if err := p.Validate(); err != nil {
		t.Fatalf("valid VM identity rejected: %v", err)
	}
	for _, field := range []string{"docker_path", "endpoint", "image_id", "host_platform", "protocol", "manifest_digest"} {
		raw, _ := json.Marshal(p)
		var v map[string]any
		_ = json.Unmarshal(raw, &v)
		switch field {
		case "docker_path", "endpoint", "image_id":
			v[field] = "foreign-authority"
		default:
			v["embedded_vm"].(map[string]any)[field] = "wrong"
		}
		raw, _ = json.Marshal(v)
		var bad C4Profile
		_ = json.Unmarshal(raw, &bad)
		if bad.Validate() == nil {
			t.Fatalf("accepted invalid %s", field)
		}
	}
}

func TestC4VMLegacySerializationDigest(t *testing.T) {
	p := *c4Fixture().Spec.C4
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "embedded_vm") {
		t.Fatal("legacy identity gained VM field")
	}
	if got := p.ProfileDigest(); got != "ab6b8c461cf93abf0c7d220d7f243daea6a96c6a779303bb021e87b6b20395b6" {
		t.Fatalf("legacy digest changed: %s", got)
	}
}

func TestC4VMControlSourceAndChangedIdentity(t *testing.T) {
	m := controlledC4Fixture()
	p := vmProfile(t)
	m.Spec.C4 = &p
	c := &m.C4Controls[0]
	c.SourceKind = "embedded_vm_c4_control"
	c.ProfileDigest = p.ProfileDigest()
	c.MaterialDigest = C4ControlDigest(*c)
	if err := ValidateC4Mission(m); err != nil {
		t.Fatal("VM control rejected", err)
	}
	for _, source := range []string{"unknown", "docker_c4_control", "synthetic_c4_control"} {
		bad := cloneC4(m)
		bad.C4Controls[0].SourceKind = source
		bad.C4Controls[0].MaterialDigest = C4ControlDigest(bad.C4Controls[0])
		if ValidateC4Mission(bad) == nil {
			t.Fatal("foreign source accepted", source)
		}
	}
	bad := cloneC4(m)
	bad.Spec.C4.EmbeddedVM.ManifestDigest = strings.Repeat("b", 64)
	if ValidateC4Mission(bad) == nil {
		t.Fatal("changed manifest reused control")
	}
}

func TestC4VMExecutionIdentityBindsBundle(t *testing.T) {
	p := vmProfile(t)
	if got := p.ExecutionIdentity(); got != "embedded-vm:sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("wrong execution identity %q", got)
	}
	if got := c4Fixture().Spec.C4.ExecutionIdentity(); got != "sha256:"+strings.Repeat("a", 64) {
		t.Fatal("legacy identity changed", got)
	}
}

func TestC4VMRejectsPIDLikeInstanceIdentity(t *testing.T) {
	p := vmProfile(t)
	o := c4Fixture().C4Operations[0]
	o.ProfileDigest = p.ProfileDigest()
	o.ReadinessID = "ready-1"
	o.ReadinessDigest = strings.Repeat("d", 64)
	o.MaterialDigest = C4MaterialDigest(o)
	o.Approval = &C4Approval{Digest: o.MaterialDigest, Revision: 1, At: c4Time}
	o.Dispatch = &C4Fact{Revision: 2, At: c4Time}
	o.Created = &C4Fact{Revision: 3, At: c4Time}
	o.State = "created"
	o.ContainerID = strings.Repeat("e", 64)
	if err := ValidateC4Operation(p, o); err != nil {
		t.Fatal(err)
	}
	o.ContainerID = "12345"
	if ValidateC4Operation(p, o) == nil {
		t.Fatal("PID-like VM handle accepted")
	}
}

func TestC4VMRejectsIncompleteStoredTestResult(t *testing.T) {
	p := vmProfile(t)
	o := c4Fixture().C4Operations[0]
	o.ProfileDigest = p.ProfileDigest()
	o.ReadinessID = "ready-1"
	o.ReadinessDigest = strings.Repeat("d", 64)
	o.MaterialDigest = C4MaterialDigest(o)
	o.Approval = &C4Approval{Digest: o.MaterialDigest, Revision: 1, At: c4Time}
	o.Dispatch = &C4Fact{Revision: 2, At: c4Time}
	o.Created = &C4Fact{Revision: 3, At: c4Time}
	o.Started = &C4Fact{Revision: 4, At: c4Time}
	o.State = "finished"
	o.ContainerID = strings.Repeat("e", 64)
	zero, one := 0, 1
	for _, r := range []C4Result{{Class: "passed", ExitCode: &zero, Tests: 1, Truncated: true, Revision: 5, At: c4Time}, {Class: "test_failed", ExitCode: &one, Tests: 0, Revision: 5, At: c4Time}} {
		o.Result = &r
		if ValidateC4Operation(p, o) == nil {
			t.Fatalf("incomplete stored VM result accepted: %+v", r)
		}
	}
}
