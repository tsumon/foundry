package dependencyadmission

import "testing"

func TestPythonNumericConstraints(t *testing.T) {
	for _, tc := range []struct {
		spec, version string
		want          bool
		invalid       bool
	}{
		{">=1.2, <2", "1.9.0", true, false},
		{"==1.2.*", "1.2.0", true, false},
		{"!=1.2.*", "1.2.0", false, false},
		{"~=1.4.5", "1.5", false, false},
		{"~=1.4", "1.9", true, false},
		{"==1.2", "1.2.0", true, false},
		{"===1.2", "1.2", false, true},
		{"~=1", "1", false, true},
		{">=01.2", "1.2", false, true},
	} {
		got, err := pythonSatisfies(tc.spec, tc.version)
		if (err != nil) != tc.invalid || err == nil && got != tc.want {
			t.Errorf("%q %q: got %v, %v", tc.spec, tc.version, got, err)
		}
	}
}

func TestPythonRequirementMarkers(t *testing.T) {
	for _, tc := range []struct {
		input              string
		extras             []string
		name, spec, status string
		invalid            bool
	}{
		{"Foo_Bar (>=1.2, <2)", nil, "foo-bar", ">=1.2, <2", "active", false},
		{"doc-helper (>=1rc1); (extra == 'docs')", []string{"docs"}, "doc-helper", ">=1rc1", "inactive_extra", false},
		{"foo; extra == 'docs'", nil, "", "", "", true},
		{"foo; python_version > '3'", nil, "", "", "", true},
		{"foo[bar]", nil, "", "", "", true},
	} {
		n, s, status, err := pythonRequirement(tc.input, tc.extras)
		if (err != nil) != tc.invalid || err == nil && (n != tc.name || s != tc.spec || status != tc.status) {
			t.Errorf("%q: %q %q %q %v", tc.input, n, s, status, err)
		}
	}
}

func TestPythonInactiveStillRequiresPredicateSyntax(t *testing.T) {
	for _, s := range []string{"foo arbitrary text; extra == 'docs'", "foo >=; extra == 'docs'", "foo >=1,,<2; extra == 'docs'", "foo =1; extra == 'docs'"} {
		if _, _, _, e := pythonRequirement(s, []string{"docs"}); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
