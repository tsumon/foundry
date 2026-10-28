package dependencyadmission

import "testing"

func TestPythonLockStrict(t *testing.T) {
	good := `{"schema_version":1,"python":"3.13.5","selected_extras":[],"roots":[],"wheels":[]}`
	if _, err := parsePythonLock([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{"schema_version":1,"python":"3.13.18","selected_extras":[],"roots":[],"wheels":[]}`,
		`{"schema_version":1,"python":"3.13.5","selected_extras":null,"roots":[],"wheels":[]}`,
		`{"schema_version":1,"python":"3.13.5","selected_extras":[],"roots":[],"wheels":[],"extra":1}`,
		`{"SCHEMA_VERSION":1,"python":"3.13.5","selected_extras":[],"roots":[],"wheels":[]}`,
		`{"schema_version":1,"python":"3.13.5","selected_extras":[],"roots":[{"name":"My_Pkg","version":"1"}],"wheels":[]}`,
	} {
		if _, err := parsePythonLock([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}
