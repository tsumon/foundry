package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/jsontext"
	"foundry/internal/store"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// Account mutations are flat objects: token decoding detects even escaped
// duplicate names before the strict typed decoder sees the request.
func decodeAccountJSON(w http.ResponseWriter, r *http.Request, target any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if !jsontext.Lossless(raw) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("object required")
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	typ := reflect.TypeOf(target).Elem()
	for i := 0; i < typ.NumField(); i++ {
		allowed[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok || seen[name] || !allowed[name] {
			return errors.New("unknown or duplicate field")
		}
		seen[name] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return err
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeJSON(w, r, target)
}

func writeAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict):
		writeMappedError(w, err)
	case errors.Is(err, accounts.ErrInvalid), errors.Is(err, domain.ErrInvalidModelAccount):
		writeError(w, 400, "invalid_model_account", "invalid model account request")
	case errors.Is(err, accounts.ErrInProgress):
		writeError(w, 409, "model_credential_in_progress", "credential operation is in progress")
	default:
		writeError(w, 503, "model_account_storage_unavailable", "model account storage is unavailable")
	}
}

func (s *server) modelAccountsRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/model-accounts")
	if path == "" {
		switch r.Method {
		case "GET":
			items, err := s.store.ListModelAccounts()
			if err != nil {
				writeAccountError(w, err)
				return
			}
			views := make([]safeModelAccount, 0, len(items))
			for _, a := range items {
				views = append(views, accountView(a))
			}
			writeJSON(w, 200, map[string]any{"accounts": views})
		case "POST":
			var q struct {
				Name        string `json:"name"`
				Provider    string `json:"provider"`
				BaseURL     string `json:"base_url"`
				AuthMethod  string `json:"auth_method"`
				BillingMode string `json:"billing_mode"`
			}
			if err := decodeAccountJSON(w, r, &q); err != nil {
				writeDecodeError(w, err)
				return
			}
			a, err := s.accounts.Save(domain.ModelAccount{Name: q.Name, Provider: q.Provider, BaseURL: q.BaseURL, AuthMethod: q.AuthMethod, BillingMode: q.BillingMode, Enabled: true}, 0)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"account": accountView(a)})
		default:
			writeError(w, 404, "not_found", "route not found")
		}
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	id := parts[0]
	if id == "" {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if len(parts) == 1 {
		a, err := s.store.GetModelAccount(id)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		switch r.Method {
		case "GET":
			versions, err := s.store.ListModelCredentials(id)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			views := make([]safeModelCredential, 0, len(versions))
			for _, v := range versions {
				views = append(views, credentialView(v))
			}
			writeJSON(w, 200, map[string]any{"account": accountView(a), "credentials": views})
		case "PUT":
			var q struct {
				Name             string `json:"name"`
				Enabled          *bool  `json:"enabled"`
				ExpectedRevision int64  `json:"expected_revision"`
			}
			if err := decodeAccountJSON(w, r, &q); err != nil {
				writeDecodeError(w, err)
				return
			}
			if q.Enabled == nil || q.ExpectedRevision < 1 {
				writeAccountError(w, accounts.ErrInvalid)
				return
			}
			a.Name = q.Name
			a.Enabled = *q.Enabled
			a, err = s.accounts.Save(a, q.ExpectedRevision)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"account": accountView(a)})
		default:
			writeError(w, 404, "not_found", "route not found")
		}
		return
	}
	if parts[1] == "acceptance" {
		if len(parts) != 2 || r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeError(w, 404, "not_found", "route not found")
			return
		}
		s.modelAcceptance(w, r, id)
		return
	}
	if parts[1] == "acceptance-operations" {
		s.modelAcceptanceOperations(w, r, parts)
		return
	}
	if len(parts) == 3 && parts[1] == "operations" && r.Method == "GET" {
		if err := domain.ValidateModelCredentialOperation(parts[2]); err != nil {
			writeAccountError(w, err)
			return
		}
		v, err := s.store.GetModelCredentialOperation(parts[2])
		if err == nil && v.AccountID != id {
			err = store.ErrNotFound
		}
		if err != nil {
			writeAccountError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"operation": credentialView(v)})
		return
	}
	if parts[1] == "credentials" && len(parts) == 2 {
		switch r.Method {
		case "GET":
			if _, err := s.store.GetModelAccount(id); err != nil {
				writeAccountError(w, err)
				return
			}
			versions, err := s.store.ListModelCredentials(id)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			views := make([]safeModelCredential, 0, len(versions))
			for _, v := range versions {
				views = append(views, credentialView(v))
			}
			writeJSON(w, 200, map[string]any{"credentials": views})
		case "POST":
			var q struct {
				ExpectedRevision int64  `json:"expected_revision"`
				OperationID      string `json:"operation_id"`
				Key              string `json:"key"`
			}
			if err := decodeAccountJSON(w, r, &q); err != nil {
				writeDecodeError(w, err)
				return
			}
			if s.rejectAcceptanceBypass(w, id) {
				return
			}
			v, err := s.accounts.AddCredential(r.Context(), id, q.ExpectedRevision, q.OperationID, q.Key)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"operation": credentialView(v)})
		default:
			writeError(w, 404, "not_found", "route not found")
		}
		return
	}
	if len(parts) == 4 && parts[1] == "credentials" && parts[3] == "revoke" && r.Method == "POST" {
		version, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || version < 1 || strconv.FormatInt(version, 10) != parts[2] {
			writeAccountError(w, accounts.ErrInvalid)
			return
		}
		var q struct {
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if err := decodeAccountJSON(w, r, &q); err != nil {
			writeDecodeError(w, err)
			return
		}
		v, err := s.accounts.RevokeCredential(r.Context(), id, version, q.ExpectedRevision)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"operation": credentialView(v)})
		return
	}
	writeError(w, 404, "not_found", "route not found")
}
