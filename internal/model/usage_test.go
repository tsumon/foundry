package model

import (
	"fmt"
	"testing"
)

// Missing or malformed counters must not turn an unknown bill into a known zero.
func TestUsageRequiresBothValidCounters(t *testing.T) {
	for _, protocol := range []string{"chat", "responses"} {
		for _, tc := range []struct {
			name, usage string
			known       bool
		}{
			{"empty", `{}`, false}, {"missing output", `{"%s":2}`, false},
			{"missing input", `{"%s":2}`, false},
			{"null input", `{"%s":null,"%s":2}`, false},
			{"null output", `{"%s":2,"%s":null}`, false},
			{"zero", `{"%s":0,"%s":0}`, true},
			{"negative", `{"%s":-1,"%s":2}`, false},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				in, out := "input_tokens", "output_tokens"
				decode := decodeResponsesResponse
				envelope := `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":%s}`
				if protocol == "chat" {
					in, out = "prompt_tokens", "completion_tokens"
					decode = decodeChatResponse
					envelope = `{"choices":[{"message":{"content":"ok"}}],"usage":%s}`
				}
				usage := tc.usage
				if tc.name == "missing output" {
					usage = fmt.Sprintf(usage, in)
				} else if tc.name == "missing input" {
					usage = fmt.Sprintf(usage, out)
				} else if tc.name != "empty" {
					usage = fmt.Sprintf(usage, in, out)
				}
				r, err := decode([]byte(fmt.Sprintf(envelope, usage)))
				if err != nil {
					t.Fatal(err)
				}
				if r.UsageKnown != tc.known {
					t.Fatalf("UsageKnown=%v want %v", r.UsageKnown, tc.known)
				}
			})
		}
	}
}
