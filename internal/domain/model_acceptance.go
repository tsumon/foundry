package domain

// ModelAccountAcceptance contains only a point-in-time metadata diagnostic,
// never credentials, provider URLs, an execution permit, or remote evidence.
type ModelAccountAcceptance struct {
	AccountID       string                 `json:"account_id"`
	AccountRevision int64                  `json:"account_revision"`
	Scope           string                 `json:"scope"`
	LiveAccess      string                 `json:"live_access"`
	Quota           string                 `json:"quota"`
	Checks          []ModelAcceptanceCheck `json:"checks"`
	Routes          []ModelAcceptanceRoute `json:"routes"`
}
type ModelAcceptanceCheck struct {
	Phase       string   `json:"phase"`
	State       string   `json:"state"`
	ReasonCodes []string `json:"reason_codes"`
}
type ModelAcceptanceRoute struct {
	ID               string   `json:"id"`
	Adapter          string   `json:"adapter"`
	Environment      string   `json:"environment"`
	LocalMetadata    string   `json:"local_metadata"`
	LocalExtraUsage  string   `json:"local_extra_usage"`
	RemoteExtraUsage string   `json:"remote_extra_usage"`
	ReasonCodes      []string `json:"reason_codes"`
}
