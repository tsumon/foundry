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
		dockerProfile := domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_model_v1", Mode: "docker", Platform: "linux/arm64", DockerPath: dockerPath, Endpoint: endpoint, Verifier: "/opt/foundry/verify-go-v1"}
		profile, installed := s.engine.CodingVMProfile()
		if !installed {
			profile = domain.C4Profile{SchemaVersion: 1, ControlVersion: 1, ID: "c4_go_model_v1", Mode: "embedded_vm", Verifier: "/opt/foundry/verify-go-v1"}
			if reason == "" {
				reason = "embedded_vm_bundle_unavailable"
			}
		}
		writeJSON(w, 200, map[string]any{"enabled": s.codingWorkbench, "available": available && installed, "reason": reason, "default_backend": "embedded_vm", "profile": profile, "backends": []any{map[string]any{"mode": "embedded_vm", "available": available && installed, "profile": profile, "prerequisite": "bundled runtime and hardware virtualization; readiness approval required"}, map[string]any{"mode": "docker", "available": available && runtime.GOOS != "windows", "profile": dockerProfile, "prerequisite": "explicit selection and independently approved Docker readiness"}}, "limits": map[string]int{"max_calls": 16, "max_repairs": 2}, "provider_families": []string{"openai", "custom"}, "approval_required": []string{"readiness", "baseline", "context", "execute", "delivery"}})
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
	if q.Spec.C4.Mode == "embedded_vm" {
		trusted, ok := s.engine.CodingVMProfile()
		p := q.Spec.C4
		if !ok || p.EmbeddedVM != nil && (p.Validate() != nil || p.ProfileDigest() != trusted.ProfileDigest()) || p.DockerPath != "" || p.Endpoint != "" || p.ImageID != "" || p.SchemaVersion != 0 && p.SchemaVersion != 1 || p.ControlVersion != 0 && p.ControlVersion != 1 || p.Platform != "" && p.Platform != trusted.Platform || p.Verifier != "" && p.Verifier != trusted.Verifier {
			writeError(w, 400, "validation", "installed VM identity required; client substitution rejected")
			return
		}
		q.Spec.C4 = &trusted
	}
	m, err := s.engine.Create(r.Context(), q)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	s.writeMission(w, m, nil)
}
