package httpapi

import (
	"foundry/internal/domain"
	"net/http"
)

// C4 request values select existing material, never a driver, command, image,
// environment, mode or resource policy. The normal session middleware applies.
type c4Request struct {
	Action         string `json:"action"`
	Revision       int64  `json:"revision"`
	OperationID    string `json:"operation_id,omitempty"`
	MaterialDigest string `json:"material_digest,omitempty"`
	ContainerID    string `json:"container_id,omitempty"`
}

func (s *server) c4(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodGet {
		m, err := s.store.GetMission(id)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		readOnly := s.recoveryReadOnly || !s.engine.C4SimulationAvailable(m)
		writeJSON(w, http.StatusOK, map[string]any{"mission_id": id, "revision": m.Revision, "profile": m.Spec.C4, "operations": m.C4Operations, "read_only": readOnly, "simulation": s.engine.C4SimulationEnabled() && !readOnly, "real_execution": false})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	var request c4Request
	if err := decodeAccountJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	valid := request.Revision > 0
	switch request.Action {
	case "prepare":
		valid = valid && request.OperationID == "" && request.MaterialDigest == "" && request.ContainerID == ""
	case "approve", "execute":
		valid = valid && request.OperationID != "" && request.MaterialDigest != "" && request.ContainerID == ""
	case "cleanup":
		valid = valid && request.OperationID != "" && request.MaterialDigest != "" && request.ContainerID != ""
	default:
		valid = false
	}
	if !valid {
		writeError(w, 400, "validation", "C4 action requires exact revision and material binding")
		return
	}
	var m domain.Mission
	var err error
	switch request.Action {
	case "prepare":
		m, err = s.engine.PrepareC4Baseline(r.Context(), id, request.Revision)
	case "approve":
		m, err = s.engine.ApproveC4Baseline(r.Context(), id, request.Revision, request.OperationID, request.MaterialDigest)
	case "execute":
		m, err = s.engine.ExecuteC4Baseline(r.Context(), id, request.Revision, request.OperationID, request.MaterialDigest)
	case "cleanup":
		m, err = s.engine.CleanupC4Operation(r.Context(), id, request.Revision, request.OperationID, request.MaterialDigest, request.ContainerID)
	}
	if err != nil {
		writeMappedError(w, err)
		return
	}
	s.writeMission(w, m, nil)
}
