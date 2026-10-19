package httpapi

import (
	"foundry/internal/domain"
	"net/http"
	"net/url"
	"strconv"
)

func singleQueryValue(values url.Values, name string) (string, bool) {
	items, ok := values[name]
	if !ok || len(items) != 1 {
		return "", false
	}
	return items[0], true
}

func strictQuery(r *http.Request) (url.Values, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	return values, err == nil
}

func asciiDecimal(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func (s *server) invocationList(w http.ResponseWriter, r *http.Request, missionID string) {
	query, ok := strictQuery(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "validation", "invocation query is invalid")
		return
	}
	for key := range query {
		if key != "before" && key != "limit" {
			writeError(w, http.StatusBadRequest, "validation", "invocation query is invalid")
			return
		}
	}
	before := int64(0)
	if _, present := query["before"]; present {
		raw, ok := singleQueryValue(query, "before")
		var err error
		if !ok || !asciiDecimal(raw) {
			err = strconv.ErrSyntax
		} else {
			var parsed uint64
			parsed, err = strconv.ParseUint(raw, 10, 63)
			before = int64(parsed)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation", "before must be a non-negative invocation ordinal")
			return
		}
	}
	limit := 30
	if _, present := query["limit"]; present {
		raw, ok := singleQueryValue(query, "limit")
		var err error
		if !ok || !asciiDecimal(raw) {
			err = strconv.ErrSyntax
		} else {
			limit, err = strconv.Atoi(raw)
		}
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "validation", "limit must be between 1 and 100")
			return
		}
	}
	invocations, err := s.store.ListInvocations(missionID, before, limit)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invocations": invocations})
}

func (s *server) readGrants(w http.ResponseWriter, r *http.Request, missionID string) {
	query, ok := strictQuery(r)
	if !ok || len(query) != 0 {
		writeError(w, http.StatusBadRequest, "validation", "read grant query is invalid")
		return
	}
	grants, err := s.store.ListReadGrants(missionID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	m, err := s.store.GetMission(missionID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		type summary struct {
			ID        string      `json:"id"`
			Role      domain.Role `json:"role"`
			CreatedAt string      `json:"created_at"`
		}
		views := make([]summary, 0, len(grants))
		for _, g := range grants {
			views = append(views, summary{g.ID, g.Role, g.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"read_grants": views})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"read_grants": grants})
}

func (s *server) invocationDetail(w http.ResponseWriter, r *http.Request, missionID, invocationID string) {
	query, ok := strictQuery(r)
	if !ok || len(query) != 0 || invocationID == "" {
		writeError(w, http.StatusBadRequest, "validation", "invocation detail query is invalid")
		return
	}
	detail, err := s.engine.InvocationDetail(missionID, invocationID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	m, err := s.store.GetMission(missionID)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invocation": invocationView(m, detail)})
}

func invocationView(m domain.Mission, detail domain.InvocationDetail) any {
	if detail.Invocation.ModelReceipt != nil {
		receipt := domain.SanitizeModelCallReceipt(*detail.Invocation.ModelReceipt)
		detail.Invocation.ModelReceipt = &receipt
	}
	if detail.Invocation.Kind == "model" && (domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) || missionMayContainAccountModelEvidence(m)) {
		// Raw model outputs may echo their inputs. Keep both verified envelope
		// identities, but expose parsed task results only through their safe views.
		detail.InputPreview = ""
		detail.InputTruncated = false
		detail.ResultPreview = ""
		detail.ResultTruncated = false
		view := struct {
			domain.InvocationDetail
			InputRedacted  bool `json:"input_redacted"`
			ResultRedacted bool `json:"result_redacted"`
		}{detail, true, detail.Invocation.Result != nil}
		return view
	}
	return detail
}

// missionMayContainAccountModelEvidence deliberately does not require a valid
// snapshot. Diagnostic reads of corrupt or future account snapshots must fail
// closed because model previews can echo credentials and prompts.
func missionMayContainAccountModelEvidence(m domain.Mission) bool {
	if m.ModelSnapshotInvalid || m.ModelSnapshot != nil {
		return true
	}
	if _, accountBound, err := domain.BaseWorkspaceMode(m.WorkspaceMode); err != nil || accountBound {
		return true
	}
	for _, profile := range m.Profiles {
		if profile.AccountID != "" || profile.AccountRevision != 0 || profile.CredentialVersion != 0 || profile.RouteID != "" {
			return true
		}
	}
	return false
}
