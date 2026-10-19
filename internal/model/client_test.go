package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry/internal/domain"
)

type memorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memorySecrets) Set(id, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = value
	return nil
}
func (s *memorySecrets) Get(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[id]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}
func (s *memorySecrets) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

func TestCompleteChatCompletionsUsesDocumentedEnvelope(t *testing.T) {
	var gotPath, gotAuth string
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hello chat"}}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`)
	}))
	defer server.Close()
	secrets := &memorySecrets{m: map[string]string{"p1": "test-chat-secret"}}
	client := NewClient(secrets)
	result, err := client.Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "chat_completions", BaseURL: server.URL + "/v1", Model: "user-model", HasKey: true},
		Role:    domain.Planner, System: "system text", Prompt: "user text", MaxOutputTokens: 123,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello chat" || result.InputTokens != 11 || result.OutputTokens != 7 || !result.UsageKnown {
		t.Fatalf("result = %+v", result)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer test-chat-secret" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if got["model"] != "user-model" || got["max_completion_tokens"] != float64(123) {
		t.Fatalf("request = %#v", got)
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", got["messages"])
	}
}

func TestCompleteResponsesUsesDocumentedEnvelope(t *testing.T) {
	var gotPath string
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		fmt.Fprint(w, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello responses","annotations":[]}]}],"usage":{"input_tokens":5,"output_tokens":9,"total_tokens":14}}`)
	}))
	defer server.Close()
	client := NewClient(&memorySecrets{m: map[string]string{}})
	result, err := client.Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "configured-by-user"},
		Role:    domain.Researcher, System: "instructions", Prompt: "input", MaxOutputTokens: 321,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello responses" || result.InputTokens != 5 || result.OutputTokens != 9 || !result.UsageKnown {
		t.Fatalf("result = %+v", result)
	}
	if gotPath != "/v1/responses" || got["instructions"] != "instructions" || got["input"] != "input" || got["max_output_tokens"] != float64(321) || got["store"] != false {
		t.Fatalf("request path=%q body=%#v", gotPath, got)
	}
}

func TestCompleteDistinguishesZeroUsageFromMissingUsage(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		usageKnown bool
	}{
		{name: "present zero usage", response: `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":0,"output_tokens":0}}`, usageKnown: true},
		{name: "missing usage", response: `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`, usageKnown: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.response)
			}))
			defer server.Close()
			result, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(context.Background(), Request{
				Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m"}, MaxOutputTokens: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.UsageKnown != tt.usageKnown {
				t.Fatalf("UsageKnown = %v, want %v", result.UsageKnown, tt.usageKnown)
			}
		})
	}
}

func TestProbeListsModelsWithoutInference(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"z-model","object":"model"},{"id":"a-model","object":"model"}]}`)
	}))
	defer server.Close()
	models, err := NewClient(&memorySecrets{m: map[string]string{}}).Probe(context.Background(), domain.Profile{
		ID: "p1", Kind: "magpie", BaseURL: server.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || strings.Join(models, ",") != "z-model,a-model" {
		t.Fatalf("requests=%d models=%v", requests, models)
	}
}

func TestMagpieLoopbackUsesAttributionTokenWhenNoKey(t *testing.T) {
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()
	_, err := NewClient(&memorySecrets{m: map[string]string{}}).Probe(context.Background(), domain.Profile{
		ID: "magpie", Kind: "magpie", BaseURL: server.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer magpie-foundry" {
		t.Fatalf("authorization = %q", auth)
	}
}

func TestCompleteRejectsRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("redirect target must not be requested")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	_, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m"}, MaxOutputTokens: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("error = %v, want redirect rejection", err)
	}
}

func TestRedirectErrorDoesNotExposeLocation(t *testing.T) {
	const leaked = "credential-in-location"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost/next?token="+leaked, http.StatusFound)
	}))
	defer server.Close()
	_, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m"}, MaxOutputTokens: 10,
	})
	if err == nil {
		t.Fatal("redirect was accepted")
	}
	if strings.Contains(err.Error(), leaked) || strings.Contains(err.Error(), "token=") {
		t.Fatalf("redirect error exposed Location: %v", err)
	}
}

func TestProviderErrorRedactsBodyAndSecret(t *testing.T) {
	const secret = "credential-never-log"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"upstream leaked %s"}`, secret)
	}))
	defer server.Close()
	_, err := NewClient(&memorySecrets{m: map[string]string{"p1": secret}}).Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m", HasKey: true}, MaxOutputTokens: 10,
	})
	if err == nil {
		t.Fatal("provider error was accepted")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "upstream leaked") {
		t.Fatalf("error leaked provider content: %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error lacks status class: %v", err)
	}
}

func TestCompleteHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(ctx, Request{
			Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m"}, MaxOutputTokens: 10,
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not stop after cancellation")
	}
}

func TestCompletePreservesCancellationWhileReadingBody(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, strings.Repeat(" ", 1024*1024))
		w.(http.Flusher).Flush()
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(ctx, Request{
			Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m"}, MaxOutputTokens: 10,
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("response headers were not received")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("body read did not stop after cancellation")
	}
}

func TestCompleteRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 2*1024*1024+1))
	}))
	defer server.Close()
	_, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "responses", BaseURL: server.URL + "/v1", Model: "m"}, MaxOutputTokens: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want body limit rejection", err)
	}
}

func TestEndpointValidationRejectsUnsafeURLs(t *testing.T) {
	unsafe := []string{
		"http://example.com/v1",
		"https://user:pass@example.com/v1",
		"https://example.com/v1?token=secret",
		"https://example.com/v1#fragment",
		"file:///tmp/provider",
		"http://localhost.example/v1",
	}
	for _, baseURL := range unsafe {
		t.Run(baseURL, func(t *testing.T) {
			_, err := NewClient(&memorySecrets{m: map[string]string{}}).Probe(context.Background(), domain.Profile{ID: "p1", BaseURL: baseURL})
			if err == nil {
				t.Fatalf("unsafe URL %q was accepted", baseURL)
			}
		})
	}
}

func TestUnsupportedProtocolFailsBeforeNetwork(t *testing.T) {
	_, err := NewClient(&memorySecrets{m: map[string]string{}}).Complete(context.Background(), Request{
		Profile: domain.Profile{ID: "p1", Protocol: "guessed", BaseURL: "http://localhost:1/v1", Model: "m"}, MaxOutputTokens: 10,
	})
	if err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("error = %v, want unsupported protocol", err)
	}
}
