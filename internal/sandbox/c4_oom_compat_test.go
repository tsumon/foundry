package sandbox

import (
	"strings"
	"testing"
)

// Catches treating explicit null as disabled, or broadening to absent/unsafe metadata.
func TestC4OOMNullTransportCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name         string
		value        any
		remove, want bool
	}{
		{"null", nil, false, true}, {"false", false, false, true},
		{"true", true, false, false}, {"absent", nil, true, false},
		{"string", "false", false, false}, {"number", 0, false, false}, {"map", map[string]any{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), "HostConfig.OomKillDisable", tc.value, tc.remove)
			normalized, err := normalizeC4ContainerEmptyFields(raw)
			if err == nil {
				_, err = decodeC4DockerInspection(normalized, c4DriverPlan(), strings.Repeat("d", 64))
			}
			if (err == nil) != tc.want {
				t.Fatal("incorrect null compatibility", err)
			}
			if tc.name == "null" {
				if _, err = decodeC4DockerInspection(raw, c4DriverPlan(), strings.Repeat("d", 64)); err == nil {
					t.Fatal("strict parser broadened")
				}
			}
		})
	}
	raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), "HostConfig.OomKillDisable", nil, false)
	for _, field := range []string{"HostConfig.Privileged", "HostConfig.ReadonlyRootfs", "HostConfig.Memory", "Config.User", "Config.Labels"} {
		rejectC4ContainerMetadata(t, mutateC4DockerFixture(t, raw, field, nil, true))
	}
	rejectC4ContainerMetadata(t, []byte(strings.Replace(string(raw), `"OomKillDisable":null`, `"OomKillDisable":null,"OomKillDisable":false`, 1)))
	rejectC4ContainerMetadata(t, []byte(strings.Replace(string(raw), `"OomKillDisable":null`, `"oomkilldisable":null`, 1)))
}

func TestC4OOMNullAfterStartPreservesRealLifecycle(t *testing.T) {
	h, f, tr := newC4DockerHarness(t)
	for _, i := range []int{5, 7, 8, 9} {
		f.steps[i].out = string(mutateC4DockerFixture(t, []byte(f.steps[i].out), "HostConfig.OomKillDisable", nil, false))
	}
	o, err := runC4DockerHarness(h, f, tr)
	if err != nil || !o.Passed() || o.Result.Tests != 1 || o.CleanupState() != "confirmed" || f.calls != 11 {
		t.Fatal("null lifecycle not verified", err, o, f.calls)
	}
}
