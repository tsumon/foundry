package engine

import (
	"bytes"
	"encoding/json"
	"fmt"

	"foundry/internal/domain"
)

// Only the new profile adds call provenance. Historical report bytes and seals
// remain unchanged. Main chat is excluded so later explanation cannot rewrite
// the exported coding result; every included envelope is integrity checked.
func (e *Engine) deliveryReport(m domain.Mission) (string, error) {
	report := marshalReport(m)
	if len(m.ModelContinuations) > 0 || m.ModelContinuationsInvalid {
		lineage, err := modelContinuationReport(m)
		if err != nil {
			return "", err
		}
		report += lineage
	}
	if !isModelC4(m) {
		return report, nil
	}
	all, err := e.verifiedInvocations(m.ID)
	if err != nil {
		return "", err
	}
	calls := []domain.Invocation{}
	for _, v := range all {
		if v.Epoch == m.Epoch && v.Kind == "model" && v.Role != domain.Main {
			calls = append(calls, v)
		}
	}
	raw, err := json.MarshalIndent(struct {
		Snapshot *domain.MissionModelSnapshot `json:"model_snapshot"`
		Controls []domain.C4Control           `json:"docker_controls"`
		Calls    []domain.Invocation          `json:"model_invocations"`
	}{m.ModelSnapshot, m.C4Controls, calls}, "", "  ")
	if err != nil {
		return "", err
	}
	if m.Spec.C4.Mode == "embedded_vm" {
		raw, err = json.MarshalIndent(struct {
			Snapshot *domain.MissionModelSnapshot `json:"model_snapshot"`
			Identity *domain.C4VMIdentity         `json:"embedded_vm"`
			Controls []domain.C4Control           `json:"vm_controls"`
			Calls    []domain.Invocation          `json:"model_invocations"`
		}{m.ModelSnapshot, m.Spec.C4.EmbeddedVM, m.C4Controls, calls}, "", "  ")
		if err != nil {
			return "", err
		}
		return report + "\n## Model-backed coding · independent provenance\n\nModel receipts and built-in VM readiness, baseline, candidate tests and owned cleanup are distinct facts. Local model protocol fixtures do not prove real-provider acceptance.\n\n```json\n" + string(raw) + "\n```\n", nil
	}
	return report + "\n## Model-backed coding · independent provenance\n\nSimulation routes are owned local integration observations, not real-provider acceptance. Each model receipt, Docker readiness, baseline, candidate test and cleanup is a distinct fact. Token usage is not remaining quota or a monetary hard cap.\n\n```json\n" + string(raw) + "\n```\n", nil
}

// New exports identify both historical and effective authority without
// rewriting any earlier report or disclosing identity subjects/client IDs.
func modelContinuationReport(m domain.Mission) (string, error) {
	effective, err := domain.EffectiveModelSnapshot(m)
	if err != nil {
		return "", err
	}
	type roleView struct {
		Model           string                         `json:"model"`
		AuthorizationID string                         `json:"authorization_id,omitempty"`
		Catalog         *domain.ModelCatalogProvenance `json:"catalog,omitempty"`
	}
	type record struct {
		ID                string                        `json:"id"`
		Digest            string                        `json:"digest"`
		PredecessorDigest string                        `json:"predecessor_digest"`
		ReplacementDigest string                        `json:"replacement_digest"`
		Approval          *domain.ModelContinuationFact `json:"approval,omitempty"`
		Application       *domain.ModelContinuationFact `json:"application,omitempty"`
	}
	view := struct {
		OriginalDigest  string                   `json:"original_snapshot_digest"`
		EffectiveDigest string                   `json:"effective_snapshot_digest"`
		Roles           map[domain.Role]roleView `json:"effective_roles"`
		Continuations   []record                 `json:"model_continuations"`
	}{OriginalDigest: m.ModelSnapshot.Digest, EffectiveDigest: effective.Digest, Roles: map[domain.Role]roleView{}}
	for role, sel := range effective.Roles {
		v := roleView{Model: sel.Profile.Model, Catalog: sel.Catalog}
		if sel.Authorization != nil {
			v.AuthorizationID = sel.Authorization.ID
		}
		view.Roles[role] = v
	}
	for _, c := range m.ModelContinuations {
		view.Continuations = append(view.Continuations, record{c.ID, c.Digest, c.PredecessorDigest, c.Replacement.Digest, c.Approval, c.Application})
	}
	raw, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return "", err
	}
	return "\n## Explicit task authorization continuation\n\nOriginal snapshots, consumed calls and unknown usage remain historical facts. Local authorization IDs identify individual sign-ins, not registered clients. Workspace identity is not independently verified.\n\n```json\n" + string(raw) + "\n```\n", nil
}

// Rendering only: no execution, readiness refresh, mutation or new authority.
func writeC4ReportSources(out *bytes.Buffer, m domain.Mission) {
	out.WriteString("\n## C4 · Agent 与测试执行来源\n\nAgent 配置与命令证据分别记录；角色配置不证明真实模型调用，Docker 配置本身不证明测试执行。每项来源与结果独立，未知来源不升级为真实执行。基线不替代候选验证或交付审批。\n\n### Agent 来源\n\n")
	for _, role := range domain.RolePolicyRoles() {
		label := "Agent 来源未记录"
		if p, ok := m.Profiles[role]; ok && p.Kind != "" {
			label = "非模拟 Agent 配置（不证明真实调用）"
			if p.Kind == "synthetic" {
				label = "本地模拟 Agent（任务配置）"
			}
		}
		fmt.Fprintf(out, "- %s · %s\n", role, label)
	}
	out.WriteString("\n### 测试执行来源与结果\n\n")
	if len(m.C4Operations) == 0 {
		out.WriteString("- 尚无执行记录（不证明已执行）\n")
	}
	for i, o := range m.C4Operations {
		kind := "未知类型"
		if o.Kind == "baseline" {
			kind = "基线"
		} else if o.Kind == "candidate" {
			kind = "候选"
		}
		if o.Result == nil {
			fmt.Fprintf(out, "- 第 %d 项 · %s · 尚无结果（不证明已执行）\n", i+1, kind)
			continue
		}
		v := domain.Verification{}
		if !m.C4Invalid && m.Spec.C4 != nil && m.Spec.C4.Validate() == nil && (m.Spec.C4.Mode == "synthetic" || m.Spec.C4.ControlVersion == 1) && o.SchemaVersion == 1 {
			v = c4Verification(m, o)
		}
		label := v.SourceLabel()
		if v.EffectiveSource() == "isolated_execution" {
			label = "真实 Docker 测试（隔离命令执行）"
			if m.Spec.C4.Mode == "embedded_vm" {
				label = "真实内置 VM 测试（隔离命令执行）"
			}
		}
		fmt.Fprintf(out, "- 第 %d 项 · %s · %s · %s\n", i+1, kind, label, v.OutcomeLabel())
	}
}
