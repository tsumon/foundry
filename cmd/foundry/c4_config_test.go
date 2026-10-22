package main

import (
	"context"
	"encoding/json"
	"foundry/internal/c4fixture"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validC4Config() engine.C4Activation {
	return engine.C4Activation{SchemaVersion: 1, Enabled: true, SourceRoot: "/owned-fixture/source", BaseCommit: strings.Repeat("b", 40), BaseDigest: strings.Repeat("c", 64), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Profile: domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_v1", Mode: "docker", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/owned-nonexistent-fixture/docker", Endpoint: "unix:///owned-nonexistent-fixture/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}}
}

func TestC4ConfigStrictStructuralOnly(t *testing.T) {
	c := validC4Config()
	raw, _ := json.Marshal(c)
	cases := map[string]string{"valid": string(raw), "null": "null", "empty": "{}", "unknown": strings.Replace(string(raw), `"enabled":true`, `"enabled":true,"surprise":1`, 1), "duplicate": strings.Replace(string(raw), `"enabled":true`, `"enabled":true,"enabled":true`, 1), "alias": strings.Replace(string(raw), `"enabled"`, `"Enabled"`, 1), "nested_alias": strings.Replace(string(raw), `"platform"`, `"Platform"`, 1), "nested_null": strings.Replace(string(raw), `"control_version":1`, `"control_version":null`, 1), "disabled": strings.Replace(string(raw), `"enabled":true`, `"enabled":false`, 1), "incomplete": strings.Replace(string(raw), `"control_version":1,`, "", 1), "trailing": string(raw) + ` {}`, "oversized": strings.Repeat(" ", c4ConfigLimit+1)}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "control.json")
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readC4Config(p, time.Now())
			if name == "valid" {
				if err != nil || got.Profile.DockerPath != c.Profile.DockerPath {
					t.Fatal("structural parser probed runtime", err)
				}
			} else if err == nil {
				t.Fatal("invalid config admitted")
			}
		})
	}
	for _, expiry := range []time.Time{time.Now().Add(-time.Minute), time.Now().Add(25 * time.Hour)} {
		c.ExpiresAt = expiry.Format(time.RFC3339Nano)
		raw, _ = json.Marshal(c)
		p := filepath.Join(t.TempDir(), "expired.json")
		os.WriteFile(p, raw, 0600)
		if _, err := readC4Config(p, time.Now()); err == nil {
			t.Fatal("invalid activation window")
		}
	}
	if _, err := readC4Config(t.TempDir(), time.Now()); err == nil {
		t.Fatal("directory accepted")
	}
	p := filepath.Join(t.TempDir(), "link.json")
	os.Symlink("missing", p)
	if _, err := readC4Config(p, time.Now()); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestC4ConfigInvalidBeforeCredentials(t *testing.T) {
	// Existing panic-on-use fixture proves no keychain access on rejected input.
	if err := runWithSecrets([]string{"--c4-control-config", "/owned-missing-control-file"}, diagnosticSecrets{}); err == nil || strings.Contains(err.Error(), "owned-missing") {
		t.Fatal("invalid configuration accepted or path leaked", err)
	}
}

func TestC4IsolatedRuntimeConstructionAndMixedHistory(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "mixed"}[mixed], func(t *testing.T) {
			dir := t.TempDir()
			s, err := store.Open(filepath.Join(dir, "foundry.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if mixed {
				if err = s.PutProfile(domain.Profile{ID: "external", Name: "External", Kind: "openai", Protocol: "chat_completions", BaseURL: "https://unused.invalid", Model: "unused"}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			factory := func(c engine.C4Activation, g *c4fixture.Gateway) (*engine.C4ControlledCapability, error) {
				calls++
				c.Profile.Mode = "synthetic"
				return engine.NewC4ControlledSyntheticCapability(c, sandbox.NewC4SyntheticDriver(nil), g, func(context.Context) (sandbox.C4ReadinessResult, error) {
					t.Fatal("startup dispatched readiness")
					return sandbox.C4ReadinessResult{}, nil
				})
			}
			e, ro, err := newC4ControlledRuntime(s, dir, validC4Config(), factory)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			if calls != 1 || ro != mixed {
				t.Fatal("wrong isolation preflight", calls, ro)
			}
			profiles, err := s.ListProfiles()
			if err != nil || len(profiles) != 1 {
				t.Fatal("unexpected profile mutation", profiles, err)
			}
			if mixed && profiles[0].ID != "external" {
				t.Fatal("mixed history rewritten")
			}
		})
	}
}
