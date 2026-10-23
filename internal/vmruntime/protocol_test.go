package vmruntime

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"foundry/internal/c4verify"
	"strings"
	"testing"
)

func probeRequest() WireRequest {
	return WireRequest{SchemaVersion: 1, Action: "probe", Nonce: strings.Repeat("a", 64), Secret: strings.Repeat("b", 64), GuestPlatform: "linux/arm64", Files: []SnapshotFile{}}
}
func goodChecks() SelfCheck { return SelfCheck{true, true, true, true, true, true, true, true} }
func rawFrame(b []byte) []byte {
	out := []byte(Header)
	out = binary.BigEndian.AppendUint32(out, uint32(len(b)))
	return append(out, b...)
}
func TestProtocolRoundTripAndForgery(t *testing.T) {
	q := probeRequest()
	var b bytes.Buffer
	if err := WriteRequest(&b, q); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequest(&b)
	if err != nil || got.Nonce != q.Nonce {
		t.Fatalf("request: %+v %v", got, err)
	}
	if err := WriteResponse(&b, q, goodChecks(), c4verify.Result{Class: "probe_passed"}); err != nil {
		t.Fatal(err)
	}
	valid := append([]byte(nil), b.Bytes()...)
	if _, err := ReadResponse(bytes.NewReader(valid), q); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"nonce", "mac", "checks", "partial", "oversize", "unknown", "duplicate-key"} {
		t.Run(kind, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			var v map[string]any
			json.Unmarshal(data[len(Header)+4:], &v)
			switch kind {
			case "nonce":
				v["nonce"] = strings.Repeat("c", 64)
			case "mac":
				v["mac"] = strings.Repeat("0", 64)
			case "checks":
				v["checks"] = SelfCheck{}
			case "unknown":
				v["extra"] = true
			case "partial":
				data = data[:len(data)-1]
			case "oversize":
				binary.BigEndian.PutUint32(data[len(Header):], uint32(MaxResponseBytes+1))
			case "duplicate-key":
				data = rawFrame([]byte(`{"schema_version":1,"schema_version":1}`))
			}
			if kind == "nonce" || kind == "mac" || kind == "checks" || kind == "unknown" {
				j, _ := json.Marshal(v)
				data = rawFrame(j)
			}
			if _, err := ReadResponse(bytes.NewReader(data), q); err == nil {
				t.Fatal("invalid authenticated frame accepted")
			}
		})
	}
}
func TestSnapshotAdmission(t *testing.T) {
	files := []SnapshotFile{{Path: "main.go", Mode: 0644, Data: []byte("package main\n")}}
	digest, err := SnapshotDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	q := probeRequest()
	q.Action = "verify"
	q.Files = files
	q.CandidateDigest = digest
	if err := ValidateRequest(q); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../x", "/x", "a\\b", ".git/config", "a/../b", ".env", "A:/x", "a\x00b"} {
		t.Run(p, func(t *testing.T) {
			bad := q
			bad.Files = []SnapshotFile{{Path: p, Mode: 0644, Data: []byte("x")}}
			if ValidateRequest(bad) == nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
	q.Files = append(q.Files, q.Files[0])
	if ValidateRequest(q) == nil {
		t.Fatal("duplicate file accepted")
	}
}

func TestResponseRejectsZeroPartialAndOversizedEvidence(t *testing.T) {
	q := probeRequest()
	q.Action = "verify"
	q.Files = []SnapshotFile{{Path: "go.mod", Mode: 0644, Data: []byte("module fixture\n")}}
	q.CandidateDigest, _ = SnapshotDigest(q.Files)
	for _, result := range []c4verify.Result{
		{Class: "passed", Tests: 0}, {Class: "passed", Tests: 1, Truncated: true},
		{Class: "passed", Tests: 1, ExitCode: 1}, {Class: "test_failed", Tests: 1, ExitCode: 0},
		{Class: "test_failed", Tests: 1, ExitCode: 125}, {Class: "test_failed", Tests: 1, ExitCode: 1, Truncated: true},
		{Class: "passed", Tests: 1, Output: strings.Repeat("x", 65537)},
	} {
		var b bytes.Buffer
		if e := WriteResponse(&b, q, goodChecks(), result); e != nil {
			continue
		}
		if _, e := ReadResponse(&b, q); e == nil {
			t.Fatalf("unsafe evidence accepted: %+v", result)
		}
	}
}

func TestRequestRejectsUnknownMissingAliasedAndDuplicateFields(t *testing.T) {
	q := probeRequest()
	b, _ := json.Marshal(q)
	for _, mutated := range []string{
		strings.Replace(string(b), `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(string(b), `"action":"probe"`, `"action":"shell"`, 1),
		strings.Replace(string(b), `"nonce":`, `"Nonce":`, 1),
		strings.Replace(string(b), `"candidate_digest":"",`, "", 1),
		strings.Replace(string(b), `"action":"probe"`, `"action":"probe","action":"verify"`, 1),
		string(b) + "{}",
	} {
		if _, e := ReadRequest(bytes.NewReader(rawFrame([]byte(mutated)))); e == nil {
			t.Fatal("unsafe request accepted")
		}
	}
}

func TestResponseRejectsNullScalarEvenWithCanonicalMAC(t *testing.T) {
	q := probeRequest()
	var b bytes.Buffer
	WriteResponse(&b, q, goodChecks(), c4verify.Result{Class: "probe_passed"})
	j := b.Bytes()[len(Header)+4:]
	j = bytes.Replace(j, []byte(`"output":""`), []byte(`"output":null`), 1)
	if _, e := ReadResponse(bytes.NewReader(rawFrame(j)), q); e == nil {
		t.Fatal("null scalar accepted as canonical empty string")
	}
}
