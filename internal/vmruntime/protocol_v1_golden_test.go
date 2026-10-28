package vmruntime

import (
	"bytes"
	"foundry/internal/c4verify"
	"testing"
)

// Independent literal goldens pin the existing v1 transport/MAC/hash format.
// Values were calculated from the canonical byte records before any shared
// helper changes (this slice intentionally makes no v1 helper changes).
func TestV1WireMACAndSnapshotGoldens(t *testing.T) {
	q := probeRequest()
	var request, response bytes.Buffer
	if e := WriteRequest(&request, q); e != nil {
		t.Fatal(e)
	}
	if got := hashV2Test(request.Bytes()); got != "fa3daf8ef4a9396cac9aa2864dfa41ba10603640c16b5f688c209ecb2e863e4b" {
		t.Fatal("v1 request golden changed", got)
	}
	if e := WriteResponse(&response, q, goodChecks(), c4verify.Result{Class: "probe_passed"}); e != nil {
		t.Fatal(e)
	}
	if got := hashV2Test(response.Bytes()); got != "cd549ebe633fa127b1671ef54dc2ec773660945adba472c1bb0c81f838fb5d23" {
		t.Fatal("v1 response golden changed", got)
	}
	r, e := ReadResponse(&response, q)
	if e != nil || r.MAC != "9b2965b5918efb18e16306949e59d83928e3cf0f041083856e1061bcca34f3d7" {
		t.Fatal("v1 MAC golden changed", e)
	}
	digest, e := SnapshotDigest([]SnapshotFile{{Path: "main.go", Mode: 0644, Data: []byte("package main\n")}})
	if e != nil || digest != "0ca01613f72c40e13d9da5309db189469114234a8b5a3474be5e67bd6fb3aee6" {
		t.Fatal("v1 snapshot golden changed", e, digest)
	}
}
