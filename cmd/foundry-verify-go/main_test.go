package main

import (
	"bytes"
	"context"
	"encoding/json"
	"foundry/internal/c4verify"
	"io"
	"testing"
)

type fixtureProcess struct {
	output string
	calls  int
}

func (p *fixtureProcess) Run(_ context.Context, r c4verify.Request, out, errout io.Writer) (int, error) {
	p.calls++
	if p.calls == 1 {
		io.WriteString(out, "go version go1.27.3 linux/arm64\n")
	} else {
		io.WriteString(out, p.output)
	}
	return 0, nil
}

func TestCommandEmitsVerifierOwnedSummaryAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		status       int
		class        string
	}{
		{"zero tests", "{\"Action\":\"pass\",\"Package\":\"p\"}\n", 2, "infrastructure_failed"},
		{"test pass", "{\"Action\":\"run\",\"Package\":\"p\",\"Test\":\"TestOK\"}\n{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"TestOK\"}\n{\"Action\":\"pass\",\"Package\":\"p\"}\n", 0, "passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			p := &fixtureProcess{output: tc.output}
			code := run(context.Background(), p, &out)
			var result c4verify.Result
			if json.Unmarshal(out.Bytes(), &result) != nil || code != tc.status || result.Class != tc.class || p.calls != 2 {
				t.Fatalf("unexpected status=%d output=%s", code, out.String())
			}
		})
	}
}
