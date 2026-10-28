package domain

import (
	"encoding/json"
	"testing"
)

// These fixtures are synthetic and carry no task or installed-runtime data.
func c4V1GoldenValues(t *testing.T) map[string]string {
	t.Helper()
	values := map[string]string{}
	put := func(name string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		values[name] = string(raw)
	}
	synthetic := *c4Fixture().Spec.C4
	docker := synthetic
	docker.Mode = "docker"
	modelVM := vmProfile(t)
	for name, p := range map[string]C4Profile{"synthetic": synthetic, "docker": docker, "model_vm": modelVM} {
		if err := p.Validate(); err != nil {
			t.Fatal(name, err)
		}
		put(name+"_profile_json", p)
		values[name+"_profile_digest"] = p.ProfileDigest()
	}
	put("identity_json", *modelVM.EmbeddedVM)
	put("policy_json", FixedC4Policy())
	put("vm_policy_json", FixedC4VMPolicy())
	prepared := c4Fixture()
	approved := c4Advance(prepared, "approved")
	if err := ValidateC4Mission(approved); err != nil {
		t.Fatal(err)
	}
	put("material_json", prepared.C4Operations[0])
	values["material_digest"] = C4MaterialDigest(prepared.C4Operations[0])
	put("approval_json", approved.C4Operations[0].Approval)
	ready := readyC4Fixture()
	if err := ValidateC4Mission(ready); err != nil {
		t.Fatal(err)
	}
	put("control_json", ready.C4Controls[0])
	values["control_digest"] = C4ControlDigest(ready.C4Controls[0])
	return values
}

// Captured before production edits from commit 28841e1. Never regenerate these
// expectations from changed production code: they freeze historical authority.
const c4V1GoldenJSON = `{
  "approval_json": "{\"digest\":\"10ccd472d8f1b30136555149045e6ef047af9ee75b446d981ec938f7d3232090\",\"revision\":10,\"at\":\"2026-10-21T00:00:00Z\"}",
  "control_digest": "f1b9a13791e11759cdbd0336bfb23031d8b8c7523f2cfb1fa25b7b8f293781e3",
  "control_json": "{\"schema_version\":1,\"id\":\"ready-1\",\"kind\":\"readiness\",\"mission_id\":\"c4\",\"epoch\":1,\"plan_revision\":0,\"base_commit\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"base_digest\":\"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"profile_digest\":\"7e6920335e49b0c7c197d4cec168581f143fe92fe250bced3c3afdaa620ab75e\",\"activation_digest\":\"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\",\"activation_expires_at\":\"2026-10-21T01:00:00Z\",\"source_kind\":\"synthetic_c4_control\",\"prepared_revision\":1,\"prepared_at\":\"2026-10-21T00:00:00Z\",\"expires_at\":\"2026-10-21T00:10:00Z\",\"material_digest\":\"f1b9a13791e11759cdbd0336bfb23031d8b8c7523f2cfb1fa25b7b8f293781e3\",\"state\":\"completed\",\"approval\":{\"digest\":\"f1b9a13791e11759cdbd0336bfb23031d8b8c7523f2cfb1fa25b7b8f293781e3\",\"revision\":2,\"at\":\"2026-10-21T00:00:00Z\"},\"dispatch\":{\"revision\":3,\"at\":\"2026-10-21T00:00:00Z\"},\"result\":{\"status\":\"ready\",\"revision\":4,\"at\":\"2026-10-21T00:00:00Z\",\"valid_until\":\"2026-10-21T00:10:00Z\",\"fingerprint\":\"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff\"}}",
  "docker_profile_digest": "239520e22f3378e901bce60696a42a2d2035284434605e4e3ac939f2924ede6f",
  "docker_profile_json": "{\"schema_version\":1,\"id\":\"c4_go_v1\",\"mode\":\"docker\",\"image_id\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"platform\":\"linux/arm64\",\"docker_path\":\"/synthetic/docker\",\"endpoint\":\"unix:///synthetic/docker.sock\",\"verifier\":\"/opt/foundry/verify-go-v1\"}",
  "identity_json": "{\"schema_version\":1,\"manifest_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"host_platform\":\"darwin/arm64\",\"guest_platform\":\"linux/arm64\",\"runtime_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"kernel_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"rootfs_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"verifier_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"protocol\":\"foundry-vm-v1\",\"policy_version\":\"go-vm-v1\"}",
  "material_digest": "10ccd472d8f1b30136555149045e6ef047af9ee75b446d981ec938f7d3232090",
  "material_json": "{\"schema_version\":1,\"id\":\"baseline-1\",\"kind\":\"baseline\",\"mission_id\":\"c4\",\"epoch\":1,\"plan_revision\":0,\"base_commit\":\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",\"base_digest\":\"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\",\"profile_digest\":\"ab6b8c461cf93abf0c7d220d7f243daea6a96c6a779303bb021e87b6b20395b6\",\"material_digest\":\"10ccd472d8f1b30136555149045e6ef047af9ee75b446d981ec938f7d3232090\",\"container_name\":\"foundry-c4-baseline-1\",\"owner\":\"owner-1\",\"state\":\"prepared\",\"prepared_at\":\"2026-10-21T00:00:00Z\"}",
  "model_vm_profile_digest": "3f27ca40620b7397f9e2973177cb7a0ff5fe5d0a41c4c07846eb4d6c6f4be86e",
  "model_vm_profile_json": "{\"schema_version\":1,\"control_version\":1,\"id\":\"c4_go_model_v1\",\"mode\":\"embedded_vm\",\"image_id\":\"\",\"platform\":\"linux/arm64\",\"docker_path\":\"\",\"endpoint\":\"\",\"verifier\":\"/opt/foundry/verify-go-v1\",\"embedded_vm\":{\"schema_version\":1,\"manifest_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"host_platform\":\"darwin/arm64\",\"guest_platform\":\"linux/arm64\",\"runtime_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"kernel_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"rootfs_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"verifier_digest\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"protocol\":\"foundry-vm-v1\",\"policy_version\":\"go-vm-v1\"}}",
  "policy_json": "{\"cpus\":1,\"memory_bytes\":1073741824,\"pids_limit\":128,\"timeout_seconds\":120,\"output_limit_bytes\":65536,\"uid\":65534,\"gid\":65534,\"read_only_root\":true,\"read_only_candidate\":true,\"no_new_privileges\":true,\"network\":\"none\",\"pull\":\"never\",\"logging\":\"none\",\"disable_healthcheck\":true,\"drop_capabilities\":[\"ALL\"],\"ports\":[],\"tmpfs\":[{\"path\":\"/tmp\",\"size_bytes\":67108864,\"inodes\":16384,\"exec\":false,\"nosuid\":true,\"nodev\":true},{\"path\":\"/foundry-build\",\"size_bytes\":268435456,\"inodes\":16384,\"exec\":true,\"nosuid\":true,\"nodev\":true}],\"go_command\":[\"go\",\"test\",\"-json\",\"-count=1\",\"-timeout=90s\",\"./...\"],\"go_version\":\"1.27.3\",\"environment\":{\"CGO_ENABLED\":\"0\",\"GOCACHE\":\"/foundry-build/cache\",\"GOENV\":\"off\",\"GOPATH\":\"/foundry-build/gopath\",\"GOPROXY\":\"off\",\"GOSUMDB\":\"off\",\"GOTMPDIR\":\"/foundry-build/tmp\",\"GOTOOLCHAIN\":\"local\",\"GOWORK\":\"off\",\"TMPDIR\":\"/tmp\"}}",
  "synthetic_profile_digest": "ab6b8c461cf93abf0c7d220d7f243daea6a96c6a779303bb021e87b6b20395b6",
  "synthetic_profile_json": "{\"schema_version\":1,\"id\":\"c4_go_v1\",\"mode\":\"synthetic\",\"image_id\":\"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"platform\":\"linux/arm64\",\"docker_path\":\"/synthetic/docker\",\"endpoint\":\"unix:///synthetic/docker.sock\",\"verifier\":\"/opt/foundry/verify-go-v1\"}",
  "vm_policy_json": "{\"version\":\"go-vm-v1\",\"guest_memory_bytes\":2147483648,\"lifetime_seconds\":180,\"host_shares\":false,\"test\":{\"cpus\":1,\"memory_bytes\":1073741824,\"pids_limit\":128,\"timeout_seconds\":120,\"output_limit_bytes\":65536,\"uid\":65534,\"gid\":65534,\"read_only_root\":true,\"read_only_candidate\":true,\"no_new_privileges\":true,\"network\":\"none\",\"pull\":\"never\",\"logging\":\"none\",\"disable_healthcheck\":true,\"drop_capabilities\":[\"ALL\"],\"ports\":[],\"tmpfs\":[{\"path\":\"/tmp\",\"size_bytes\":67108864,\"inodes\":16384,\"exec\":false,\"nosuid\":true,\"nodev\":true},{\"path\":\"/foundry-build\",\"size_bytes\":268435456,\"inodes\":16384,\"exec\":true,\"nosuid\":true,\"nodev\":true}],\"go_command\":[\"go\",\"test\",\"-json\",\"-count=1\",\"-timeout=90s\",\"./...\"],\"go_version\":\"1.27.3\",\"environment\":{\"CGO_ENABLED\":\"0\",\"GOCACHE\":\"/foundry-build/cache\",\"GOENV\":\"off\",\"GOPATH\":\"/foundry-build/gopath\",\"GOPROXY\":\"off\",\"GOSUMDB\":\"off\",\"GOTMPDIR\":\"/foundry-build/tmp\",\"GOTOOLCHAIN\":\"local\",\"GOWORK\":\"off\",\"TMPDIR\":\"/tmp\"}}}"
}`

func TestC4V1FrozenBytesAndDigests(t *testing.T) {
	var want map[string]string
	if err := json.Unmarshal([]byte(c4V1GoldenJSON), &want); err != nil {
		t.Fatal(err)
	}
	got := c4V1GoldenValues(t)
	if len(got) != len(want) {
		t.Fatal("golden set changed")
	}
	for name, expected := range want {
		if got[name] != expected {
			t.Errorf("%s changed:\ngot %s\nwant %s", name, got[name], expected)
		}
	}
}
