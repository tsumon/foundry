package main

import (
	"bytes"
	"encoding/json"
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
