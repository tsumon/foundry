package mcptest

import (
	"context"
	"foundry/internal/mcp"
	"foundry/internal/store"
	"path/filepath"
	"testing"
)

func TestOwnedFixtureDiscoveryAndReadOnlyPolicy(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service, err := Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	descriptor := service.Descriptor()
	descriptor.Binding.Tools[0].Policy[0] = 'X'
	descriptor = service.Descriptor()
	if descriptor.Binding.Tools[0].Policy[0] != '{' {
		t.Fatal("descriptor exposed mutable service authority")
	}
	b := descriptor.Binding
	if !descriptor.Alive() || len(b.Tools) != 1 || b.Tools[0].Name != "reference.lookup" {
		t.Fatal("missing owned selection")
	}
	snap, err := s.GetMCPExecutionSnapshot(b.ConnectionID, b.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := s.GetMCPConnection(b.ConnectionID)
	result, err := mcp.Call(context.Background(), connection, snap.Tools[0], b.Tools[0].Policy, []byte(`{"topic":"mcp-workflow"}`), "", func() error { return nil })
	if err != nil || result.RemoteToolError || len(result.Result) == 0 {
		t.Fatalf("lookup: %+v %v", result, err)
	}
	for _, args := range []string{`{"topic":"other"}`, `{"topic":"mcp-workflow","path":"/etc/passwd"}`} {
		if _, err := mcp.ValidateArguments(snap.Tools[0], b.Tools[0].Policy, []byte(args)); err == nil {
			t.Fatal("unreviewed argument accepted")
		}
	}
	service.Close()
	if descriptor.Alive() {
		t.Fatal("closed fixture remains eligible")
	}
}
