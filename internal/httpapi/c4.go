package httpapi

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"net/http"
)

// The outer decoder rejects duplicate, unknown and case-aliased keys. This
// discriminator also rejects irrelevant keys even when empty or null.
func (q *c4Request) UnmarshalJSON(raw []byte) error {
	type plain c4Request
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	allowed := map[string]bool{"action": true, "revision": true}
	switch value.Action {
	case "prepare", "prepare_readiness":
	case "approve", "execute":
		allowed["operation_id"], allowed["material_digest"] = true, true
	case "cleanup", "prepare_cleanup":
		allowed["operation_id"], allowed["material_digest"], allowed["container_id"] = true, true, true
	case "approve_control", "execute_readiness", "execute_cleanup":
		allowed["control_id"], allowed["material_digest"] = true, true
	default:
		return errors.New("unsupported C4 action")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if len(fields) != len(allowed) {
		return errors.New("exact C4 action fields required")
	}
	for key, v := range fields {
		if !allowed[key] || string(v) == "null" {
			return errors.New("invalid C4 action field")
		}
	}
	*q = c4Request(value)
	return nil
}

// C4 request values select existing material, never a driver, command, image,
// environment, mode or resource policy. The normal session middleware applies.
type c4Request struct {
	Action         string `json:"action"`
	Revision       int64  `json:"revision"`
	OperationID    string `json:"operation_id,omitempty"`
	ControlID      string `json:"control_id,omitempty"`
	MaterialDigest string `json:"material_digest,omitempty"`
	ContainerID    string `json:"container_id,omitempty"`
}

func c4RealExecution(source string, expired, readOnly bool) bool {
	return !expired && !readOnly && (source == "docker_c4_control" || source == "embedded_vm_c4_control")
}

func (s *server) c4(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodGet {
		m, err := s.store.GetMission(id)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		control := s.engine.C4ControlledStatus(m)
		available := s.engine.C4SimulationAvailable(m)
		if m.Spec.C4 != nil && m.Spec.C4.ControlVersion == 1 {
			available = control.StructurallyAvailable
		}
		readOnly := s.recoveryReadOnly || !available
		writeJSON(w, http.StatusOK, map[string]any{"mission_id": id, "revision": m.Revision, "profile": m.Spec.C4, "operations": m.C4Operations, "controls": m.C4Controls, "control": control, "read_only": readOnly, "simulation": s.engine.C4SimulationEnabled() && !readOnly, "real_execution": c4RealExecution(control.SourceKind, control.Expired, s.recoveryReadOnly)})
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
	case "prepare", "prepare_readiness":
		valid = valid && request.OperationID == "" && request.ControlID == "" && request.MaterialDigest == "" && request.ContainerID == ""
	case "approve", "execute":
		valid = valid && request.ControlID == "" && request.OperationID != "" && request.MaterialDigest != "" && request.ContainerID == ""
	case "cleanup", "prepare_cleanup":
		valid = valid && request.ControlID == "" && request.OperationID != "" && request.MaterialDigest != "" && request.ContainerID != ""
	case "approve_control", "execute_readiness", "execute_cleanup":
		valid = valid && request.ControlID != "" && request.OperationID == "" && request.MaterialDigest != "" && request.ContainerID == ""
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
	case "prepare_readiness":
		m, err = s.engine.PrepareC4Readiness(r.Context(), id, request.Revision)
	case "approve_control":
		m, err = s.engine.ApproveC4Control(r.Context(), id, request.Revision, request.ControlID, request.MaterialDigest)
	case "execute_readiness":
		m, err = s.engine.ExecuteC4Readiness(r.Context(), id, request.Revision, request.ControlID, request.MaterialDigest)
	case "prepare_cleanup":
		m, err = s.engine.PrepareC4Cleanup(r.Context(), id, request.Revision, request.OperationID, request.MaterialDigest, request.ContainerID)
	case "execute_cleanup":
		m, err = s.engine.ExecuteC4Cleanup(r.Context(), id, request.Revision, request.ControlID, request.MaterialDigest)
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
