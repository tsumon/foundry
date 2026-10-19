package capability

import (
	"context"
	"errors"
	"foundry/internal/store"
	"path/filepath"
	"testing"
)

type memorySecrets map[string]string

func (s memorySecrets) Set(id, value string) error { s[id] = value; return nil }
func (s memorySecrets) Get(id string) (string, error) {
	v, ok := s[id]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}
func (s memorySecrets) Delete(id string) error { delete(s, id); return nil }

func TestSaveCredentialRotationAndEndpointIsolation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	keys := memorySecrets{}
	s := New(db, keys)
	c, err := s.SaveMCP(MCPInput{Name: "fixture", Endpoint: "http://127.0.0.1:9/mcp", Enabled: true, APIKey: "fake-key"})
	if err != nil {
		t.Fatal(err)
	}
	if keys[c.CredentialRef] != "fake-key" || c.CredentialRevision != 1 {
		t.Fatal(c)
	}
	_, err = s.SaveMCP(MCPInput{ID: c.ID, ExpectedRevision: 1, Name: c.Name, Endpoint: "http://127.0.0.1:10/mcp", Enabled: true})
	if err == nil {
		t.Fatal("endpoint inherited credential")
	}
	if _, err = s.Discover(context.Background(), c.ID, 1, "no-consent", false); err == nil {
		t.Fatal("dispatched without consent")
	}
}
