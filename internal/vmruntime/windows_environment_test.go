package vmruntime

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestWindowsEnvironmentContainsOnlyExplicitValuesAndAuthoritativeRoot(t *testing.T) {
	t.Setenv("SYSTEMROOT", `Z:\untrusted`)
	t.Setenv("FOUNDRY_PRIVATE_SENTINEL", "must-not-inherit")
	for _, env := range [][]string{nil, {"LC_ALL=C", "LANG=C"}, {"LANG=C", "LC_ALL=C"}} {
		block, err := windowsEnvironment(env, `C:\Windows`)
		if err != nil {
			t.Fatal(err)
		}
		want := "SYSTEMROOT=C:\\Windows\x00\x00"
		if len(env) != 0 {
			want = "LANG=C\x00LC_ALL=C\x00" + want
		}
		if got := string(utf16.Decode(block)); got != want {
			t.Fatalf("unexpected explicit environment: %q", got)
		}
	}
}

func TestWindowsEnvironmentRejectsAmbiguousOrInjectedEntries(t *testing.T) {
	for _, env := range [][]string{
		{"SYSTEMROOT=C:\\Windows"}, {"SystemRoot=Z:\\untrusted"},
		{"LANG=C", "lang=other"}, {"NO_EQUALS"}, {"=hidden=value"},
		{"LANG=C\x00PATH=untrusted"}, {"NA\x00ME=value"},
	} {
		if _, err := windowsEnvironment(env, `C:\Windows`); err == nil {
			t.Fatalf("accepted invalid environment %q", env)
		}
	}
	for _, root := range []string{"", "relative", `\Windows`, `\\server\Windows`, "C:\\Windows\x00PATH=x"} {
		if _, err := windowsEnvironment(nil, root); err == nil {
			t.Fatal("accepted invalid authoritative root")
		}
	}
}

func TestWindowsEnvironmentPreservesUnicodeAndCallerInput(t *testing.T) {
	env := []string{"Z=\u6d4b\u8bd5", "A=x=y"}
	before := append([]string(nil), env...)
	block, err := windowsEnvironment(env, `C:\\Windows`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(env, before) {
		t.Fatal("mutated caller environment")
	}
	if got := strings.Split(string(utf16.Decode(block)), "\x00"); !reflect.DeepEqual(got, []string{"A=x=y", `SYSTEMROOT=C:\\Windows`, "Z=\u6d4b\u8bd5", "", ""}) {
		t.Fatalf("unexpected UTF-16 block: %q", got)
	}
}
