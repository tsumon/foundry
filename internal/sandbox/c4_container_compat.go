package sandbox

import "encoding/json"

// Docker omits these two empty maps from container inspect JSON. Normalize only
// at the trusted CLI transport boundary; the security parser remains strict.
// Parse before rewriting so duplicate keys, aliases, invalid Unicode, oversized
// or deeply nested input cannot be erased by ordinary JSON unmarshalling.
func normalizeC4ContainerEmptyFields(raw []byte) ([]byte, error) {
	m, err := c4DockerInspectObject(raw)
	if err != nil {
		return nil, errC4DockerProtocol
	}
	for _, field := range [][2]string{{"Config", "ExposedPorts"}, {"HostConfig", "Sysctls"}} {
		parent := m.object(field[0])
		if parent == nil || !parent.exactCase(field[1]) {
			return nil, errC4DockerProtocol
		}
		if _, exists := parent[field[1]]; !exists {
			parent[field[1]] = nil
		}
	}
	// Moby defaults unset OomKillDisable to false and resets it to nil on
	// kernels without that capability (notably cgroup v2). Only explicit null
	// means this default here: absent, true, aliases and wrong types still fail
	// the unchanged strict parser. Never normalize other resource limits.
	host := m.object("HostConfig")
	if !host.exactCase("OomKillDisable") {
		return nil, errC4DockerProtocol
	}
	if value, exists := host["OomKillDisable"]; exists && value == nil {
		host["OomKillDisable"] = false
	}
	// Preserve every existing value and unknown metadata. The downstream parser
	// applies its original limits again to the normalized representation.
	return json.Marshal([]c4DockerObject{m})
}
