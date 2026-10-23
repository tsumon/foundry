package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Inert regular files prove Load/metadata have no guest or executable effects.
func metadataVMCapability(t *testing.T, router *model.Router) *engine.C4ControlledCapability {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("inert Apple Silicon manifest fixture")
	}
	bundle, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instances, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := vmruntime.Manifest{SchemaVersion: 1, HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1", Runtime: "runtime/foundry-vm-helper", Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest"}
	body := []byte("inert metadata fixture; must never execute")
	sum := sha256.Sum256(body)
	for _, p := range []string{manifest.Runtime, manifest.Kernel, manifest.RootFS, manifest.Verifier} {
		full := filepath.Join(bundle, p)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Assets = append(manifest.Assets, vmruntime.Asset{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))})
	}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	vm, err := vmruntime.Load(bundle, instances)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := engine.NewCodingVMCapability(router, vm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		entries, err := os.ReadDir(instances)
		if err != nil || len(entries) != 0 {
			t.Fatal("metadata allocated VM instance", entries, err)
		}
	})
	return cap
}

func assertVMStatusContract(t *testing.T, f *fixture, m domain.Mission) {
	t.Helper()
	var contract map[string]any
	raw, err := os.ReadFile("../../web/src/test/c4VMStatusContract.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	rr := f.request("GET", "/api/missions/"+m.ID+"/c4", "", true)
	var got map[string]any
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &got) != nil {
		t.Fatal(rr.Code, rr.Body.String())
	}
	for key, want := range contract {
		if key == "control" {
			for field, value := range want.(map[string]any) {
				if !reflect.DeepEqual(got[key].(map[string]any)[field], value) {
					t.Fatalf("VM status control.%s: got %v want %v", field, got[key], want)
				}
			}
		} else if !reflect.DeepEqual(got[key], want) {
			t.Fatalf("VM status %s: got %v want %v", key, got[key], want)
		}
	}
	server := f.handler.(*server)
	rr = f.request("GET", "/api/missions/"+m.ID, "", true)
	var mission struct {
		Mission struct {
			Binding *c4ProfileBinding `json:"c4_profile_binding"`
		} `json:"mission"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &mission) != nil || mission.Mission.Binding == nil || mission.Mission.Binding.Revision != m.Revision || mission.Mission.Binding.Digest != m.Spec.C4.ProfileDigest() || !reflect.DeepEqual(mission.Mission.Binding.Profile, *m.Spec.C4) {
		t.Fatal("account-bound mission projection", rr.Body.String())
	}
	server.c4ControlledOnly = true
	rr = f.request("GET", "/api/health", "", true)
	var health struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &health) != nil || !health.Capabilities["c4_real_execution"] || health.Capabilities["docker_verification"] {
		t.Fatal("VM health classification", rr.Body.String())
	}
	server.recoveryReadOnly = true
	rr = f.request("GET", "/api/missions/"+m.ID+"/c4", "", true)
	if json.Unmarshal(rr.Body.Bytes(), &got) != nil || got["real_execution"] != false || got["read_only"] != true {
		t.Fatal("recovery must close VM controls", rr.Body.String())
	}
}

func TestC4RealExecutionClassification(t *testing.T) {
	for _, tc := range []struct {
		source string
		real   bool
	}{
		{"embedded_vm_c4_control", true}, {"docker_c4_control", true},
		{"synthetic_c4_control", false}, {"future", false}, {"", false},
	} {
		if got := c4RealExecution(tc.source, false, false); got != tc.real {
			t.Fatalf("source=%s got=%v want=%v", tc.source, got, tc.real)
		}
		if c4RealExecution(tc.source, true, false) || c4RealExecution(tc.source, false, true) || c4RealExecution(tc.source, true, true) {
			t.Fatalf("expired/read-only source %s enabled", tc.source)
		}
	}
}

func TestC4VMProfileBindingProjection(t *testing.T) {
	f := newFixture(t)
	identity := domain.C4VMIdentity{SchemaVersion: 1, ManifestDigest: strings.Repeat("1", 64), HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", RuntimeDigest: strings.Repeat("2", 64), KernelDigest: strings.Repeat("3", 64), RootFSDigest: strings.Repeat("4", 64), VerifierDigest: strings.Repeat("5", 64), Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1"}
	p := domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_model_v1", Mode: "embedded_vm", Platform: "linux/arm64", Verifier: "/opt/foundry/verify-go-v1", EmbeddedVM: &identity}
	m := domain.Mission{ID: "projection", Revision: 17, Spec: domain.Spec{C4: &p}}
	view, err := f.handler.(*server).missionView(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	var got struct {
		Binding *struct {
			Revision int64            `json:"revision"`
			Profile  domain.C4Profile `json:"profile"`
			Digest   string           `json:"digest"`
		} `json:"c4_profile_binding"`
	}
	if json.Unmarshal(raw, &got) != nil || got.Binding == nil || got.Binding.Revision != 17 || got.Binding.Digest != p.ProfileDigest() || !reflect.DeepEqual(got.Binding.Profile, p) {
		t.Fatal("missing exact response-only profile binding", string(raw))
	}
	identity.ManifestDigest = strings.Repeat("6", 64)
	if got.Binding.Digest == p.ProfileDigest() {
		t.Fatal("changed manifest retained profile digest")
	}
	legacy := domain.Mission{ID: "legacy"}
	view, err = f.handler.(*server).missionView(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(view)
	if strings.Contains(string(raw), "c4_profile_binding") {
		t.Fatal("legacy projection changed")
	}
	p.EmbeddedVM.Protocol = "unsupported"
	view, err = f.handler.(*server).missionView(m)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(view)
	if strings.Contains(string(raw), "c4_profile_binding") {
		t.Fatal("invalid profile presented as bound")
	}
}
