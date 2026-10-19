package httpapi

import (
	"bytes"

	"foundry/internal/domain"
)

// These HTTP-only views add lossless display text alongside the compatible raw
// JSON fields. Derive it from stored RawMessages, never from decoded numbers;
// display fields must not enter persisted snapshots or their canonical digests.
type mcpToolJSONView struct {
	domain.MCPTool
	InputSchemaJSON  string `json:"input_schema_json,omitempty"`
	OutputSchemaJSON string `json:"output_schema_json,omitempty"`
	AnnotationsJSON  string `json:"annotations_json,omitempty"`
}

type mcpToolSnapshotView struct {
	domain.MCPToolSnapshot
	Tools []mcpToolJSONView `json:"tools"`
}

type capabilitiesJSONView struct {
	domain.CapabilitySnapshot
	Connections []mcpConnectionView   `json:"connections"`
	Snapshots   []mcpToolSnapshotView `json:"snapshots"`
}

type mcpConnectionView struct {
	ID                 string `json:"id"`
	Revision           int64  `json:"revision"`
	Name               string `json:"name"`
	Endpoint           string `json:"endpoint"`
	Protocol           string `json:"protocol"`
	Enabled            bool   `json:"enabled"`
	HasCredential      bool   `json:"has_credential"`
	CredentialRevision int64  `json:"credential_revision"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

func connectionView(c domain.MCPConnection) mcpConnectionView {
	return mcpConnectionView{c.ID, c.Revision, c.Name, c.Endpoint, c.Protocol, c.Enabled, c.CredentialRef != "", c.CredentialRevision, c.CreatedAt, c.UpdatedAt}
}

func mcpSnapshotView(snapshot domain.MCPToolSnapshot) mcpToolSnapshotView {
	tools := make([]mcpToolJSONView, 0, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		tools = append(tools, mcpToolJSONView{
			MCPTool: tool, InputSchemaJSON: rawJSONText(tool.InputSchema),
			OutputSchemaJSON: rawJSONText(tool.OutputSchema), AnnotationsJSON: rawJSONText(tool.Annotations),
		})
	}
	return mcpToolSnapshotView{MCPToolSnapshot: snapshot, Tools: tools}
}

func rawJSONText(raw []byte) string {
	// A missing optional RawMessage is serialized as null by persistence. It
	// represents absent metadata, not a JSON document to offer for inspection.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	return string(raw)
}

func capabilitySnapshotView(snapshot domain.CapabilitySnapshot) capabilitiesJSONView {
	snapshots := make([]mcpToolSnapshotView, 0, len(snapshot.Snapshots))
	for _, item := range snapshot.Snapshots {
		snapshots = append(snapshots, mcpSnapshotView(item))
	}
	connections := make([]mcpConnectionView, 0, len(snapshot.Connections))
	for _, c := range snapshot.Connections {
		connections = append(connections, connectionView(c))
	}
	return capabilitiesJSONView{CapabilitySnapshot: snapshot, Snapshots: snapshots, Connections: connections}
}
