package sandbox

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
)

// Fixtures are hand-authored synthetic Docker JSON, not observations of Docker.
func c4DockerProtocolPlan() C4Plan {
	return C4Plan{profile: domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "docker", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", Verifier: "/opt/foundry/verify-go-v1"}, operation: domain.C4Operation{ID: "op-1", Owner: "owner-1", MissionID: "mission-1", ContainerName: "foundry-op-1"}, candidate: "/synthetic/candidate"}
}

func c4DockerImageFixture() []byte {
	return []byte(`[{"Id":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Os":"linux","Architecture":"arm64","Config":{"Volumes":null,"Cmd":null,"Env":["GOLANG_VERSION=1.27.3","PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"]}}]`)
}

func c4DockerContainerFixture() []byte {
	return []byte(`[{
	"Id":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","Name":"/foundry-op-1",
	"Image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"Config":{"Image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Labels":{"foundry.owner":"owner-1","foundry.operation":"op-1","foundry.mission":"mission-1","foundry.name":"foundry-op-1"},"Entrypoint":["/opt/foundry/verify-go-v1"],"Cmd":null,"User":"65534:65534","WorkingDir":"/workspace","Env":["GOPROXY=off","GOSUMDB=off","GOTOOLCHAIN=local","GOENV=off","GOWORK=off","CGO_ENABLED=0","GOCACHE=/foundry-build/cache","GOTMPDIR=/foundry-build/tmp","GOPATH=/foundry-build/gopath","TMPDIR=/tmp"],"Volumes":null,"ExposedPorts":null,"Healthcheck":{"Test":["NONE"]},"Tty":false,"OpenStdin":false,"StdinOnce":false},
	"HostConfig":{"ReadonlyRootfs":true,"Mounts":[{"Type":"bind","Source":"/synthetic/candidate","Target":"/workspace","ReadOnly":true}],"Binds":null,"Tmpfs":{"/tmp":"rw,nosuid,nodev,noexec,size=67108864,nr_inodes=16384,mode=1777","/foundry-build":"rw,nosuid,nodev,exec,size=268435456,nr_inodes=16384,mode=1777"},"NetworkMode":"none","PortBindings":{},"PublishAllPorts":false,"Privileged":false,"CapAdd":null,"CapDrop":["ALL"],"SecurityOpt":["no-new-privileges"],"NanoCpus":1000000000,"Memory":1073741824,"MemorySwap":1073741824,"PidsLimit":128,"LogConfig":{"Type":"none","Config":{}},"RestartPolicy":{"Name":"no","MaximumRetryCount":0},"PidMode":"","IpcMode":"private","UTSMode":"","UsernsMode":"","Devices":[],"DeviceRequests":null,"VolumesFrom":null,"Links":null,"ExtraHosts":null,"GroupAdd":null,"Dns":[],"DnsOptions":[],"DnsSearch":[],"Sysctls":{},"AutoRemove":false,"CpuShares":0,"CpuPeriod":0,"CpuQuota":0,"CpuRealtimePeriod":0,"CpuRealtimeRuntime":0,"CpusetCpus":"","CpusetMems":"","MemoryReservation":0,"MemorySwappiness":null,"OomKillDisable":false,"CgroupParent":"","CgroupnsMode":"private","Isolation":""},
	"Mounts":[{"Type":"bind","Source":"/synthetic/candidate","Destination":"/workspace","Mode":"","RW":false,"Propagation":"rprivate"}],
	"NetworkSettings":{"Ports":{},"Networks":{"none":{}}},
	"State":{"Running":false,"OOMKilled":false,"ExitCode":0,"StartedAt":"0001-01-01T00:00:00Z"}
}]`)
}

func TestC4DockerVerifierAcceptsOnlyCompleteEnvelope(t *testing.T) {
	r, err := decodeC4Verifier([]byte(`{"class":"passed","exit_code":0,"tests":1,"output":"ok\n","truncated":false}`), 0)
	if err != nil || r == nil || r.Class != "passed" || r.Tests != 1 || r.Output != "ok\n" {
		t.Fatalf("valid complete result rejected: %+v %v", r, err)
	}
}

func TestC4DockerVerifierProcessMapping(t *testing.T) {
	for _, tc := range []struct {
		class                  string
		goExit, process, tests int
	}{{"passed", 0, 0, 1}, {"test_failed", 7, 1, 0}, {"infrastructure_failed", -1, 2, 0}, {"infrastructure_failed", 0, 2, 0}, {"timeout", -1, 2, 0}} {
		raw, _ := json.Marshal(map[string]any{"class": tc.class, "exit_code": tc.goExit, "tests": tc.tests, "output": "", "truncated": false})
		r, err := decodeC4Verifier(raw, tc.process)
		if err != nil || r == nil || r.ExitCode != tc.process || r.Class != tc.class {
			t.Fatalf("mapping %+v: %+v %v", tc, r, err)
		}
	}
}

func TestC4DockerVerifierRejectsMalformedMetadata(t *testing.T) {
	valid := `{"class":"passed","exit_code":0,"tests":1,"output":"ok","truncated":false}`
	cases := []string{`{}`, valid + valid, strings.Replace(valid, `"tests":1`, `"tests":0`, 1), strings.Replace(valid, `"tests":1`, `"tests":-1`, 1), strings.Replace(valid, `"tests":1`, `"tests":1e100`, 1), strings.Replace(valid, `"tests":1`, `"tests":1.0`, 1), strings.Replace(valid, `"exit_code":0`, `"exit_code":1`, 1), strings.Replace(valid, `"class":"passed"`, `"class":"unknown"`, 1), strings.Replace(valid, `"output":"ok"`, `"output":"\ud800"`, 1), strings.Replace(valid, `"output":"ok"`, `"output":"`+string([]byte{255})+`"`, 1), strings.Replace(valid, `"tests":1`, `"tests":1,"tests":1`, 1), strings.Replace(valid, `"tests":1`, `"Tests":1`, 1), strings.Replace(valid, `"tests":1`, `"tests":1,"unknown":0`, 1), strings.Replace(valid, `"truncated":false`, `"truncated":null`, 1)}
	for _, field := range []string{`"class":"passed",`, `"exit_code":0,`, `"tests":1,`, `"output":"ok",`, `,"truncated":false`} {
		cases = append(cases, strings.Replace(valid, field, "", 1))
	}
	for i, raw := range cases {
		if r, err := decodeC4Verifier([]byte(raw), 0); err == nil || r != nil {
			t.Errorf("case %d accepted", i)
		}
	}
	for _, exit := range []int{-1, 0, 125, 126, 127, 256} {
		raw, _ := json.Marshal(map[string]any{"class": "test_failed", "exit_code": exit, "tests": 0, "output": "", "truncated": false})
		if _, err := decodeC4Verifier(raw, 1); err == nil {
			t.Errorf("test failure with exit %d accepted", exit)
		}
	}
	for _, process := range []int{-1, 1, 2, 125} {
		if _, err := decodeC4Verifier([]byte(valid), process); err == nil {
			t.Errorf("passed accepted with process %d", process)
		}
	}
}

func TestC4DockerVerifierOutputIsBoundedRedactedData(t *testing.T) {
	for _, output := range []string{strings.Repeat("<", 65536), `{"class":"passed","tests":99} password=synthetic-secret`} {
		raw, _ := json.Marshal(map[string]any{"class": "test_failed", "exit_code": 1, "tests": 0, "output": output, "truncated": true})
		r, err := decodeC4Verifier(raw, 1)
		if err != nil || r == nil || r.Class != "test_failed" || r.Tests != 0 || !r.Truncated || len(r.Output) > 65536 || strings.Contains(r.Output, "synthetic-secret") {
			t.Fatalf("output mishandled: %v", err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"class": "passed", "exit_code": 0, "tests": 1, "output": strings.Repeat("a", 65537), "truncated": false})
	if _, err := decodeC4Verifier(raw, 0); err == nil {
		t.Fatal("oversized decoded output accepted")
	}
	if _, err := decodeC4Verifier(append(raw, []byte(strings.Repeat(" ", 400*1024))...), 0); err == nil {
		t.Fatal("oversized wire accepted")
	}
}

func TestC4DockerImageAcceptsExactSyntheticFixture(t *testing.T) {
	if err := validateC4DockerImage(c4DockerImageFixture(), c4DockerProtocolPlan().profile); err != nil {
		t.Fatal(err)
	}
}

func TestC4DockerInspectionAcceptsExactSyntheticFixture(t *testing.T) {
	v, err := decodeC4DockerInspection(c4DockerContainerFixture(), c4DockerProtocolPlan(), strings.Repeat("d", 64))
	if err != nil || v.ID != strings.Repeat("d", 64) || v.Name != "foundry-op-1" || v.Labels["foundry.owner"] != "owner-1" || !v.StartedAt.IsZero() || v.ExitCode != 0 || v.Running || v.OOMKilled || v.Verification != nil {
		t.Fatalf("invalid inspection: %+v %v", v, err)
	}
}

func mutateC4DockerFixture(t *testing.T, raw []byte, path string, value any, remove bool) []byte {
	t.Helper()
	var a []map[string]any
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	m := a[0]
	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		m = m[p].(map[string]any)
	}
	if remove {
		delete(m, parts[len(parts)-1])
	} else {
		m[parts[len(parts)-1]] = value
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestC4DockerImageRejectsPolicyDrift(t *testing.T) {
	for _, tc := range []struct {
		path  string
		value any
	}{
		{"Id", "sha256:" + strings.Repeat("b", 64)}, {"Os", "darwin"}, {"Architecture", "amd64"}, {"Config.Volumes", map[string]any{"/data": map[string]any{}}}, {"Config.Cmd", []string{"sh"}},
		{"Config.Env", []string{"PATH=/evil"}}, {"Config.Env", []string{"GOLANG_VERSION=1.26.0"}}, {"Config.Env", []string{"LD_PRELOAD=/evil"}}, {"Config.Env", []string{"GOFLAGS=-exec=evil"}}, {"Config.Env", []string{"GOPROXY=https://example.invalid"}}, {"Config.Env", []string{"GOPROXY=off", "GOPROXY=off"}}, {"Config.Env", []string{"GOPROXY"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if err := validateC4DockerImage(mutateC4DockerFixture(t, c4DockerImageFixture(), tc.path, tc.value, false), c4DockerProtocolPlan().profile); err == nil {
				t.Fatal("drift accepted")
			}
		})
	}
	for _, p := range []string{"Id", "Os", "Architecture", "Config", "Config.Volumes", "Config.Cmd", "Config.Env"} {
		if err := validateC4DockerImage(mutateC4DockerFixture(t, c4DockerImageFixture(), p, nil, true), c4DockerProtocolPlan().profile); err == nil {
			t.Errorf("missing %s accepted", p)
		}
	}
	for _, v := range []any{nil, []string{}, []string{"GOPROXY=off"}} {
		if err := validateC4DockerImage(mutateC4DockerFixture(t, c4DockerImageFixture(), "Config.Env", v, false), c4DockerProtocolPlan().profile); err != nil {
			t.Fatalf("safe env rejected: %v", err)
		}
	}
}

func TestC4DockerInspectionRejectsPolicyDrift(t *testing.T) {
	changes := []struct {
		path  string
		value any
	}{
		{"Id", strings.Repeat("e", 64)}, {"Name", "//foundry-op-1"}, {"Image", "other"}, {"Config.Image", "other"}, {"Config.Labels", map[string]string{"foundry.owner": "foreign"}},
		{"Config.Entrypoint", []string{"/bin/sh"}}, {"Config.Cmd", []string{"evil"}}, {"Config.User", "0:0"}, {"Config.WorkingDir", "/"}, {"Config.Env", []string{}}, {"Config.Env", []string{"LD_PRELOAD=/evil"}}, {"Config.Volumes", map[string]any{"/data": nil}}, {"Config.ExposedPorts", map[string]any{"80/tcp": nil}}, {"Config.Healthcheck", map[string]any{"Test": []string{"CMD", "evil"}}}, {"Config.Tty", true}, {"Config.OpenStdin", true}, {"Config.StdinOnce", true},
		{"HostConfig.ReadonlyRootfs", false}, {"HostConfig.Mounts", []any{}}, {"HostConfig.Binds", []string{"/:/host"}}, {"HostConfig.Tmpfs", map[string]string{"/tmp": "rw"}}, {"HostConfig.NetworkMode", "host"}, {"HostConfig.PortBindings", map[string]any{"80/tcp": nil}}, {"HostConfig.PublishAllPorts", true}, {"HostConfig.Privileged", true}, {"HostConfig.CapAdd", []string{"SYS_ADMIN"}}, {"HostConfig.CapDrop", []string{}}, {"HostConfig.SecurityOpt", []string{}}, {"HostConfig.NanoCpus", 2e9}, {"HostConfig.Memory", 2e9}, {"HostConfig.MemorySwap", -1}, {"HostConfig.PidsLimit", -1}, {"HostConfig.LogConfig", map[string]any{"Type": "json-file", "Config": map[string]any{}}}, {"HostConfig.RestartPolicy", map[string]any{"Name": "always", "MaximumRetryCount": 0}},
		{"HostConfig.PidMode", "host"}, {"HostConfig.IpcMode", "host"}, {"HostConfig.UTSMode", "host"}, {"HostConfig.UsernsMode", "host"}, {"HostConfig.Devices", []string{"evil"}}, {"HostConfig.DeviceRequests", []string{"evil"}}, {"HostConfig.VolumesFrom", []string{"foreign"}}, {"HostConfig.Links", []string{"foreign"}}, {"HostConfig.ExtraHosts", []string{"host:1.2.3.4"}}, {"HostConfig.GroupAdd", []string{"0"}}, {"HostConfig.Dns", []string{"1.2.3.4"}}, {"HostConfig.DnsOptions", []string{"evil"}}, {"HostConfig.DnsSearch", []string{"evil"}}, {"HostConfig.Sysctls", map[string]string{"net.ipv4.ip_forward": "1"}}, {"HostConfig.AutoRemove", true},
		{"HostConfig.CpuShares", 2}, {"HostConfig.CpuPeriod", 100}, {"HostConfig.CpuQuota", 100}, {"HostConfig.CpuRealtimePeriod", 100}, {"HostConfig.CpuRealtimeRuntime", 100}, {"HostConfig.CpusetCpus", "0"}, {"HostConfig.CpusetMems", "0"}, {"HostConfig.MemoryReservation", 100}, {"HostConfig.MemorySwappiness", 50}, {"HostConfig.OomKillDisable", true}, {"HostConfig.CgroupParent", "host"}, {"HostConfig.CgroupnsMode", "host"}, {"HostConfig.Isolation", "hyperv"},
		{"Mounts", []any{}}, {"NetworkSettings.Ports", map[string]any{"80/tcp": nil}}, {"NetworkSettings.Networks", map[string]any{"bridge": map[string]any{}}}, {"State.StartedAt", "bad"}, {"State.ExitCode", -1}, {"State.Running", "false"}, {"State.OOMKilled", nil},
	}
	for _, tc := range changes {
		t.Run(tc.path, func(t *testing.T) {
			for _, remove := range []bool{false, true} {
				if _, err := decodeC4DockerInspection(mutateC4DockerFixture(t, c4DockerContainerFixture(), tc.path, tc.value, remove), c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
					t.Fatalf("drift/missing field accepted (remove=%v)", remove)
				}
			}
		})
	}
	for _, tc := range []struct {
		path  string
		value any
	}{
		{"Mounts", []any{map[string]any{"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/workspace", "Mode": "", "RW": false, "Propagation": "rprivate"}}},
		{"Mounts", []any{map[string]any{"Type": "bind", "Source": "/synthetic/candidate", "Destination": "/workspace", "Mode": "", "RW": true, "Propagation": "rprivate"}}},
		{"HostConfig.Mounts", []any{map[string]any{"Type": "bind", "Source": "/synthetic/candidate", "Target": "/workspace", "ReadOnly": false}}},
	} {
		if _, err := decodeC4DockerInspection(mutateC4DockerFixture(t, c4DockerContainerFixture(), tc.path, tc.value, false), c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
			t.Errorf("unsafe mount accepted: %s", tc.path)
		}
	}
}

func TestC4DockerInspectionStateFactsAndNormalization(t *testing.T) {
	raw := c4DockerContainerFixture()
	for path, value := range map[string]any{"Name": "foundry-op-1", "State.StartedAt": "2026-10-21T01:02:03.123456789Z", "State.ExitCode": 2, "State.Running": true, "State.OOMKilled": true, "HostConfig.SecurityOpt": []string{"no-new-privileges=true"}, "HostConfig.IpcMode": "", "HostConfig.CgroupnsMode": ""} {
		raw = mutateC4DockerFixture(t, raw, path, value, false)
	}
	v, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64))
	if err != nil || v.StartedAt.IsZero() || v.ExitCode != 2 || !v.Running || !v.OOMKilled {
		t.Fatalf("state facts lost: %+v %v", v, err)
	}
	if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), "dddd"); err == nil {
		t.Fatal("short ID accepted")
	}
}

func TestC4DockerInspectRejectsAmbiguousJSON(t *testing.T) {
	for _, fixture := range [][]byte{c4DockerImageFixture(), c4DockerContainerFixture()} {
		for _, raw := range [][]byte{[]byte(`[]`), []byte(`null`), append(append([]byte{}, fixture...), fixture...), []byte("[" + string(fixture[1:len(fixture)-1]) + "," + string(fixture[1:len(fixture)-1]) + "]"), []byte(strings.Replace(string(fixture), `"Config":{`, `"Config":{"Unrelated":{"x":1,"x":2},`, 1)), []byte(strings.Replace(string(fixture), `"Id":`, `"id":`, 1)), []byte(strings.Replace(string(fixture), `"Config":{`, `"Config":{"Nested":`+strings.Repeat("[", 40)+`0`+strings.Repeat("]", 40)+`,`, 1)), []byte(strings.Replace(string(fixture), `"Config":{`, `"Config":{"Unrelated":"\udfff",`, 1)), append(fixture, []byte(strings.Repeat(" ", 1024*1024))...)} {
			if validateC4DockerImage(raw, c4DockerProtocolPlan().profile) == nil {
				t.Fatal("ambiguous image accepted")
			}
			if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
				t.Fatal("ambiguous container accepted")
			}
		}
	}
	image := strings.Replace(string(c4DockerImageFixture()), `"Config":{`, `"Unrelated":{"safe":[1,2]},"Config":{`, 1)
	if err := validateC4DockerImage([]byte(image), c4DockerProtocolPlan().profile); err != nil {
		t.Fatal("unrelated Docker metadata rejected", err)
	}
}

// Removing a guard for an optional security field, or treating its alternate
// spelling as unrelated Docker metadata, must not silently accept the override.
func TestC4DockerInspectionRejectsOptionalSecurityOverrides(t *testing.T) {
	for key, value := range map[string]any{
		"DeviceCgroupRules": []string{"a *:* rwm"}, "Ulimits": []string{"unlimited"}, "BlkioDeviceReadBps": []string{"override"}, "BlkioDeviceWriteBps": []string{"override"}, "BlkioDeviceReadIOps": []string{"override"}, "BlkioDeviceWriteIOps": []string{"override"}, "BlkioWeightDevice": []string{"override"}, "StorageOpt": map[string]string{"size": "1T"}, "Annotations": map[string]string{"override": "true"}, "OomScoreAdj": -1000, "BlkioWeight": 1000, "CpuCount": 2, "CpuPercent": 200, "IOMaximumIOps": 1, "IOMaximumBandwidth": 1, "Init": true,
	} {
		for _, spelling := range []string{key, strings.ToLower(key)} {
			t.Run(spelling, func(t *testing.T) {
				raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), "HostConfig."+spelling, value, false)
				if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
					t.Fatal("optional security override accepted")
				}
			})
		}
	}
	for _, spelling := range []string{"Interval", "interval", "Timeout", "timeout", "Retries", "retries", "StartPeriod", "startperiod", "StartInterval", "startinterval"} {
		raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), "Config.Healthcheck."+spelling, 1, false)
		if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
			t.Errorf("healthcheck override %s accepted", spelling)
		}
	}
}

func TestC4DockerInspectionRejectsEveryMountDimension(t *testing.T) {
	for _, which := range []string{"Mounts", "HostConfig.Mounts"} {
		base := map[string]any{"Type": "bind", "Source": "/synthetic/candidate", "Destination": "/workspace", "Mode": "", "RW": false, "Propagation": "rprivate"}
		changes := map[string]any{"Type": "volume", "Source": "/host", "Destination": "/elsewhere", "Mode": "rw", "RW": true, "Propagation": "rshared"}
		if which == "HostConfig.Mounts" {
			base = map[string]any{"Type": "bind", "Source": "/synthetic/candidate", "Target": "/workspace", "ReadOnly": true}
			changes = map[string]any{"Type": "volume", "Source": "/host", "Target": "/elsewhere", "ReadOnly": false, "VolumeOptions": map[string]any{"NoCopy": true}, "TmpfsOptions": map[string]any{"SizeBytes": 1}, "ImageOptions": map[string]any{"Subpath": "/"}, "Consistency": "cached", "BindOptions": map[string]any{"Propagation": "rshared"}}
		}
		for key, value := range changes {
			for _, remove := range []bool{false, true} {
				if _, exists := base[key]; remove && !exists {
					continue
				}
				b := map[string]any{}
				for k, v := range base {
					b[k] = v
				}
				if remove {
					delete(b, key)
				} else {
					b[key] = value
				}
				raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), which, []any{b}, false)
				if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
					t.Errorf("mount %s %s remove=%v accepted", which, key, remove)
				}
			}
		}
		for _, key := range []string{"NonRecursive", "CreateMountpoint", "ReadOnlyNonRecursive", "ReadOnlyForceRecursive", "Unknown"} {
			if which != "HostConfig.Mounts" {
				continue
			}
			b := map[string]any{}
			for k, v := range base {
				b[k] = v
			}
			b["BindOptions"] = map[string]any{key: true}
			raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), which, []any{b}, false)
			if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
				t.Errorf("unsafe bind option %s accepted", key)
			}
		}
	}
	for _, original := range []string{"rw", "nosuid", "nodev", "noexec", "size=67108864", "nr_inodes=16384", "mode=1777"} {
		raw := []byte(strings.Replace(string(c4DockerContainerFixture()), original+",", "unsafe,", 1))
		if original == "mode=1777" {
			raw = []byte(strings.Replace(string(c4DockerContainerFixture()), original, "mode=0777", 1))
		}
		if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
			t.Errorf("tmpfs option %s drift accepted", original)
		}
	}
}

func TestC4DockerInspectionRejectsLabelAndStateAmbiguity(t *testing.T) {
	for _, key := range []string{"foundry.owner", "foundry.operation", "foundry.mission", "foundry.name"} {
		labels := map[string]string{"foundry.owner": "owner-1", "foundry.operation": "op-1", "foundry.mission": "mission-1", "foundry.name": "foundry-op-1"}
		labels[key] = "foreign"
		if _, err := decodeC4DockerInspection(mutateC4DockerFixture(t, c4DockerContainerFixture(), "Config.Labels", labels, false), c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
			t.Errorf("foreign %s accepted", key)
		}
	}
	for path, value := range map[string]any{"State.Running": true, "State.StartedAt": "", "State.ExitCode": 256} {
		if _, err := decodeC4DockerInspection(mutateC4DockerFixture(t, c4DockerContainerFixture(), path, value, false), c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
			t.Errorf("ambiguous state %s accepted", path)
		}
	}
}

func TestC4DockerBoundedCollectionsAndLosslessFields(t *testing.T) {
	valid := `{"class":"passed","exit_code":0,"tests":1,"output":"ok","truncated":false}`
	for _, raw := range []string{strings.Replace(valid, `"tests":1`, `"tests":4097`, 1), strings.Replace(valid, `"tests":1`, `"tests":9223372036854775808`, 1), strings.Replace(valid, `"tests":1`, `"tests":1,"te\u0073ts":1`, 1), strings.Replace(valid, `"tests":1`, `"tests":1,"Tests":1`, 1)} {
		if _, err := decodeC4Verifier([]byte(raw), 0); err == nil {
			t.Fatal("ambiguous count accepted")
		}
	}
	for _, extra := range []string{`[` + strings.Repeat(`0,`, 1024) + `0]`, `{` + strings.Join(func() []string {
		a := []string{}
		for i := 0; i < 257; i++ {
			b, _ := json.Marshal(i)
			a = append(a, `"k`+string(b)+`":0`)
		}
		return a
	}(), ",") + `}`, `[` + strings.Repeat(`[`+strings.Repeat(`0,`, 1000)+`0],`, 9) + `0]`} {
		raw := strings.Replace(string(c4DockerImageFixture()), `"Config":{`, `"extra":`+extra+`,"Config":{`, 1)
		if err := validateC4DockerImage([]byte(raw), c4DockerProtocolPlan().profile); err == nil {
			t.Fatal("unbounded collection accepted")
		}
	}
}

func TestC4DockerVerifierMappingCannotPromoteFailure(t *testing.T) {
	for _, tc := range []struct {
		class          string
		inner, process int
		want           string
	}{
		{"test_failed", 7, 1, "test_failed"}, {"infrastructure_failed", 0, 2, "infrastructure_failed"}, {"timeout", 0, 2, "infrastructure_failed"},
	} {
		raw, _ := json.Marshal(map[string]any{"class": tc.class, "exit_code": tc.inner, "tests": 1, "output": "", "truncated": false})
		r, err := decodeC4Verifier(raw, tc.process)
		if err != nil {
			t.Fatal(err)
		}
		got := classifyC4(C4Inspection{ExitCode: tc.process, Verification: r}, nil, nil)
		if got.Class != tc.want {
			t.Fatalf("%s mapped to %s", tc.class, got.Class)
		}
	}
}

func TestC4DockerInspectionAcceptsOnlyCompleteTmpfsMountSet(t *testing.T) {
	mounts := []any{
		map[string]any{"Type": "bind", "Source": "/synthetic/candidate", "Destination": "/workspace", "Mode": "ro", "RW": false, "Propagation": "rprivate"},
		map[string]any{"Type": "tmpfs", "Source": "", "Destination": "/tmp", "Mode": "", "RW": true, "Propagation": ""},
		map[string]any{"Type": "tmpfs", "Source": "", "Destination": "/foundry-build", "Mode": "", "RW": true, "Propagation": ""},
	}
	raw := mutateC4DockerFixture(t, c4DockerContainerFixture(), "Mounts", mounts, false)
	raw = []byte(strings.Replace(string(raw), "rw,nosuid,nodev,noexec,size=67108864,nr_inodes=16384,mode=1777", "mode=1777,nr_inodes=16384,size=67108864,noexec,nodev,nosuid,rw", 1))
	if _, err := decodeC4DockerInspection(raw, c4DockerProtocolPlan(), strings.Repeat("d", 64)); err != nil {
		t.Fatal("complete fixed tmpfs mounts rejected", err)
	}
	for _, bad := range [][]any{mounts[:2], append(append([]any{}, mounts...), mounts[1]), {mounts[0], mounts[1], mounts[1]}, {mounts[0], mounts[1], map[string]any{"Type": "tmpfs", "Source": "/host", "Destination": "/foundry-build", "Mode": "", "RW": true, "Propagation": ""}}} {
		if _, err := decodeC4DockerInspection(mutateC4DockerFixture(t, c4DockerContainerFixture(), "Mounts", bad, false), c4DockerProtocolPlan(), strings.Repeat("d", 64)); err == nil {
			t.Fatal("ambiguous tmpfs mounts accepted")
		}
	}
}
