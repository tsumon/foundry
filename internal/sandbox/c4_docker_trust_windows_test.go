package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestWindowsRealDockerTrustAlwaysClosed(t *testing.T) {
	for _, trust := range []C4Trust{{}, {DockerPath: `C:\Program Files\Docker\docker.exe`, Endpoint: "npipe:////./pipe/docker_engine"}, {DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock"}} {
		if (&c4DockerTrust{trust: trust, fs: c4DockerOSFS{}}).Check() == nil {
			t.Fatal("native Docker trust enabled")
		}
	}
}

func TestWindowsLegacyDockerCannotLaunchEvenValidExecutable(t *testing.T) {
	// This test executable would exit successfully without running tests if the
	// platform gate disappeared. No real Docker/client/account is involved.
	d := &Docker{Path: os.Args[0]}
	if _, err := d.command(context.Background(), "-test.run=^$"); err == nil || !strings.Contains(err.Error(), "unsupported on Windows") {
		t.Fatalf("legacy Docker command not blocked by platform gate: %v", err)
	}
	if _, err := d.Run(context.Background(), "image", t.TempDir(), "command", "run"); err == nil || !strings.Contains(err.Error(), "unsupported on Windows") {
		t.Fatalf("legacy Docker execution not blocked by platform gate: %v", err)
	}
}
