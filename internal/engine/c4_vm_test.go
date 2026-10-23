package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/vmruntime"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestC4UnknownSourceCannotInheritSyntheticAuthority(t *testing.T) {
	e, _, m, _, _, _ := c4FixtureMode(t, true)
	e.options.C4Controlled.source = "unknown"
	if e.c4ControlledGate(m, false) == nil {
		t.Fatal("unknown source accepted as synthetic authority")
	}
}

func TestC4CapabilityMetadataWithoutMissionPreservesFixtureSource(t *testing.T) {
	e, _, _, _, _, _ := c4FixtureMode(t, true)
	status := e.C4ControlledStatus(domain.Mission{})
	if status.SourceKind != "synthetic_c4_control" || status.Available {
		t.Fatal("wrong global fixture metadata", status)
	}
}

func TestC4VMVerificationNamesExactBackend(t *testing.T) {
	zero := 0
	p := &domain.C4Profile{Mode: "embedded_vm", ControlVersion: 1, ImageID: "", Verifier: "/opt/foundry/verify-go-v1", EmbeddedVM: &domain.C4VMIdentity{ManifestDigest: strings.Repeat("a", 64)}}
	o := domain.C4Operation{SchemaVersion: 1, ID: "op", State: "finished", ContainerID: strings.Repeat("b", 64), Owner: "owner", ReadinessID: "ready", ReadinessDigest: "digest", Approval: &domain.C4Approval{}, Dispatch: &domain.C4Fact{}, Created: &domain.C4Fact{}, Started: &domain.C4Fact{}, Result: &domain.C4Result{Class: "passed", Tests: 1, ExitCode: &zero}, Cleanup: []domain.C4Cleanup{{State: "confirmed", ContainerID: strings.Repeat("b", 64), Owner: "owner"}}}
	m := domain.Mission{Spec: domain.Spec{C4: p}, C4Operations: []domain.C4Operation{o}, C4Controls: []domain.C4Control{{ID: "ready", MaterialDigest: "digest", SourceKind: "embedded_vm_c4_control", Result: &domain.C4ControlResult{Status: "ready"}}}}
	v := c4Verification(m, o)
	if v.SourceKind != "isolated_execution" || v.ImageID != "embedded-vm:sha256:"+strings.Repeat("a", 64) || !v.Passed {
		t.Fatal("wrong VM verification", v)
	}
	for _, source := range []string{"unknown", "docker_c4_control", "synthetic_c4_control"} {
		m.C4Controls[0].SourceKind = source
		if c4Verification(m, o).SourceKind != "unknown" {
			t.Fatal("foreign receipt promoted", source)
		}
	}
	// Fill a structurally valid declaration for the report's defensive gate.
	p.SchemaVersion = 1
	p.ID = "c4_go_model_v1"
	p.Platform = "linux/arm64"
	p.EmbeddedVM = &domain.C4VMIdentity{SchemaVersion: 1, ManifestDigest: strings.Repeat("a", 64), HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", RuntimeDigest: strings.Repeat("a", 64), KernelDigest: strings.Repeat("a", 64), RootFSDigest: strings.Repeat("a", 64), VerifierDigest: strings.Repeat("a", 64), Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1"}
	m.C4Controls[0].SourceKind = "embedded_vm_c4_control"
	var out bytes.Buffer
	writeC4ReportSources(&out, m)
	if !strings.Contains(out.String(), "真实内置 VM 测试") || strings.Contains(out.String(), "真实 Docker 测试") {
		t.Fatal("mislabelled VM evidence", out.String())
	}
}

// Inert metadata only: no helper is executable and no VM is started.
func newInertVMRuntime(t *testing.T) (*vmruntime.Runtime, string, domain.C4VMIdentity) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("fixture manifest targets Apple Silicon")
	}
	bundle, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	instances, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := vmruntime.Manifest{SchemaVersion: 1, HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1", Runtime: "runtime/foundry-vm-helper", Kernel: "kernel", RootFS: "initramfs.cpio.gz", Verifier: "foundry-vm-guest"}
	body := []byte("inert metadata fixture, never executable")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	for _, p := range []string{m.Runtime, m.Kernel, m.RootFS, m.Verifier} {
		full := filepath.Join(bundle, p)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0600); err != nil {
			t.Fatal(err)
		}
		m.Assets = append(m.Assets, vmruntime.Asset{Path: p, SHA256: digest, Size: int64(len(body))})
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	vm, err := vmruntime.Load(bundle, instances)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(raw)
	expected := domain.C4VMIdentity{SchemaVersion: 1, ManifestDigest: hex.EncodeToString(manifestSum[:]), HostPlatform: "darwin/arm64", GuestPlatform: "linux/arm64", RuntimeDigest: digest, KernelDigest: digest, RootFSDigest: digest, VerifierDigest: digest, Protocol: "foundry-vm-v1", PolicyVersion: "go-vm-v1"}
	return vm, instances, expected
}

func TestCodingVMCapabilityFrozenMetadataAndExactIdentity(t *testing.T) {
	vm, instances, expected := newInertVMRuntime(t)
	router := model.NewRouter(nil, nil, model.RouterOptions{})
	cap, err := NewCodingVMCapability(router, vm)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{options: Options{C4Controlled: cap}}
	p, ok := e.CodingVMProfile()
	if !ok {
		t.Fatal("no VM metadata")
	}
	if p.Validate() != nil || *p.EmbeddedVM != expected || !cap.vmProfileMatches(p) {
		t.Fatalf("wrong frozen identity: %+v", p)
	}
	p.EmbeddedVM.ManifestDigest = strings.Repeat("b", 64)
	again, _ := e.CodingVMProfile()
	if *again.EmbeddedVM != expected || cap.vmProfileMatches(p) {
		t.Fatal("client identity mutation changed trusted runtime")
	}
	entries, err := os.ReadDir(instances)
	if err != nil || len(entries) != 0 {
		t.Fatal("metadata booted/allocated VM", entries, err)
	}
	for _, bad := range []*vmruntime.Runtime{nil, {}} {
		if _, err := NewCodingVMCapability(router, bad); err == nil {
			t.Fatal("untrusted runtime capability")
		}
	}
	if cap.sourceFor(&domain.C4Profile{Mode: "docker"}) != "docker_c4_control" {
		t.Fatal("explicit Docker selection unavailable")
	}
}
