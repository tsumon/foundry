package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCLIRequiresExplicitFlagAndEmitsSingleSanitizedJSON(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{}, &out); code == 0 {
		t.Fatal("missing flag accepted")
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["passed"] != false || v["failure_code"] != "explicit_native_vm_acceptance_required" {
		t.Fatal(v)
	}
	out.Reset()
	if run([]string{"--unknown=/private/secret"}, &out) == 0 || bytes.Contains(out.Bytes(), []byte("secret")) {
		t.Fatal("unsanitized unknown flag")
	}
}

func TestCLIV2SelectionIsExplicitAndMalformedBundleNeverLaunches(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(root, "bundle")
	if e = os.Mkdir(bundle, 0700); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ profile, caseID, want string }{{"unknown", "workflow", "unsupported_v2_profile"}, {"c4_node_model_v2", "setup", "unsupported_v2_fixture_case"}, {"c4_node_model_v2", "workflow", "bundle_failed"}} {
		if tc.want == "bundle_failed" && (runtime.GOOS != "darwin" && runtime.GOOS != "windows") {
			tc.want = "unsupported_native_host"
		}
		var out bytes.Buffer
		data := filepath.Join(root, "new-"+tc.profile)
		code := run([]string{"--native-vm-acceptance", "--bundle", bundle, "--data-dir", data, "--profile", tc.profile, "--case", tc.caseID}, &out)
		var receipt map[string]any
		if code != 1 || json.Unmarshal(out.Bytes(), &receipt) != nil || receipt["failure_code"] != tc.want {
			t.Fatal(code, out.String())
		}
		if tc.want == "bundle_failed" {
			if receipt["schema_version"] != float64(2) {
				t.Fatal("v2 receipt downgraded")
			}
		} else if _, e = os.Stat(data); !os.IsNotExist(e) {
			t.Fatal("invalid selection had effects")
		}
	}
}
