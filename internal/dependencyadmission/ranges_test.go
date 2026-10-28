package dependencyadmission

import (
	"strings"
	"testing"
)

// Numeric fixtures follow npm/node-semver's documented range expansions. They
// test our restricted boundary, including pre-1.0 carets and partial endpoints.
func TestSupportedRanges(t *testing.T) {
	for _, x := range []struct {
		spec, version string
		want          bool
	}{
		{"1.2.3", "1.2.3", true}, {"=1.2.3", "1.2.4", false}, {"= 1.2.3", "1.2.3", true},
		{"^1", "1.9.9", true}, {"^1.2", "2.0.0", false}, {"^1.2.3", "1.3.0", true},
		{"^0.2.3", "0.3.0", false}, {"^0.0.3", "0.0.4", false}, {"^0.0", "0.0.9", true}, {"^0", "0.9.0", true},
		{"~1", "1.9.0", true}, {"~1.2", "1.3.0", false}, {"~1.2.3", "1.2.9", true},
		{">1", "1.9.0", false}, {">1.2", "1.3.0", true}, {">1.2.3", "1.2.4", true},
		{">=1", "1.0.0", true}, {">=1.2", "1.2.0", true}, {">=1.2.3", "1.2.2", false},
		{"<2", "1.9.9", true}, {"<1.2", "1.2.0", false}, {"<1.2.3", "1.2.2", true},
		{"<=1", "1.9.0", true}, {"<=1.2", "1.2.9", true}, {"<=1.2.3", "1.2.4", false},
		{">= 1.2 < 2 || ^3.1.0", "3.2.0", true}, {">=1 <2 || >=3 <4", "2.5.0", false},
	} {
		t.Run(x.spec+"/"+x.version, func(t *testing.T) {
			got, e := satisfies(x.spec, x.version)
			if e != nil || got != x.want {
				t.Fatalf("got %v %v want %v", got, e, x.want)
			}
		})
	}
}
func TestUnsupportedRanges(t *testing.T) {
	for _, s := range []string{"", " ", "1", "1.2", "v1.2.3", "01.2.3", "^01.2", "1.2.3-beta", "1.2.3+build", "*", "1.x", "1 - 2", "!=1.2.3", "=>1", "=<2", "~>1", "1.2.3, <2", "npm:a@1.0.0", "latest", "git:foo", "https://example.invalid/a", "file:../a", "workspace:*", ">=1<2", "1.2.3 ||", ">=1 || >=2 || >=3 || >=4 || >=5", strings.Repeat(">=1 ", 9), strings.Repeat(" ", 257), ">=１"} {
		t.Run(s, func(t *testing.T) {
			if _, e := satisfies(s, "1.2.3"); e == nil {
				t.Fatalf("accepted %q", s)
			}
		})
	}
}

func TestRangeExactBounds(t *testing.T) {
	for _, s := range []string{strings.Repeat(" ", 251) + "1.2.3", ">=1 || >=2 || >=3 || >=4", strings.TrimSpace(strings.Repeat(">=1 ", 8))} {
		if _, e := satisfies(s, "1.2.3"); e != nil {
			t.Fatalf("rejected exact bound %q %v", s, e)
		}
	}
}
