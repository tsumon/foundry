package httpapi

import (
	"foundry/internal/domain"
	"foundry/internal/engine"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

func (s *server) codingTasks(w http.ResponseWriter, r *http.Request) {
	available := s.codingWorkbench && s.engine.CodingWorkbenchAvailable() && !s.recoveryReadOnly
	if r.Method == http.MethodGet {
		reason := ""
		if !s.codingWorkbench {
			reason = "coding_workbench_disabled"
		} else if runtime.GOOS == "windows" {
			reason = "windows_docker_trust_unavailable"
		} else if s.recoveryReadOnly {
			reason = "recovery_read_only"
		} else if !available {
			reason = "coding_runtime_unavailable_or_expired"
		}
		endpoint := "unix:///var/run/docker.sock"
		dockerPath := "/usr/bin/docker"
		if runtime.GOOS == "darwin" {
			dockerPath = "/Applications/Docker.app/Contents/Resources/bin/docker"
			if home, err := os.UserHomeDir(); err == nil {
				endpoint = "unix://" + filepath.Join(home, ".docker", "run", "docker.sock")
			}
		}
		profile := domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_model_v1", Mode: "docker", Platform: "linux/arm64", DockerPath: dockerPath, Endpoint: endpoint, Verifier: "/opt/foundry/verify-go-v1"}
		writeJSON(w, 200, map[string]any{"enabled": s.codingWorkbench, "available": available, "reason": reason, "profile": profile, "limits": map[string]int{"max_calls": 16, "max_repairs": 2}, "provider_families": []string{"openai"}, "approval_required": []string{"readiness", "baseline", "context", "execute", "delivery"}})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 404, "not_found", "route not found")
		return
	}
	if !available {
		writeError(w, 403, "coding_workbench_unavailable", "controlled coding workbench is unavailable")
		return
	}
	var q engine.CreateRequest
	if err := decodeAccountJSON(w, r, &q); err != nil {
		writeDecodeError(w, err)
		return
	}
	if q.Spec.C4 == nil || q.Spec.C4.ID != "c4_go_model_v1" {
		writeError(w, 400, "validation", "model-backed C4 declaration required")
		return
	}
	m, err := s.engine.Create(r.Context(), q)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	s.writeMission(w, m, nil)
}
