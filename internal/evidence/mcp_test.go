package evidence

import (
	"strings"
	"testing"
)

func TestMCPResultIsBoundedUntruncatedEvidence(t *testing.T) {
	s := New(privateTestRoot(t))
	body := []byte(`{"content":[]}`)
	envelope := testEnvelope(body)
	envelope.Kind = "mcp_result"
	sealed, err := s.Put(envelope, body)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Read(sealed); err != nil || string(got) != string(body) {
		t.Fatal(string(got), err)
	}
	envelope.Truncated = true
	if _, err = s.Put(envelope, body); err == nil {
		t.Fatal("truncated MCP observation accepted")
	}
	envelope.Truncated = false
	envelope.Digest = ""
	envelope.Bytes = 0
	if _, err = s.Put(envelope, []byte(`"`+strings.Repeat("x", 64<<10)+`"`)); err == nil {
		t.Fatal("oversized MCP observation accepted")
	}
}
