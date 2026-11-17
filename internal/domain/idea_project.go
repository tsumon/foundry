package domain

// IdeaProject is created only by the managed application entrypoint. The same
// declaration is frozen as protected seed bytes and in snapshot authority.
type IdeaProject struct {
	SchemaVersion   int      `json:"schema_version"`
	Template        string   `json:"template"`
	Acceptance      []string `json:"acceptance"`
	ParentMissionID string   `json:"parent_mission_id,omitempty"`
}
