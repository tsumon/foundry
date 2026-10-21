package sandbox

import (
	"strings"
	"time"
)

// Accepted normalization is deliberately narrow: a single Docker name slash,
// null/empty collections, no-new-privileges[=true], private/empty IPC and cgroup
// namespaces, and reordered identical tmpfs options. No daemon is consulted to
// guess additional variants. A rejected variant requires review, not fallback.
func decodeC4DockerInspection(raw []byte, plan C4Plan, id string) (c4DockerInspection, error) {
	m, err := c4DockerInspectObject(raw)
	if err != nil {
		return c4DockerInspection{}, err
	}
	fail := func() (c4DockerInspection, error) { return c4DockerInspection{}, errC4DockerProtocol }
	if !c4ContainerID.MatchString(id) || !m.equal("Id", id) || plan.operation.Owner == "" || plan.operation.ID == "" || plan.operation.MissionID == "" || plan.operation.ContainerName == "" || plan.candidate == "" || plan.profile.Verifier != "/opt/foundry/verify-go-v1" || plan.profile.Platform != "linux/arm64" || !strings.HasPrefix(plan.profile.ImageID, "sha256:") || !c4ContainerID.MatchString(strings.TrimPrefix(plan.profile.ImageID, "sha256:")) {
		return fail()
	}
	name, ok := m.str("Name")
	if !ok || strings.TrimPrefix(name, "/") != plan.operation.ContainerName || !m.equal("Image", plan.profile.ImageID) {
		return fail()
	}
	c := m.object("Config")
	labels := c.object("Labels")
	wantLabels := map[string]string{"foundry.owner": plan.operation.Owner, "foundry.operation": plan.operation.ID, "foundry.mission": plan.operation.MissionID, "foundry.name": plan.operation.ContainerName}
	if len(labels) != 4 {
		return fail()
	}
	for k, v := range wantLabels {
		if !labels.equal(k, v) {
			return fail()
		}
	}
	if !c.equal("Image", plan.profile.ImageID) || !c.list("Entrypoint", "/opt/foundry/verify-go-v1") || !c.list("Cmd") || !c.equal("User", "65534:65534") || !c.equal("WorkingDir", "/workspace") || !c4DockerEnvironment(c, true) || !c.emptyObject("Volumes") || !c.emptyObject("ExposedPorts") || !c.equal("Tty", false) || !c.equal("OpenStdin", false) || !c.equal("StdinOnce", false) {
		return fail()
	}
	health := c.object("Healthcheck")
	if !health.list("Test", "NONE") || !health.exactCase("Interval", "Timeout", "StartPeriod", "StartInterval", "Retries") {
		return fail()
	}
	for _, key := range []string{"Interval", "Timeout", "StartPeriod", "StartInterval", "Retries"} {
		if _, exists := health[key]; exists && !health.equal(key, 0) {
			return fail()
		}
	}
	h := m.object("HostConfig")
	if !h.exactCase("DeviceCgroupRules", "Ulimits", "BlkioDeviceReadBps", "BlkioDeviceWriteBps", "BlkioDeviceReadIOps", "BlkioDeviceWriteIOps", "BlkioWeightDevice", "StorageOpt", "Annotations", "OomScoreAdj", "BlkioWeight", "CpuCount", "CpuPercent", "IOMaximumIOps", "IOMaximumBandwidth", "Init") {
		return fail()
	}
	if !h.equal("ReadonlyRootfs", true) || !h.equal("NetworkMode", "none") || !h.emptyObject("PortBindings") || !h.equal("PublishAllPorts", false) || !h.equal("Privileged", false) || !h.list("CapAdd") || !h.list("CapDrop", "ALL") || !(h.list("SecurityOpt", "no-new-privileges") || h.list("SecurityOpt", "no-new-privileges=true")) || !h.equal("NanoCpus", 1000000000) || !h.equal("Memory", 1073741824) || !h.equal("MemorySwap", 1073741824) || !h.equal("PidsLimit", 128) || !h.equal("AutoRemove", false) {
		return fail()
	}
	log := h.object("LogConfig")
	restart := h.object("RestartPolicy")
	if !log.equal("Type", "none") || !log.emptyObject("Config") || !restart.equal("Name", "no") || !restart.equal("MaximumRetryCount", 0) {
		return fail()
	}
	for _, key := range []string{"PidMode", "UTSMode", "UsernsMode", "CgroupParent", "Isolation", "CpusetCpus", "CpusetMems"} {
		if !h.equal(key, "") {
			return fail()
		}
	}
	for _, key := range []string{"IpcMode", "CgroupnsMode"} {
		if !h.equal(key, "") && !h.equal(key, "private") {
			return fail()
		}
	}
	for _, key := range []string{"Binds", "Devices", "DeviceRequests", "VolumesFrom", "Links", "ExtraHosts", "GroupAdd", "Dns", "DnsOptions", "DnsSearch"} {
		if !h.list(key) {
			return fail()
		}
	}
	if !h.emptyObject("Sysctls") || !h.equal("OomKillDisable", false) {
		return fail()
	}
	for _, key := range []string{"CpuShares", "CpuPeriod", "CpuQuota", "CpuRealtimePeriod", "CpuRealtimeRuntime", "MemoryReservation"} {
		if !h.equal(key, 0) {
			return fail()
		}
	}
	// Only the null/unset swappiness representation is supported.
	if v, exists := h["MemorySwappiness"]; !exists || v != nil {
		return fail()
	}
	// These optional engine-version fields may only be unset. Unknown unrelated
	// metadata is allowed, but known alternative resource/device/config knobs
	// cannot widen the fixed policy.
	for _, key := range []string{"DeviceCgroupRules", "Ulimits", "BlkioDeviceReadBps", "BlkioDeviceWriteBps", "BlkioDeviceReadIOps", "BlkioDeviceWriteIOps", "BlkioWeightDevice"} {
		if _, exists := h[key]; exists && !h.list(key) {
			return fail()
		}
	}
	for _, key := range []string{"StorageOpt", "Annotations"} {
		if _, exists := h[key]; exists && !h.emptyObject(key) {
			return fail()
		}
	}
	for _, key := range []string{"OomScoreAdj", "BlkioWeight", "CpuCount", "CpuPercent", "IOMaximumIOps", "IOMaximumBandwidth"} {
		if _, exists := h[key]; exists && !h.equal(key, 0) {
			return fail()
		}
	}
	if v, exists := h["Init"]; exists && v != nil && !h.equal("Init", false) {
		return fail()
	}
	if !c4DockerMounts(m, h, plan.candidate) {
		return fail()
	}
	n := m.object("NetworkSettings")
	networks := n.object("Networks")
	if !n.emptyObject("Ports") || len(networks) != 1 || networks.object("none") == nil {
		return fail()
	}
	s := m.object("State")
	running, okRunning := s["Running"].(bool)
	oom, okOOM := s["OOMKilled"].(bool)
	exit, okExit := s.integer("ExitCode")
	started, okStarted := s.str("StartedAt")
	at, err := time.Parse(time.RFC3339Nano, started)
	if !okRunning || !okOOM || !okExit || exit < 0 || exit > 255 || !okStarted || err != nil || at.Year() < 1 || (running && at.IsZero()) {
		return fail()
	}
	return c4DockerInspection{C4Inspection: C4Inspection{ID: id, Name: plan.operation.ContainerName, Labels: wantLabels, Running: running, OOMKilled: oom, ExitCode: int(exit)}, StartedAt: at}, nil
}

func c4DockerTmpfsOptions(actual, want string) bool {
	a, w := strings.Split(actual, ","), strings.Split(want, ",")
	if len(a) != len(w) {
		return false
	}
	allowed := map[string]bool{}
	for _, s := range w {
		allowed[s] = true
	}
	for _, s := range a {
		if !allowed[s] {
			return false
		}
		delete(allowed, s)
	}
	return len(allowed) == 0
}

func c4DockerMounts(m, h c4DockerObject, candidate string) bool {
	tmp := h.object("Tmpfs")
	if len(tmp) != 2 {
		return false
	}
	for key, want := range map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=67108864,nr_inodes=16384,mode=1777", "/foundry-build": "rw,nosuid,nodev,exec,size=268435456,nr_inodes=16384,mode=1777"} {
		actual, ok := tmp.str(key)
		if !ok || !c4DockerTmpfsOptions(actual, want) {
			return false
		}
	}
	declared, ok := h["Mounts"].([]any)
	if !ok || len(declared) != 1 {
		return false
	}
	d, ok := declared[0].(c4DockerObject)
	if !ok || !d.equal("Type", "bind") || !d.equal("Source", candidate) || !d.equal("Target", "/workspace") || !d.equal("ReadOnly", true) || !d.exactCase("VolumeOptions", "TmpfsOptions", "ImageOptions", "Consistency", "BindOptions") {
		return false
	}
	for _, key := range []string{"VolumeOptions", "TmpfsOptions", "ImageOptions"} {
		if _, exists := d[key]; exists && !d.emptyObject(key) {
			return false
		}
	}
	if _, exists := d["Consistency"]; exists && !d.equal("Consistency", "") {
		return false
	}
	if v, exists := d["BindOptions"]; exists && v != nil {
		b, ok := v.(c4DockerObject)
		if !ok {
			return false
		}
		for key := range b {
			switch key {
			case "Propagation":
				if !b.equal(key, "") && !b.equal(key, "rprivate") {
					return false
				}
			case "NonRecursive", "CreateMountpoint", "ReadOnlyNonRecursive", "ReadOnlyForceRecursive":
				if !b.equal(key, false) {
					return false
				}
			default:
				return false
			}
		}
	}
	// Accept bind-only Mounts or bind plus both declared tmpfs entries, never
	// a partial/extra mount set. HostConfig must carry the fixed tmpfs options
	// in either shape. These accepted shapes are not observed daemon evidence.
	effective, ok := m["Mounts"].([]any)
	if !ok || (len(effective) != 1 && len(effective) != 3) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range effective {
		v, ok := item.(c4DockerObject)
		if !ok {
			return false
		}
		dest, ok := v.str("Destination")
		if !ok || seen[dest] {
			return false
		}
		seen[dest] = true
		switch dest {
		case "/workspace":
			if !v.equal("Type", "bind") || !v.equal("Source", candidate) || !v.equal("RW", false) || !v.equal("Propagation", "rprivate") || !(v.equal("Mode", "") || v.equal("Mode", "ro")) {
				return false
			}
		case "/tmp", "/foundry-build":
			if !v.equal("Type", "tmpfs") || !v.equal("Source", "") || !v.equal("RW", true) || !v.equal("Mode", "") || !v.equal("Propagation", "") {
				return false
			}
		default:
			return false
		}
	}
	return seen["/workspace"]
}
