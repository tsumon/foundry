package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAcceptanceGrammarAndRecursiveInputs(t *testing.T) {
	base := t.TempDir()
	for name, body := range map[string]string{
		"entry.sh":   "#!/bin/sh\nset -eu\nsh ./helper.sh\ngrep -Fq 'expected value' /workspace/src/main.txt\n",
		"helper.sh":  "test -f src/main.txt\nexit 1\n",
		"dynamic.sh": "sh src/mutable.sh\n", "unsafe.sh": ". src/helper.sh\n",
		"cycle.sh": "sh cycle.sh\n", "runner.sh": "npm test\n",
	} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		command string
		want    []string
		bad     bool
	}{
		{"sh /workspace/entry.sh", []string{"entry.sh", "helper.sh"}, false},
		{"grep expected /workspace/src/main.txt", []string{}, false},
		{"grep -qx 'hello from foundry' /workspace/src/greeting.txt", []string{}, false},
		{"test -s ./src/main.txt", []string{}, false},
		{"/opt/foundry/verify --suite 'web tests'", []string{}, false},
		{"sh /opt/foundry/verify.sh /workspace/src/main.txt", []string{}, false},
		{"sh unsafe.sh", nil, true}, {"sh dynamic.sh", nil, true}, {"sh cycle.sh", nil, true}, {"sh runner.sh", nil, true},
		{"sh ENTRY.sh", nil, true},
		{"grep -f src/pattern src/main.txt", nil, true}, {"sh entry.sh argument", nil, true},
		{"grep expected src/main.txt > result", nil, true}, {"grep expected src/main.txt && exit 0", nil, true},
		{"grep \"$X\" src/main.txt", nil, true}, {"/opt/foundry/verify `cat src/main.txt`", nil, true},
		{"/opt/foundry/verify\ntrue", nil, true}, {"/opt/foundry/verify'abc'", nil, true},
		{"test -f ../../outside", nil, true}, {"/opt/foundry/../verify", nil, true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			got, err := AcceptanceInputs(base, []string{tc.command})
			if tc.bad {
				if err == nil {
					t.Fatal("unsupported form accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("frozen=%v want %v", got, tc.want)
			}
		})
	}
}
