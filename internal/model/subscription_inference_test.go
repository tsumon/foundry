package model

import (
	"errors"
	"strings"
	"testing"
)

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
