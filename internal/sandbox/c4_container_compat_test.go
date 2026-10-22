package sandbox

import (
	"context"
	"strings"
	"testing"
)

// Exercise the real driver boundary, not a permissive replacement parser.
func TestC4ContainerOmittedEmptyFields(t *testing.T) {
	for _, path := range []string{"Config.ExposedPorts", "HostConfig.Sysctls", "both"} {
		t.Run(path, func(t *testing.T) {
			raw := c4DockerContainerFixture()
			for _, field := range []string{"Config.ExposedPorts", "HostConfig.Sysctls"} {
				if path == field || path == "both" {
					raw = mutateC4DockerFixture(t, raw, field, nil, true)
				}
			}
			// Missing input still fails the security parser directly.
			if _, err := decodeC4DockerInspection(raw, c4DriverPlan(), strings.Repeat("d", 64)); err == nil {
				t.Fatal("strict parser weakened")
			}
			p := c4DriverPlan()
			id := strings.Repeat("d", 64)
			f := &c4ScriptProcess{t: t, steps: []c4ProcessStep{{args: c4Args("container", "inspect", id), out: string(raw)}}}
			d := newC4DockerDriver(p, f, &c4CountingTrust{}, id)
			v, err := d.Inspect(context.Background(), c4Stage(t, p, "inspect", id))
			if err != nil || v.ID != id || f.calls != 1 {
				t.Fatal("safe omission rejected or retried", err, f.calls)
			}
		})
	}
}

func TestC4ContainerCompatibilityRejectsUnsafeMetadata(t *testing.T) {
	base := c4DockerContainerFixture()
	for _, field := range []string{"Config.ExposedPorts", "HostConfig.Sysctls"} {
		for _, value := range []any{map[string]any{"unsafe": "1"}, []any{}, false, "", 0} {
			raw := mutateC4DockerFixture(t, base, field, value, false)
			rejectC4ContainerMetadata(t, raw)
		}
		omitted := mutateC4DockerFixture(t, base, field, nil, true)
		parts := strings.Split(field, ".")
		rejectC4ContainerMetadata(t, mutateC4DockerFixture(t, omitted, parts[0]+"."+strings.ToLower(parts[1]), nil, false))
	}
	for _, field := range []string{"Config", "HostConfig", "Config.User", "HostConfig.PortBindings", "HostConfig.NetworkMode", "HostConfig.ReadonlyRootfs"} {
		rejectC4ContainerMetadata(t, mutateC4DockerFixture(t, base, field, nil, true))
	}
	rejectC4ContainerMetadata(t, []byte(strings.Replace(string(base), `"ExposedPorts":null`, `"ExposedPorts":null,"ExposedPorts":null`, 1)))
	rejectC4ContainerMetadata(t, append(base, base...))
	rejectC4ContainerMetadata(t, append(base, []byte(strings.Repeat(" ", c4DockerInspectLimit))...))
}

func rejectC4ContainerMetadata(t *testing.T, raw []byte) {
	t.Helper()
	p := c4DriverPlan()
	id := strings.Repeat("d", 64)
	f := &c4ScriptProcess{t: t, steps: []c4ProcessStep{{args: c4Args("container", "inspect", id), out: string(raw)}}}
	d := newC4DockerDriver(p, f, &c4CountingTrust{}, id)
	if _, err := d.Inspect(context.Background(), c4Stage(t, p, "inspect", id)); err == nil || f.calls != 1 {
		t.Fatal("unsafe metadata accepted or retried")
	}
}
