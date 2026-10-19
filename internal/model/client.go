package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"foundry/internal/domain"
)

const maxResponseBytes = 2 * 1024 * 1024

var errProviderRedirect = errors.New("provider redirects are disabled")
var ErrAccountInferenceDisabled = errors.New("r5a_account_inference_disabled")

// AccountProfilePreflighter is explicit opt-in for synthetic gateways. The
// production Client never enables account inference in R5A.
type AccountProfilePreflighter interface {
	PreflightAccountProfile(context.Context, domain.Profile) error
}

func (c *Client) PreflightAccountProfile(_ context.Context, profile domain.Profile) error {
	if profile.AccountID != "" {
		return ErrAccountInferenceDisabled
	}
	return nil
}

type Request struct {
	SnapshotSchema  int
	Selection       *domain.RoleModelSelection
	Profile         domain.Profile
	Role            domain.Role
	System          string
	Prompt          string
	MaxOutputTokens int
}

type Result struct {
	RequestID            string
	ResolvedModel        string
	CredentialGeneration int64
	Text                 string
	InputTokens          int64
	OutputTokens         int64
	UsageKnown           bool
}

type Gateway interface {
	Complete(context.Context, Request) (Result, error)
}

type Client struct {
	secrets Secrets
	http    *http.Client
}

func NewClient(secrets Secrets) *Client {
	return &Client{
		secrets: secrets,
		http: &http.Client{
			Timeout: 90 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errProviderRedirect
			},
		},
	}
}

func (c *Client) Complete(ctx context.Context, request Request) (Result, error) {
	if err := c.PreflightAccountProfile(ctx, request.Profile); err != nil {
		return Result{}, err
	}
	if request.MaxOutputTokens < 1 {
		return Result{}, errors.New("max output tokens must be positive")
	}
	if strings.TrimSpace(request.Profile.Model) == "" {
		return Result{}, errors.New("provider model is required")
	}

	var endpoint string
	var payload any
	switch request.Profile.Protocol {
	case "chat_completions":
		endpoint = "/chat/completions"
		payload = chatRequest{
			Model: request.Profile.Model,
			Messages: []chatMessage{
				{Role: "system", Content: request.System},
				{Role: "user", Content: request.Prompt},
			},
			MaxCompletionTokens: request.MaxOutputTokens,
		}
	case "responses":
		endpoint = "/responses"
		payload = responsesRequest{
			Model: request.Profile.Model, Instructions: request.System, Input: request.Prompt,
			MaxOutputTokens: request.MaxOutputTokens, Store: false,
		}
	default:
		return Result{}, fmt.Errorf("unsupported provider protocol %q", request.Profile.Protocol)
	}

	responseBody, err := c.do(ctx, request.Profile, http.MethodPost, endpoint, payload)
	if err != nil {
		return Result{}, err
	}
	if request.Profile.Protocol == "chat_completions" {
		return decodeChatResponse(responseBody)
	}
	return decodeResponsesResponse(responseBody)
}

func (c *Client) Probe(ctx context.Context, profile domain.Profile) ([]string, error) {
	if err := c.PreflightAccountProfile(ctx, profile); err != nil {
		return nil, err
	}
	body, err := c.do(ctx, profile, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	var envelope modelsResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("provider returned an invalid models response")
	}
	models := make([]string, 0, len(envelope.Data))
	for _, model := range envelope.Data {
		if strings.TrimSpace(model.ID) != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

func (c *Client) do(ctx context.Context, profile domain.Profile, method, endpoint string, payload any) ([]byte, error) {
	providerURL, loopback, err := buildEndpoint(profile.BaseURL, endpoint)
	if err != nil {
		return nil, err
	}
	credential, err := c.credential(profile, loopback)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("encode provider request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, providerURL, body)
	if err != nil {
		return nil, errors.New("create provider request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Foundry/0.1.0")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, errProviderRedirect) {
			return nil, errProviderRedirect
		}
		return nil, errors.New("provider request failed (transport_error)")
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider request failed with status %d (%s)", response.StatusCode, statusClass(response.StatusCode))
	}
	return responseBody, nil
}

func (c *Client) credential(profile domain.Profile, loopback bool) (string, error) {
	if profile.HasKey {
		if c.secrets == nil {
			return "", errors.New("provider credential storage is unavailable")
		}
		secret, err := c.secrets.Get(profile.ID)
		if err != nil || secret == "" {
			return "", errors.New("provider credential is unavailable")
		}
		return secret, nil
	}
	if profile.Kind == "magpie" && loopback {
		return "magpie-foundry", nil
	}
	if !loopback {
		return "", errors.New("remote provider requires a credential")
	}
	return "", nil
}

func buildEndpoint(baseURL, suffix string) (string, bool, error) {
	u, loopback, err := domain.ParseProviderBaseURL(baseURL)
	if err != nil {
		return "", false, err
	}
	basePath := strings.TrimSuffix(u.EscapedPath(), "/")
	if basePath == "" {
		basePath = "/v1"
	}
	u.RawPath = ""
	u.Path = basePath + suffix
	return u.String(), loopback, nil
}

func readBounded(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("read provider response")
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("provider response is too large")
	}
	return body, nil
}

func decodeChatResponse(body []byte) (Result, error) {
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil || len(response.Choices) == 0 {
		return Result{}, errors.New("provider returned an invalid chat completions response")
	}
	text := response.Choices[0].Message.Content
	if text == "" {
		return Result{}, errors.New("provider returned an empty chat completions response")
	}
	result := Result{Text: text}
	if response.Usage != nil && validUsage(response.Usage.PromptTokens, response.Usage.CompletionTokens) {
		result.InputTokens = *response.Usage.PromptTokens
		result.OutputTokens = *response.Usage.CompletionTokens
		result.UsageKnown = true
	}
	return result, nil
}

func decodeResponsesResponse(body []byte) (Result, error) {
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return Result{}, errors.New("provider returned an invalid responses response")
	}
	var text strings.Builder
	for _, output := range response.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	if text.Len() == 0 {
		return Result{}, errors.New("provider returned an empty responses response")
	}
	result := Result{Text: text.String()}
	if response.Usage != nil && validUsage(response.Usage.InputTokens, response.Usage.OutputTokens) {
		result.InputTokens = *response.Usage.InputTokens
		result.OutputTokens = *response.Usage.OutputTokens
		result.UsageKnown = true
	}
	return result, nil
}

func statusClass(status int) string {
	switch {
	case status >= 400 && status < 500:
		return "client_error"
	case status >= 500:
		return "server_error"
	default:
		return "unexpected_status"
	}
}

func validUsage(input, output *int64) bool {
	return input != nil && output != nil && *input >= 0 && *output >= 0
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	MaxCompletionTokens int           `json:"max_completion_tokens"`
}

type responsesRequest struct {
	Model           string `json:"model"`
	Instructions    string `json:"instructions"`
	Input           string `json:"input"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Store           bool   `json:"store"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

type chatUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
}

type responsesResponse struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage *responsesUsage `json:"usage"`
}

type responsesUsage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}
