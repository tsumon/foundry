package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func inspectionV2Fixture(t *testing.T) RepositoryInspection {
	t.Helper()
	c := sealedInput(t, 0)
	p := v2Profile(t, 0, false)
	v := inspectionFixture()
	v.SchemaVersion = 2
	v.PolicyVersion = "committed_text_c4_v2"
	v.Spec.Acceptance = []string{p.Verifier}
	v.Spec.VerificationV2 = &C4ImportSelectionV2{SchemaVersion: 2, ProfileID: c.ProfileID, EmbeddedVM: *p.EmbeddedVM, RuntimeInventoryDigest: p.V2.RuntimeInventoryDigest, AdmissionDigest: c.AdmissionDigest, Dependencies: c.Dependencies, SourceRoot: "src", TestRoot: "tests", TestSupportPrefixes: []string{}}
	v.VerificationV2 = &c
	v.ProtectedInputs = []string{}
	for _, f := range c.Protected {
		v.ProtectedInputs = append(v.ProtectedInputs, f.Path)
	}
	n := len(c.Protected) + 1
	b := int64(100)
	v.FileCount = &n
	v.TotalBytes = &b
	v.Digest = RepositoryInspectionDigest(v)
	return v
}
func TestRepositoryInspectionV2Relations(t *testing.T) {
	v := inspectionV2Fixture(t)
	if err := ValidateRepositoryInspection(v); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RepositoryInspection){
		"stripped selection": func(v *RepositoryInspection) { v.Spec.VerificationV2 = nil },
		"stripped inputs":    func(v *RepositoryInspection) { v.VerificationV2 = nil },
		"v1 schema":          func(v *RepositoryInspection) { v.SchemaVersion = 1 },
		"admission":          func(v *RepositoryInspection) { v.Spec.VerificationV2.AdmissionDigest = strings.Repeat("e", 64) },
		"binding":            func(v *RepositoryInspection) { v.Spec.VerificationV2.Dependencies.FileCount++ },
		"root":               func(v *RepositoryInspection) { v.Spec.VerificationV2.SourceRoot = "." },
		"test root":          func(v *RepositoryInspection) { v.Spec.VerificationV2.TestRoot = "test" },
		"support":            func(v *RepositoryInspection) { v.Spec.VerificationV2.TestSupportPrefixes = []string{"helpers"} },
		"protected list":     func(v *RepositoryInspection) { v.ProtectedInputs = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(v)
			var x RepositoryInspection
			json.Unmarshal(raw, &x)
			mutate(&x)
			x.Digest = RepositoryInspectionDigest(x)
			if ValidateRepositoryInspection(x) == nil {
				t.Fatal("accepted unrelated material")
			}
		})
	}
	before := RepositoryImportSpecDigest(v.Spec)
	v.Spec.VerificationV2 = nil
	if before == RepositoryImportSpecDigest(v.Spec) {
		t.Fatal("shared spec digest domain")
	}
	v = inspectionV2Fixture(t)
	v.Status = "blocked"
	v.Stage = "inputs"
	v.Code = ImportInputs
	v.FileCount = nil
	v.TotalBytes = nil
	v.ContextFileCount = nil
	v.ContextBytes = nil
	v.ContextManifest = nil
	v.ProtectedInputs = nil
	v.VerificationV2 = nil
	v.Digest = RepositoryInspectionDigest(v)
	if err := ValidateRepositoryInspection(v); err != nil {
		t.Fatal(err)
	}
}
func TestRepositoryInspectionV1BytesRemain(t *testing.T) {
	v := inspectionFixture()
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "verification_v2") {
		t.Fatal(string(raw))
	}
	var round RepositoryInspection
	json.Unmarshal(raw, &round)
	if !reflect.DeepEqual(v, round) {
		t.Fatal("v1 changed")
	}
}
func TestRepositoryInspectionV2IndependentRecordBound(t *testing.T) {
	v := inspectionV2Fixture(t)
	c := *v.VerificationV2
	for len(c.Protected) < 500 {
		p := "tests/" + strings.Repeat("a", 250) + "/" + strings.Repeat("b", 250) + "/" + strings.Repeat("c", 250) + "/" + fmt.Sprintf("%04d", len(c.Protected)) + strings.Repeat("d", 240)
		c.Protected = append(c.Protected, inputFile(p, "helper"))
	}
	sort.Slice(c.Protected, func(i, j int) bool { return c.Protected[i].Path < c.Protected[j].Path })
	var err error
	c, err = c.WithDigests()
	if err != nil {
		t.Fatal(err)
	}
	v.VerificationV2 = &c
	v.ProtectedInputs = []string{}
	for _, f := range c.Protected {
		v.ProtectedInputs = append(v.ProtectedInputs, f.Path)
	}
	n := 500
	b := int64(1000)
	v.FileCount = &n
	v.TotalBytes = &b
	v.Digest = RepositoryInspectionDigest(v)
	raw, _ := json.Marshal(v)
	if len(raw) <= MaxRepositoryInspectionBytes || len(raw) > MaxRepositoryInspectionBytesV2 {
		t.Fatal("fixture did not cross schema limits", len(raw))
	}
	if err = ValidateRepositoryInspection(v); err != nil {
		t.Fatal(err)
	}

	oversized := v
	supports := []string{}
	for i := 0; i < 450; i++ {
		supports = append(supports, "support/"+strings.Repeat("e", 250)+"/"+strings.Repeat("f", 250)+"/"+strings.Repeat("g", 250)+"/"+fmt.Sprintf("%04d", i)+strings.Repeat("h", 140))
	}
	selection := *v.Spec.VerificationV2
	selection.TestSupportPrefixes = supports
	oversized.Spec.VerificationV2 = &selection
	largeContract := c
	largeContract.ProtectedPrefixes = append(append([]string{}, c.ProtectedPrefixes...), supports...)
	sort.Strings(largeContract.ProtectedPrefixes)
	largeContract, err = largeContract.WithDigests()
	if err != nil {
		t.Fatal("component contract must fit its own limit", err)
	}
	oversized.VerificationV2 = &largeContract
	oversized.Spec.Scope = []string{}
	for i := 0; i < 250; i++ {
		oversized.Spec.Scope = append(oversized.Spec.Scope, fmt.Sprintf("scope%03d/", i)+strings.Repeat("a", 240)+"/"+strings.Repeat("b", 240)+"/"+strings.Repeat("c", 240)+"/"+strings.Repeat("d", 240))
	}
	oversized.Digest = RepositoryInspectionDigest(oversized)
	encoded, _ := json.Marshal(oversized)
	if len(encoded) <= MaxRepositoryInspectionBytesV2 {
		t.Fatal("fixture must exceed independent record limit", len(encoded))
	}
	if ValidateRepositoryInspection(oversized) == nil {
		t.Fatal("record bound relaxed by individually bounded components")
	}
	c.Protected = append(c.Protected, inputFile("tests/zzzz.txt", "helper"))
	if _, err = c.WithDigests(); err == nil {
		t.Fatal("contract independent bound relaxed")
	}
}
func TestV2CandidateRejectsNewUnsupportedLayout(t *testing.T) {
	c := sealedInput(t, 0)
	for _, name := range []string{"src/native.c", "src/package.json", "src/tsconfig.json", "src/node_modules/a.js", "go.mod"} {
		inventory := append([]C4InputFileV2{}, c.Protected...)
		inventory = append(inventory, inputFile(name, ""))
		sort.Slice(inventory, func(i, j int) bool { return inventory[i].Path < inventory[j].Path })
		if c.ValidateSourceInventory(inventory) == nil {
			t.Fatal("candidate introduced unsupported layout", name)
		}
	}
}

// Literal v1 expectations independently calculated from the base revision's
// serialization/domain definitions (top-level receipt keys only are sorted).
func TestRepositoryImportV1LiteralDigestGoldens(t *testing.T) {
	v := inspectionFixture()
	if got := RepositoryImportSpecDigest(v.Spec); got != "275c4dfe09a8c32bdd0e5c74440d079d80e3664af0156762a5ae05e495de07ac" {
		t.Fatal("v1 spec digest changed", got)
	}
	if v.Digest != "5275dd2cbbdb6b3f51f4fdd7a88516571ce2ab38254ac1618cccf7e3df60838a" {
		t.Fatal("v1 inspection digest changed", v.Digest)
	}
	s := v.Spec
	s.Protected = []string{}
	raw, _ := json.Marshal(s)
	if string(raw) != `{"scope":["file.txt"],"protected":[],"acceptance":["test -f file.txt"],"image":""}` {
		t.Fatal("v1 spec bytes changed", string(raw))
	}
}
