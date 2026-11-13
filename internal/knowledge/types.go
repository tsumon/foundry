// Package knowledge projects business facts into a typed, local knowledge graph.
// It does not grant execution authority or replace the source records.
package knowledge

import "foundry/internal/domain"

const SchemaVersion = 1
const Backend = "sqlite-property-graph"

type TypeDefinition struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description"`
}
type RelationDefinition struct {
	Pairs [][2]string `json:"pairs,omitempty"`
	Kind  string      `json:"kind"`
	Label string      `json:"label"`
	From  []string    `json:"from"`
	To    []string    `json:"to"`
}
type Ontology struct {
	Version   int                  `json:"version"`
	Types     []TypeDefinition     `json:"types"`
	Relations []RelationDefinition `json:"relations"`
	Rules     []string             `json:"rules"`
}
type Provenance struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}
type Node struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Label          string            `json:"label"`
	ProjectID      string            `json:"project_id,omitempty"`
	MissionID      string            `json:"mission_id,omitempty"`
	State          string            `json:"state,omitempty"`
	EvidenceSource string            `json:"evidence_source,omitempty"`
	Historical     bool              `json:"historical"`
	Source         Provenance        `json:"source"`
	Properties     map[string]string `json:"properties"`
}
type Edge struct {
	ID     string     `json:"id"`
	From   string     `json:"from"`
	To     string     `json:"to"`
	Kind   string     `json:"kind"`
	Source Provenance `json:"source"`
}
type Issue struct {
	Code     string `json:"code"`
	SourceID string `json:"source_id"`
	Message  string `json:"message"`
}
type Graph struct {
	SchemaVersion int     `json:"schema_version"`
	Backend       string  `json:"backend"`
	SourceDigest  string  `json:"source_digest"`
	SyncedAt      string  `json:"synced_at"`
	TotalNodes    int     `json:"total_nodes"`
	TotalEdges    int     `json:"total_edges"`
	Truncated     bool    `json:"truncated"`
	Nodes         []Node  `json:"nodes"`
	Edges         []Edge  `json:"edges"`
	Issues        []Issue `json:"issues"`
}
type Source struct {
	Catalog       domain.CatalogSnapshot
	Missions      []domain.Mission
	PlanRevisions []domain.PlanRevision
}
type Query struct {
	ProjectID string
	MissionID string
	Kind      string
	Search    string
	Root      string
	Mode      string // neighbors (both directions), impact (outgoing), lineage (incoming)
	Depth     int
	Limit     int
}
type Context struct {
	SchemaVersion int    `json:"schema_version"`
	MissionID     string `json:"mission_id"`
	Revision      int64  `json:"revision"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
	Truncated     bool   `json:"truncated"`
}
