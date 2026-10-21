package model

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestProductionInferenceHTTPFailureCredentialEchoUnknown(t *testing.T) {
	for _, body := range []string{
		`synthetic-secret`,
		`{"error":{"message":"synthetic-secret","code":"invalid_api_key"}}`,
		`{"error":{"message":"synthetic-\u0073ecret","code":"invalid_api_key"}}`,
		`{"error":{"message":"synthetic-\u0073ecret","message":"discarded","code":"invalid_api_key"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			err := SubscriptionInferenceHTTPFailure(401, http.Header{"X-Request-Id": []string{"req_safe"}}, []byte(body), "synthetic-secret")
			var pe *ProviderError
			if !errors.As(err, &pe) || pe.Code != "response_incomplete" || pe.Certainty != "unknown" || pe.RequestID != "" || pe.ProviderCode != "" || pe.ProviderParam != "" {
				t.Fatalf("contaminated evidence classified: %+v", pe)
			}
		})
	}
}

func TestProductionInferenceHTTPFailureHeaderEchoUnknown(t *testing.T) {
	for _, header := range []http.Header{
		{"X-Request-Id": []string{"synthetic-secret"}},
		{"X-Diagnostic": []string{"echo synthetic-secret"}},
	} {
		err := SubscriptionInferenceHTTPFailure(401, header, []byte(`{"error":{"code":"invalid_api_key"}}`), "synthetic-secret")
		var pe *ProviderError
		if !errors.As(err, &pe) || pe.Code != "response_incomplete" || pe.Certainty != "unknown" || pe.RequestID != "" || pe.ProviderCode != "" {
			t.Fatalf("header contamination classified: %+v", pe)
		}
	}
}

const completedInference = "data: {\"type\":\"response.completed\",\"response\":{\"object\":\"response\",\"status\":\"completed\",\"model\":\"gpt-6.1-sol\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"FOUNDRY_INFERENCE_OK\"}]}]}}\n\n"

func TestProductionInferenceRejectsUnsafeStreamEvidence(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"tool before completion", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"exec\"}}\n\n" + completedInference},
		{"duplicate-key escaped echo", strings.ReplaceAll(completedInference, `"text":"FOUNDRY_INFERENCE_OK"`, `"text":"synthetic-\u0073ecret","text":"FOUNDRY_INFERENCE_OK"`)},
		{"unpaired surrogate", strings.ReplaceAll(completedInference, "FOUNDRY_INFERENCE_OK", `\ud800`)},
		{"wrong event", "event: response.failed\n" + completedInference},
		{"no event terminator", strings.TrimRight(completedInference, "\n")},
		{"incomplete", "data: {\"type\":\"response.incomplete\"}\n\n"},
		{"invalid utf8", strings.ReplaceAll(completedInference, "FOUNDRY_INFERENCE_OK", "\xff")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, e := DecodeSubscriptionInference(strings.NewReader(tc.body), "synthetic-secret")
			var pe *ProviderError
			if !errors.As(e, &pe) || pe.Code != "response_incomplete" || pe.Certainty != "unknown" || got.Text != "" {
				t.Fatalf("unsafe evidence accepted: %+v %v", got, e)
			}
		})
	}
}

func TestProductionInferenceMalformedFailureTerminalsUnknown(t *testing.T) {
	for _, body := range []string{
		`{"type":"response.failed"}`,
		`{"type":"response.failed","response":42}`,
		`{"type":"response.failed","response":null}`,
		`{"type":"response.failed","response":{"object":"response","status":"failed"}}`,
		`{"type":"response.failed","response":{"object":"response","status":"completed","error":{"code":"insufficient_quota"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"insufficient_quota"},"usage":{"input_tokens":9,"output_tokens":7}}}`,
		`{"type":"response.failed","response":{"object":"response","status":"failed","error":42}}`,
		`{"type":"response.failed","response":{"object":"response","status":"failed","error":{"code":42},"usage":{"input_tokens":9,"output_tokens":7}}}`,
		`{"type":"error"}`,
		`{"type":"error","error":null}`,
		`{"type":"error","error":{}}`,
		`{"type":"error","error":{"code":""}}`,
		`{"type":"error","error":{"code":"future_code","message":42}}`,
		`{"type":"error","error":{"code":"future_code","param":{}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			got, e := DecodeSubscriptionInference(strings.NewReader("data: "+body+"\n\n"), "synthetic-secret")
			var pe *ProviderError
			var terminal *SubscriptionInferenceFailure
			if !errors.As(e, &pe) || pe.Code != "response_incomplete" || pe.Certainty != "unknown" || errors.As(e, &terminal) || got != (Result{}) {
				t.Fatalf("malformed terminal classified: result=%+v error=%+v", got, e)
			}
		})
	}
}

func TestProductionInferenceValidUnfamiliarFailureRemainsFinite(t *testing.T) {
	for _, body := range []string{
		`{"type":"response.failed","response":{"object":"response","status":"failed","error":{"code":"future_code","message":"not retained"}}}`,
		`{"type":"error","error":{"code":"future_code","message":"not retained"}}`,
		`{"type":"error","code":"future_code","message":"not retained"}`,
	} {
		t.Run(body, func(t *testing.T) {
			_, e := DecodeSubscriptionInference(strings.NewReader("data: "+body+"\n\n"), "synthetic-secret")
			var pe *ProviderError
			var terminal *SubscriptionInferenceFailure
			if !errors.As(e, &pe) || pe.Code != "request_rejected" || pe.Certainty != "rejected" || pe.ProviderCode != "" || !errors.As(e, &terminal) {
				t.Fatalf("valid unfamiliar error lost finite rejection: %+v", e)
			}
		})
	}
}
