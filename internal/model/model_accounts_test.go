package model

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

type forbiddenAccountSecrets struct{ t *testing.T }

func (s forbiddenAccountSecrets) Get(string) (string, error) {
	s.t.Error("account inference read secrets")
	return "fake", nil
}
func (s forbiddenAccountSecrets) Set(string, string) error {
	s.t.Error("account inference wrote secrets")
	return nil
}
func (s forbiddenAccountSecrets) Delete(string) error {
	s.t.Error("account inference deleted secrets")
	return nil
}

func TestAccountClientRejectsBeforeSecretsAndNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("account inference reached network")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	var p domain.Profile
	if err := json.Unmarshal([]byte(`{"id":"p","account_id":"a","model":"synthetic","protocol":"chat_completions","has_key":true,"base_url":"`+server.URL+`"}`), &p); err != nil {
		t.Fatal(err)
	}
	c := NewClient(forbiddenAccountSecrets{t})
	if _, err := c.Complete(context.Background(), Request{Profile: p, MaxOutputTokens: 1}); err == nil || err.Error() != "r5a_account_inference_disabled" {
		t.Errorf("Complete error=%v", err)
	}
	if _, err := c.Probe(context.Background(), p); err == nil || err.Error() != "r5a_account_inference_disabled" {
		t.Errorf("Probe error=%v", err)
	}
}
