package sandbox

import (
	"strings"
	"testing"
)

func TestImmutableImageRejectsVolumes(t *testing.T) {
	if _, e := parseImage([]byte(`[{"Id":"sha256:abc","Config":{"Volumes":{"/data":{}}}}]`)); e == nil {
		t.Fatal("volume accepted")
	}
	if id, e := parseImage([]byte(`[{"Id":"sha256:abc","Config":{"Volumes":null}}]`)); e != nil || id != "sha256:abc" {
		t.Fatalf("%s %v", id, e)
	}
}
func TestFixedIsolationArguments(t *testing.T) {
	args, e := runArgs("owner", "mission", "job", "/safe/candidate", "sha256:abc", "echo ok")
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--no-healthcheck", "--pids-limit=128", "--memory=512m", "--cpus=1", "--user=65534:65534", "--entrypoint=/bin/sh", "readonly", "size=64m", "--log-driver=none", "foundry.owner=owner", "sha256:abc -c echo ok"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s", want)
		}
	}
	if _, e := runArgs("owner", "mission", "job", "/bad,path", "sha256:abc", "true"); e == nil {
		t.Fatal("mount injection accepted")
	}
}
