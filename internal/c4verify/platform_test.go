package c4verify

import (
	"context"
	"testing"
)

func TestRunForPlatformExactVersionAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		platform, version, class string
		calls                    int
	}{
		{"linux/amd64", "go version go1.27.3 linux/amd64\n", "passed", 2},
		{"linux/arm64", "go version go1.27.3 linux/arm64\n", "passed", 2},
		{"linux/amd64", "go version go1.27.3 linux/arm64\n", "infrastructure_failed", 1},
		{"linux/amd64", "go version go1.27.2 linux/amd64\n", "infrastructure_failed", 1},
		{"darwin/arm64", "go version go1.27.3 darwin/arm64\n", "infrastructure_failed", 0},
		{"", "go version go1.27.3 linux/arm64\n", "infrastructure_failed", 0},
	} {
		f := &processFixture{t: t, version: tc.version, output: passingEvents}
		r := RunForPlatform(context.Background(), f, tc.platform)
		if r.Class != tc.class || f.calls != tc.calls {
			t.Fatalf("%s: %+v calls=%d", tc.platform, r, f.calls)
		}
	}
	f := &processFixture{t: t, version: "go version go1.27.3 linux/amd64\n", output: passingEvents}
	if r := Run(context.Background(), f); r.Class != "infrastructure_failed" {
		t.Fatal("legacy platform changed", r)
	}
}
