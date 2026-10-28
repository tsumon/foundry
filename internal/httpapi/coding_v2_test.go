package httpapi

import (
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodingV2UnavailableCatalogIsAdditive(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	r := f.request("GET", "/api/coding-tasks", "", true)
	var v map[string]json.RawMessage
	json.Unmarshal(r.Body.Bytes(), &v)
	var profiles []engine.CodingProfileTemplateV2
	if json.Unmarshal(v["profiles"], &profiles) != nil || len(profiles) != 3 {
		t.Fatal("missing closed catalog", r.Body.String())
	}
	for _, p := range profiles {
		if p.Available || p.EmbeddedVM != nil || p.Reason != "embedded_vm_v2_bundle_unavailable" {
			t.Fatal(p)
		}
	}
}
func TestCodingV2RecursiveExactKeys(t *testing.T) {
	for _, raw := range []string{`{"spec":{"scope":[],"Scope":[]}}`, `{"spec":{"c4":{"schema_version":2,"id":"c4_node_model_v2","mode":"embedded_vm","dependency_store_root":"/private"}}}`, `{"spec":{"verification_v2":{"schema_version":2,"profile_id":"c4_node_model_v2","dependencies":{"schema_version":1,"ecosystem":"node","bundle_digest":"a","manifest_digest":"b","total_bytes":0}}}}`, `{"spec":{"verification_v2":{"schema_version":2,"schema_versio\u006e":2}}}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
		w := httptest.NewRecorder()
		var q domain.RepositoryInspectionRequest
		if err := decodeCodingJSON(w, r, &q); err == nil {
			t.Fatal("accepted nested malformed", raw)
		}
	}
}
func TestCodingV2RejectsNullRequiredScalar(t *testing.T) {
	raw := `{"schema_version":1,"ecosystem":"node","bundle_digest":"a","manifest_digest":"b","file_count":null,"total_bytes":0}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	var binding domain.C4DependencyBindingV2
	if decodeCodingJSON(httptest.NewRecorder(), r, &binding) == nil {
		t.Fatal("null count accepted as real zero")
	}
}
func TestCodingV2DependencyFailureUsesClosedClientError(t *testing.T) {
	for _, code := range []domain.ImportError{domain.ImportDependencies, domain.ImportInputs} {
		w := httptest.NewRecorder()
		writeMappedError(w, code)
		if w.Code != 400 || !strings.Contains(w.Body.String(), string(code)) {
			t.Fatal("dependency/input rejection is not a client error", w.Code, w.Body.String())
		}
	}
}
