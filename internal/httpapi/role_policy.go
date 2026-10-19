package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"foundry/internal/domain"
	"foundry/internal/jsontext"
	"foundry/internal/store"
)

type rolePolicyPutRequest struct {
	ExpectedRevision *int64                                     `json:"expected_revision"`
	ProfilesDigest   string                                     `json:"profiles_digest"`
	Roles            map[domain.Role]domain.RolePolicyRoleInput `json:"roles"`
}

type rolePolicyConfirmRequest struct {
	Digest  string `json:"digest"`
	Confirm *bool  `json:"confirm"`
}

func (s *server) rolePolicyRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/role-model-policy":
		state, err := s.store.GetRolePolicyState()
		if err != nil {
			writeRolePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": state})
	case r.Method == http.MethodPut && r.URL.Path == "/api/role-model-policy":
		var request rolePolicyPutRequest
		if err := decodeRolePolicyJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		if request.ExpectedRevision == nil {
			writeRolePolicyError(w, store.ErrRolePolicyValidation)
			return
		}
		policy, err := s.store.SaveRoleModelPolicy(domain.RolePolicyInput{Roles: request.Roles}, *request.ExpectedRevision, request.ProfilesDigest)
		if err != nil {
			writeRolePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"policy": policy})
	case r.Method == http.MethodPost && r.URL.Path == "/api/role-model-policy/proposals":
		var request struct{}
		if err := decodeRolePolicyJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		id, err := opaqueID()
		if err != nil {
			writeRolePolicyError(w, err)
			return
		}
		proposal, err := s.store.ProposeRoleModels(id)
		if err != nil {
			writeRolePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"proposal": proposal})
	case r.Method == http.MethodPost && rolePolicyConfirmationPath(r.URL.Path) != "":
		proposalID := rolePolicyConfirmationPath(r.URL.Path)
		var request rolePolicyConfirmRequest
		if err := decodeRolePolicyJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		if request.Confirm == nil || !*request.Confirm {
			writeRolePolicyError(w, store.ErrRolePolicyValidation)
			return
		}
		decisionID, err := opaqueID()
		if err != nil {
			writeRolePolicyError(w, err)
			return
		}
		decision, settings, applied, err := s.store.ConfirmRoleModelProposal(proposalID, request.Digest, decisionID)
		if err != nil {
			writeRolePolicyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"decision": decision, "settings": settings, "applied": applied})
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func rolePolicyConfirmationPath(path string) string {
	const prefix = "/api/role-model-policy/proposals/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "confirm" {
		return ""
	}
	return parts[0]
}

func writeRolePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrRolePolicyValidation):
		writeError(w, http.StatusBadRequest, "role_policy_validation", "role policy request is invalid")
	case errors.Is(err, store.ErrRolePolicyStale):
		writeError(w, http.StatusConflict, "role_policy_stale", "role policy inputs have changed")
	case errors.Is(err, store.ErrRolePolicyIncomplete):
		writeError(w, http.StatusConflict, "role_policy_incomplete", "role policy does not have a complete recommendation")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "role policy operation failed")
	}
}

// Role-policy requests contain nested user declarations. Reject duplicate
// names at every object depth before the typed decoder can collapse them.
func decodeRolePolicyJSON(w http.ResponseWriter, r *http.Request, target any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if !jsontext.Lossless(raw) {
		return errors.New("invalid JSON")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("JSON object required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decodeExactRolePolicyJSONValue(decoder, reflect.TypeOf(target)); err != nil {
		return err
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON(w, r, target)
}

func decodeExactRolePolicyJSONValue(decoder *json.Decoder, targetType reflect.Type) error {
	for targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		fields := map[string]reflect.Type{}
		var mapValueType reflect.Type
		switch targetType.Kind() {
		case reflect.Struct:
			for index := 0; index < targetType.NumField(); index++ {
				field := targetType.Field(index)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					fields[name] = field.Type
				}
			}
		case reflect.Map:
			mapValueType = targetType.Elem()
		default:
			return errors.New("unexpected JSON object")
		}
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, nameErr := decoder.Token()
			if nameErr != nil {
				return nameErr
			}
			name, nameOK := nameToken.(string)
			if !nameOK {
				return errors.New("object field name is invalid")
			}
			if _, duplicate := seen[name]; duplicate {
				return errors.New("duplicate JSON field")
			}
			seen[name] = struct{}{}
			valueType := mapValueType
			if targetType.Kind() == reflect.Struct {
				var exists bool
				valueType, exists = fields[name]
				if !exists {
					return errors.New("unknown JSON field")
				}
			}
			if err = decodeExactRolePolicyJSONValue(decoder, valueType); err != nil {
				return err
			}
		}
		end, endErr := decoder.Token()
		if endErr != nil || end != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		if targetType.Kind() != reflect.Slice && targetType.Kind() != reflect.Array {
			return errors.New("unexpected JSON array")
		}
		for decoder.More() {
			if err = decodeExactRolePolicyJSONValue(decoder, targetType.Elem()); err != nil {
				return err
			}
		}
		end, endErr := decoder.Token()
		if endErr != nil || end != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}
