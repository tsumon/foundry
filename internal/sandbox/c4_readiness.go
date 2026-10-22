package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"os"
	"strings"
	"time"
)

// FailureCode contains only a closed stage/reason pair, never CLI output.
// A failure cannot carry a fingerprint or authorize subsequent execution.
type C4ReadinessResult struct{ Fingerprint, FailureCode string }

func c4ReadinessFailure(stage string, err error) (C4ReadinessResult, error) {
	reason := "protocol_failed"
	switch {
	case errors.Is(err, context.Canceled):
		reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "timeout"
	case errors.Is(err, errC4DockerTrust):
		reason = "trust_failed"
	case errors.Is(err, errC4DockerProcess):
		reason = "process_failed"
	case errors.Is(err, errC4DockerCommand):
		reason = "command_failed"
	case errors.Is(err, errC4DockerOutputLimit):
		reason = "output_limit"
	}
	return C4ReadinessResult{FailureCode: stage + "_" + reason}, err
}

// CheckC4DockerReadiness performs only the three fixed queries. The caller must
// persist and consume dispatch authority before calling; this function neither
// grants permission nor discovers defaults. Disabled construction has no effects.
func CheckC4DockerReadiness(ctx context.Context, p domain.C4Profile, c C4DockerConfig) (C4ReadinessResult, error) {
	if !c4ReadinessAllowed(ctx, p, c) {
		return C4ReadinessResult{FailureCode: "readiness_gate_failed"}, errC4Docker
	}
	return checkC4DockerReadiness(ctx, p, c, c4OSProcess{}, &c4DockerTrust{trust: c.Trust, fs: c4DockerOSFS{}, uid: uint32(os.Getuid())})
}
func c4ReadinessAllowed(ctx context.Context, p domain.C4Profile, c C4DockerConfig) bool {
	return c.Enabled && p.Mode == "docker" && p.ControlVersion == 1 && p.Validate() == nil && p.DockerPath == c.Trust.DockerPath && p.Endpoint == c.Trust.Endpoint && ctx.Err() == nil
}
func checkC4DockerReadiness(ctx context.Context, p domain.C4Profile, c C4DockerConfig, process c4DockerProcess, trust c4DockerTrustCheck) (C4ReadinessResult, error) {
	if !c4ReadinessAllowed(ctx, p, c) || process == nil || trust == nil {
		return C4ReadinessResult{FailureCode: "readiness_gate_failed"}, errC4Docker
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	plan := C4Plan{profile: p}
	driver := newC4DockerDriver(plan, process, trust, "")
	versionRaw, err := driver.control(ctx, plan.command("version", "--format", "{{json .}}"), 64<<10)
	if err != nil {
		return c4ReadinessFailure("version", err)
	}
	version, err := c4ReadinessVersion(versionRaw)
	if err != nil {
		return c4ReadinessFailure("version", errC4DockerProtocol)
	}
	infoRaw, err := driver.control(ctx, plan.command("info", "--format", "{{json .}}"), 64<<10)
	if err != nil {
		return c4ReadinessFailure("info", err)
	}
	identity, err := c4ReadinessInfo(infoRaw)
	if err != nil {
		return c4ReadinessFailure("info", errC4DockerProtocol)
	}
	image, err := driver.control(ctx, plan.imageInspection(), c4DockerInspectLimit)
	if err != nil {
		return c4ReadinessFailure("image", err)
	}
	if validateC4DockerImage(image, p) != nil {
		return c4ReadinessFailure("image", errC4DockerProtocol)
	}
	if ctx.Err() != nil {
		return c4ReadinessFailure("image", ctx.Err())
	}
	// Explicit normalization makes aarch64 equivalent to arm64; the canonical
	// profile digest binds image, endpoint, executable and immutable policy.
	material, _ := json.Marshal([]string{"foundry:c4:readiness:v1", p.ProfileDigest(), version, identity, "linux/arm64"})
	digest := sha256.Sum256(material)
	return C4ReadinessResult{Fingerprint: hex.EncodeToString(digest[:])}, nil
}
func c4ReadinessVersion(raw []byte) (string, error) {
	v, err := c4DockerJSON(raw, 64<<10)
	m, ok := v.(c4DockerObject)
	if err != nil || !ok {
		return "", errC4DockerProtocol
	}
	server := m.object("Server")
	version, ok := server.str("Version")
	if !ok || !c4ReadinessToken(version) || !server.equal("Os", "linux") || !server.equal("Arch", "arm64") {
		return "", errC4DockerProtocol
	}
	return version, nil
}
func c4ReadinessInfo(raw []byte) (string, error) {
	v, err := c4DockerJSON(raw, 64<<10)
	m, ok := v.(c4DockerObject)
	if err != nil || !ok {
		return "", errC4DockerProtocol
	}
	id, ok := m.str("ID")
	if !ok || !c4ReadinessToken(id) || !m.equal("OSType", "linux") || !(m.equal("Architecture", "arm64") || m.equal("Architecture", "aarch64")) {
		return "", errC4DockerProtocol
	}
	return id, nil
}
func c4ReadinessToken(s string) bool {
	if len(s) == 0 || len(s) > 128 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
			return false
		}
	}
	return true
}
