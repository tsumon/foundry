package vmguest

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
)

func TestV2ChildContractClosed(t *testing.T) {
	c := childContractV2{SchemaVersion: 1, Phase: testNodeV2, SourceRoots: []string{"."}, TestFiles: []string{"tests/a.test.js"}, InputFiles: []string{}, SourceRoot: "/workspace", OutputRoot: ""}
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = parseChildContractV2(b, testNodeV2); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{strings.Replace(string(b), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), strings.Replace(string(b), `"input_files":[]`, `"input_files":null`, 1), strings.Replace(string(b), `tests/a.test.js`, `../bad`, 1), strings.Replace(string(b), `"output_root":""`, `"output_root":"/tmp"`, 1), strings.Replace(string(b), `"phase":"test-node"`, `"phase":"/bin/sh"`, 1), string(b[:len(b)-1]) + `,"secret":"bad"}`} {
		if _, e := parseChildContractV2([]byte(raw), testNodeV2); e == nil {
			t.Fatal("unsafe contract", raw)
		}
	}
	if _, e := parseChildContractV2(b, testPythonV2); e == nil {
		t.Fatal("phase mixing")
	}
}
func identityV2() domain.C4VMIdentity {
	return domain.C4VMIdentity{SchemaVersion: 2, HostPlatform: "windows/amd64", GuestPlatform: "linux/amd64", Protocol: "foundry-vm-v2", PolicyVersion: "multilang-vm-v2", ManifestDigest: strings.Repeat("1", 64), RuntimeDigest: strings.Repeat("2", 64), KernelDigest: strings.Repeat("3", 64), RootFSDigest: strings.Repeat("4", 64), VerifierDigest: strings.Repeat("5", 64)}
}
