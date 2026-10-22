package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"foundry/internal/domain"
)

// Hand-authored rendering records only; no capability, process or daemon.
func c4ReportFixture() domain.Mission {
	p := c4Profile()
	p.Mode, p.ControlVersion = "docker", 1
	zero := 0
	o := domain.C4Operation{SchemaVersion: 1, ID: "op", Kind: "candidate", State: "finished", ContainerID: "container", Owner: "owner", ReadinessID: "ready", ReadinessDigest: "receipt", Approval: &domain.C4Approval{}, Dispatch: &domain.C4Fact{}, Created: &domain.C4Fact{}, Started: &domain.C4Fact{}, Result: &domain.C4Result{Class: "passed", ExitCode: &zero, Tests: 1}, Cleanup: []domain.C4Cleanup{{State: "confirmed", ContainerID: "container", Owner: "owner"}}}
	m := domain.Mission{Spec: domain.Spec{C4: p}, Profiles: map[domain.Role]domain.Profile{}, C4Operations: []domain.C4Operation{o}, C4Controls: []domain.C4Control{{ID: "ready", MaterialDigest: "receipt", SourceKind: "docker_c4_control", Result: &domain.C4ControlResult{Status: "ready"}}}}
	for _, role := range domain.RolePolicyRoles() {
		m.Profiles[role] = domain.Profile{Kind: "synthetic"}
	}
	return m
}

// Catches inferring simulated commands from simulated agents, or inferring
// real execution from a Docker declaration without complete execution evidence.
func TestC4ReportSeparatesAgentsFromExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.Mission)
		want   string
	}{
		{"real", func(*domain.Mission) {}, "真实 Docker 测试（隔离命令执行） · 验收命令通过"},
		{"failed real", func(m *domain.Mission) {
			n := 1
			m.C4Operations[0].Result.Class = "test_failed"
			m.C4Operations[0].Result.ExitCode = &n
		}, "真实 Docker 测试（隔离命令执行） · 验收命令未通过"},
		{"synthetic", func(m *domain.Mission) { m.Spec.C4.Mode = "synthetic" }, "流程模拟（未执行命令） · 模拟验证通过"},
		{"no start", func(m *domain.Mission) { m.C4Operations[0].Started = nil }, "来源未记录"},
		{"no cleanup", func(m *domain.Mission) { m.C4Operations[0].Cleanup = nil }, "来源未记录"},
		{"no tests", func(m *domain.Mission) { m.C4Operations[0].Result.Tests = 0 }, "来源未记录"},
		{"no readiness", func(m *domain.Mission) { m.C4Controls = nil }, "来源未记录"},
		{"wrong readiness", func(m *domain.Mission) { m.C4Controls[0].SourceKind = "synthetic_c4_control" }, "来源未记录"},
		{"future profile", func(m *domain.Mission) { m.Spec.C4.ControlVersion = 9 }, "来源未记录"},
		{"uncontrolled Docker declaration", func(m *domain.Mission) { m.Spec.C4.ControlVersion = 0 }, "来源未记录"},
		{"missing profile", func(m *domain.Mission) { m.Spec.C4 = nil }, "来源未记录"},
		{"pending", func(m *domain.Mission) { m.C4Operations[0].Result = nil }, "尚无结果（不证明已执行）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := c4ReportFixture()
			tc.change(&m)
			before, _ := json.Marshal(m)
			report := marshalReport(m)
			if !strings.Contains(report, "worker · 本地模拟 Agent（任务配置）") || !strings.Contains(report, "第 1 项 · 候选 · "+tc.want) || strings.Contains(report, "Synthetic models and command outcomes only") {
				t.Fatalf("incorrect provenance in rendered report: want %q; got %s", tc.want, report)
			}
			if tc.want == "来源未记录" && strings.Contains(report, "真实 Docker 测试（隔离命令执行）") {
				t.Fatal("incomplete evidence upgraded")
			}
			after, _ := json.Marshal(m)
			if string(before) != string(after) {
				t.Fatal("report mutated evidence")
			}
		})
	}
}

func TestC4ReportMixedRecordsAndAgentConfigurations(t *testing.T) {
	m := c4ReportFixture()
	m.Profiles[domain.Worker] = domain.Profile{Kind: "openai"}
	delete(m.Profiles, domain.Debugger)
	unknown := m.C4Operations[0]
	unknown.ID = "unknown"
	unknown.Kind = "baseline"
	unknown.Cleanup = nil
	m.C4Operations = append(m.C4Operations, unknown)
	report := marshalReport(m)
	for _, want := range []string{"worker · 非模拟 Agent 配置（不证明真实调用）", "debugger · Agent 来源未记录", "第 1 项 · 候选 · 真实 Docker 测试", "第 2 项 · 基线 · 来源未记录", "基线不替代候选验证或交付审批"} {
		if !strings.Contains(report, want) {
			t.Fatalf("missing %q in report: %s", want, report)
		}
	}
}
