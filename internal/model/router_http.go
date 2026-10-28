package model

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptrace"
	"reflect"
	"strings"

	"foundry/internal/domain"
)

type codingRequestKey struct{}

func (r *Router) checkCodingSend(ctx context.Context, s domain.RoleModelSelection, method, endpoint string, encoded []byte) error {
	if r.codingAdmission == nil {
		return nil
	}
	q, ok := ctx.Value(codingRequestKey{}).(Request)
	if !ok || q.Selection == nil || !reflect.DeepEqual(*q.Selection, s) || method != http.MethodPost || ctx.Err() != nil {
		return providerFailure("request_rejected", "not_sent")
	}
	wantEndpoint, payload := routedPayload(q, s.Route.Adapter)
	want, err := json.Marshal(payload)
	if err != nil || endpoint != wantEndpoint || !bytes.Equal(want, encoded) || r.codingAdmission(ctx, q) != nil {
		return providerFailure("request_rejected", "not_sent")
	}
	return nil
}

func routedPayload(r Request, adapter string) (string, any) {
	switch adapter {
	case "anthropic_api_messages":
		return "/messages", map[string]any{"model": r.Profile.Model, "system": r.System, "messages": []chatMessage{{Role: "user", Content: r.Prompt}}, "max_tokens": r.MaxOutputTokens}
	case "openai_siwc_responses":
		return "/responses", map[string]any{"model": r.Profile.Model, "instructions": r.System, "input": []chatMessage{{Role: "user", Content: r.Prompt}}, "stream": true, "store": false}
	case "openai_api_responses":
		return "/responses", map[string]any{"model": r.Profile.Model, "instructions": r.System, "input": []chatMessage{{Role: "user", Content: r.Prompt}}, "max_output_tokens": r.MaxOutputTokens, "store": false}
	default:
		key := "max_tokens"
		if adapter == "openai_api_chat" {
			key = "max_completion_tokens"
		}
		return "/chat/completions", map[string]any{"model": r.Profile.Model, "messages": []chatMessage{{Role: "system", Content: r.System}, {Role: "user", Content: r.Prompt}}, key: r.MaxOutputTokens}
	}
}

func (r *Router) send(ctx context.Context, s domain.RoleModelSelection, method, endpoint string, payload any) (reply *routedHTTPResponse, generation int64, sendErr error) {
	if ctx.Err() != nil {
		return nil, 0, providerFailure("request_rejected", "not_sent")
	}
	if s.Route.Adapter == "openai_siwc_responses" && !validSIWCRequestPair(method, endpoint) {
		return nil, 0, providerFailure("request_rejected", "not_sent")
	}
	var encoded []byte
	if payload != nil {
		var err error
		encoded, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, providerFailure("request_rejected", "not_sent")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(s.Route.BaseURL, "/")+endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, providerFailure("request_rejected", "not_sent")
	}
	if r.resolver == nil {
		return nil, 0, providerFailure("auth_invalid", "not_sent")
	}
	if err := r.checkCodingSend(ctx, s, method, endpoint, encoded); err != nil {
		return nil, 0, err
	}
	lease, err := r.resolver.ResolveModelCredential(ctx, s)
	if err != nil {
		return nil, 0, sanitizedResolverError(err)
	}
	if lease.Secret == "" || strings.ContainsAny(lease.Secret, "\r\n") {
		return nil, lease.Generation, providerFailure("auth_invalid", "not_sent")
	}
	req.Header.Set("User-Agent", "Foundry/0.1.0")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.Route.Adapter == "anthropic_api_messages" {
		req.Header.Set("x-api-key", lease.Secret)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+lease.Secret)
	}
	if s.Route.Adapter == "openai_siwc_responses" && method == http.MethodPost {
		req.Header.Set("Accept", "text/event-stream")
	}
	if err := r.checkCodingSend(ctx, s, method, endpoint, encoded); err != nil {
		return nil, lease.Generation, err
	}
	var observer *exchangeObserver
	if s.Route.Adapter == "openai_siwc_responses" && method == http.MethodPost {
		observer = newExchangeObserver(r.http.Transport)
		req.Header.Set("X-Client-Request-Id", observer.d.ClientRequestID)
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), observer.trace()))
	}
	response, err := r.http.Do(req)
	exchange := observer.finish(response)
	defer func() {
		if p, ok := sendErr.(*ProviderError); ok && exchange.Valid() {
			p.ExchangeDiagnostic = exchange
		}
	}()
	if err != nil {
		return nil, lease.Generation, providerFailure("transport_unknown", "unknown")
	}
	var transport domain.HTTPTransportDiagnostic
	if s.Route.Adapter == "openai_siwc_responses" && method == http.MethodPost && response.StatusCode == http.StatusOK {
		transport = observeHTTPTransport(response)
	}
	// A hostile provider can echo credentials into otherwise valid ID fields.
	for _, name := range []string{"X-Request-ID", "Request-ID"} {
		if strings.Contains(response.Header.Get(name), lease.Secret) {
			response.Header.Del(name)
		}
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		// Accepted/partial success and unhandled 3xx envelopes do not prove
		// rejection. Never promote body error strings into known-failure evidence.
		if response.StatusCode < http.StatusBadRequest {
			failure := providerFailure("response_incomplete", "unknown")
			failure.HTTPStatus = response.StatusCode
			failure.RequestID = safeIdentifier(response.Header.Get("X-Request-ID"))
			if failure.RequestID == "" {
				failure.RequestID = safeIdentifier(response.Header.Get("Request-ID"))
			}
			return nil, lease.Generation, failure
		}
		body, err := readBounded(response.Body)
		if err != nil {
			return nil, lease.Generation, providerFailure("transport_unknown", "unknown")
		}
		failure := classifyProviderError(response.StatusCode, response.Header, body)
		removeCredentialErrorEchoes(response.Request, failure)
		return nil, lease.Generation, failure
	}
	return &routedHTTPResponse{Response: response, transport: transport, exchange: exchange}, lease.Generation, nil
}

func validSIWCRequestPair(method, endpoint string) bool {
	if method == http.MethodGet && endpoint == "/models" {
		return true
	}
	return method == http.MethodPost && endpoint == "/responses"
}

func (r *Router) decodeResponse(response *http.Response, adapter string) (result Result, err error) {
	defer func() {
		if p, ok := err.(*ProviderError); ok {
			removeCredentialErrorEchoes(response.Request, p)
			p.HTTPStatus = response.StatusCode
			if p.RequestID == "" {
				p.RequestID = safeIdentifier(response.Header.Get("X-Request-ID"))
				if p.RequestID == "" {
					p.RequestID = safeIdentifier(response.Header.Get("Request-ID"))
				}
			}
		}
	}()
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	want := "application/json"
	if adapter == "openai_siwc_responses" {
		want = "text/event-stream"
	}
	// Header.Get only returns the first value. Duplicate media fields are
	// ambiguous even if their first value (or both values) claims to be SSE.
	multipleMedia := adapter == "openai_siwc_responses" && len(response.Header.Values("Content-Type")) > 1
	if err != nil || media != want || multipleMedia {
		p := providerFailure("response_incomplete", "unknown")
		if adapter == "openai_siwc_responses" {
			d := domain.ResponseDiagnostic{Version: 1, Stage: "http_media", Reason: "unexpected_media_type", MediaType: "other"}
			if media == "application/json" {
				d.MediaType = "json"
			}
			if err != nil || multipleMedia {
				d.Reason, d.MediaType = "invalid_media_type", "invalid"
			}
			if !multipleMedia && response.Header.Get("Content-Type") == "" {
				d.Reason, d.MediaType = "missing_media_type", "missing"
			}
			p.ResponseDiagnostic = d
		}
		return Result{}, p
	}
	var observed domain.ResponseDiagnostic
	if adapter == "openai_siwc_responses" {
		result, err = decodeSIWCStreamObserved(response.Body, response.Header, &observed)
	} else {
		var body []byte
		body, err = readBounded(response.Body)
		if err != nil {
			return Result{}, providerFailure("response_incomplete", "unknown")
		}
		switch adapter {
		case "anthropic_api_messages":
			result, err = decodeMessages(body)
		case "openai_api_responses":
			result, err = decodeRoutedResponses(body)
		default:
			result, err = decodeRoutedChat(body)
		}
	}
	id := safeIdentifier(response.Header.Get("X-Request-ID"))
	if id == "" {
		id = safeIdentifier(response.Header.Get("Request-ID"))
	}
	if err != nil {
		if p, ok := err.(*ProviderError); ok && p.RequestID == "" {
			p.RequestID = id
		}
		return Result{}, err
	}
	// This exact leased credential must not enter sealed model evidence. This
	// is intentionally not general secret detection or rewriting model output.
	if echoesRequestCredential(response.Request, result.Text) {
		p := providerFailure("response_incomplete", "unknown")
		if adapter == "openai_siwc_responses" {
			observed.Stage, observed.Reason = "output_guard", "credential_echo"
			p.ResponseDiagnostic = observed
		}
		return Result{}, p
	}
	result.RequestID = id
	if echoesRequestCredential(response.Request, result.ResolvedModel) {
		result.ResolvedModel = ""
	}
	return result, nil
}

func removeCredentialErrorEchoes(request *http.Request, failure *ProviderError) {
	if echoesRequestCredential(request, failure.ProviderCode) {
		failure.ProviderCode = ""
	}
	if echoesRequestCredential(request, failure.ProviderParam) {
		failure.ProviderParam = ""
	}
}

func echoesRequestCredential(request *http.Request, values ...string) bool {
	if request == nil {
		return false
	}
	secret := request.Header.Get("x-api-key")
	if secret == "" {
		secret = strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	}
	if secret == "" {
		return false
	}
	for _, value := range values {
		if strings.Contains(value, secret) {
			return true
		}
	}
	return false
}
