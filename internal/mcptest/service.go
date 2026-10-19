// Package mcptest owns the explicitly enabled, synthetic loopback reference
// service. It has no filesystem, command, credential or upstream capability.
package mcptest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"foundry/internal/capability"
	"foundry/internal/domain"
	"foundry/internal/jsontext"
	"foundry/internal/store"
)

const schema = `{"type":"object","properties":{"topic":{"type":"string","enum":["mcp-workflow","approval-boundaries"]}},"required":["topic"],"additionalProperties":false}`
const definition = `{"name":"reference.lookup","description":"Synthetic local test reference; not repository evidence","inputSchema":` + schema + `}`
const policy = `{"policy_version":"mcp_read_v1","reviewed":true,"schema":` + schema + `}`
const connectionID = "foundry-owned-local-test"

// Descriptor is Go-only process authority, never a browser request. Tests may
// inject their own owned listener. Engine copies Binding when constructed.
type Descriptor struct {
	Binding domain.MCPTaskBindings
	Alive   func() bool
}
type Service struct {
	server     *http.Server
	endpoint   string
	alive      atomic.Bool
	descriptor Descriptor
}

func hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

// Start binds its own ephemeral loopback port and persists real discovery.
// A new endpoint changes the connection revision; old authority is never adopted.
func Start(ctx context.Context, s *store.Store) (*Service, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	service := &Service{endpoint: "http://" + listener.Addr().String() + "/mcp"}
	service.server = &http.Server{Handler: http.HandlerFunc(service.handle), ReadHeaderTimeout: 5 * time.Second}
	service.alive.Store(true)
	go func() { _ = service.server.Serve(listener); service.alive.Store(false) }()
	if err = service.register(ctx, s); err != nil {
		service.Close()
		return nil, err
	}
	return service, nil
}
func (s *Service) Close() error { s.alive.Store(false); return s.server.Close() }
func (s *Service) Descriptor() Descriptor {
	d := s.descriptor
	raw, _ := json.Marshal(d.Binding)
	var binding domain.MCPTaskBindings
	_ = json.Unmarshal(raw, &binding)
	d.Binding = binding
	return d
}

func (s *Service) register(ctx context.Context, db *store.Store) error {
	c, err := db.GetMCPConnection(connectionID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if c.Endpoint != s.endpoint || !c.Enabled || c.CredentialRef != "" || c.Protocol != domain.MCPProtocol || c.Name != "FOUNDRY synthetic local test" {
		c, err = db.SaveMCPConnection(domain.MCPConnection{ID: connectionID, Name: "FOUNDRY synthetic local test", Endpoint: s.endpoint, Protocol: domain.MCPProtocol, Enabled: true}, c.Revision)
		if err != nil {
			return err
		}
	}
	op, err := capability.New(db, nil).Discover(ctx, c.ID, c.Revision, fmt.Sprintf("local-test-discovery-%d", c.Revision), true)
	if err != nil {
		return err
	}
	if op.Status != "succeeded" {
		return errors.New("local test discovery did not succeed")
	}
	snap, err := db.GetMCPExecutionSnapshot(c.ID, op.SnapshotID)
	if err != nil {
		return err
	}
	if len(snap.Tools) != 1 || snap.Tools[0].Name != "reference.lookup" {
		return errors.New("local test definition mismatch")
	}
	tool := snap.Tools[0]
	s.descriptor = Descriptor{Binding: domain.MCPTaskBindings{ConnectionID: c.ID, ConnectionRevision: c.Revision, Endpoint: c.Endpoint, Protocol: c.Protocol, DiscoveryOperationID: op.ID, SnapshotID: snap.ID, SnapshotDigest: snap.Digest, Tools: []domain.MCPToolBinding{{Name: tool.Name, DefinitionDigest: tool.DefinitionDigest, InputSchemaDigest: hash(tool.InputSchema), Policy: json.RawMessage(policy)}}, Budget: domain.FixedMCPBudget()}, Alive: s.alive.Load}
	return nil
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Host != s.serverHost() || r.URL.Path != "/mcp" || r.Header.Get("Authorization") != "" {
		http.Error(w, "denied", 403)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if decoder.Decode(&request) != nil || request.JSONRPC != "2.0" {
		http.Error(w, "invalid", 400)
		return
	}
	if request.Method == "notifications/initialized" {
		w.WriteHeader(202)
		return
	}
	var result any
	switch request.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": domain.MCPProtocol, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "FOUNDRY synthetic local test", "version": "1"}}
	case "tools/list":
		result = map[string]any{"tools": []json.RawMessage{json.RawMessage(definition)}}
	case "tools/call":
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		var topic string
		if !jsontext.Lossless(request.Params) || json.Unmarshal(request.Params, &p) != nil || p.Name != "reference.lookup" || len(p.Arguments) != 1 || json.Unmarshal(p.Arguments["topic"], &topic) != nil || (topic != "mcp-workflow" && topic != "approval-boundaries") {
			http.Error(w, "invalid", 400)
			return
		}
		text := "SYNTHETIC LOCAL TEST: MCP observations inform research; they do not authorize execution, verification or delivery."
		if topic == "approval-boundaries" {
			text = "SYNTHETIC LOCAL TEST: Context consent, one MCP call, plan execution and delivery are separate approvals."
		}
		result = map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}, "isError": false}
	default:
		http.Error(w, "unsupported", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
}
func (s *Service) serverHost() string {
	return s.endpoint[len("http://") : len(s.endpoint)-len("/mcp")]
}
