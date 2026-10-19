import { useCallback, useEffect, useRef, useState } from "react";
import { capabilityApi } from "../../capabilityApi";
import type { CapabilitySnapshot, ProjectChoices } from "../../capabilityTypes";
import { ModelSettings } from "../ModelSettings";
import { MCPSettings } from "./MCPSettings";
import { SkillSettings } from "./SkillSettings";
import { GitSettings } from "./GitSettings";
import { capabilityError } from "./useCapabilityRequest";
import { HookSettings } from "../hooks/HookSettings";
import type { HookMissionChoice } from "../../hookTypes";

const sections = ["模型", "工具与 MCP", "Skills", "Git", "事件钩子"] as const;
export function CapabilitySettings({
  enabled,
  catalog,
  missions = [],
  onOpenMission = () => {},
}: {
  enabled: boolean;
  catalog: ProjectChoices;
  missions?: HookMissionChoice[];
  onOpenMission?: (id: string) => void;
}) {
  const [section, setSection] = useState<(typeof sections)[number]>("模型");
  const [requested, setRequested] = useState(false);
  const [snapshot, setSnapshot] = useState<CapabilitySnapshot | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [received, setReceived] = useState("");
  const [ready, setReady] = useState(false);
  const state = useRef({ active: false, epoch: 0, request: 0, mutation: 0 });
  const refresh = useCallback(async () => {
    const current = state.current;
    if (!current.active) return;
    const epoch = current.epoch,
      request = ++current.request,
      mutation = current.mutation;
    const valid = () =>
      current.active && current.epoch === epoch && current.request === request;
    setLoading(true);
    setError("");
    try {
      const data = await capabilityApi.snapshot();
      if (valid() && current.mutation === mutation) {
        setSnapshot(data);
        setReceived(new Date().toISOString());
        setReady(true);
      }
    } catch (e) {
      if (valid()) setError(capabilityError(e));
    } finally {
      if (valid()) setLoading(false);
    }
  }, []);
  useEffect(() => {
    const current = state.current;
    current.active = enabled && requested;
    current.epoch++;
    setReady(false);
    setLoading(false);
    if (current.active) void refresh();
    return () => {
      current.active = false;
      current.epoch++;
    };
  }, [enabled, requested, refresh]);
  const update = useCallback(
    (change: (previous: CapabilitySnapshot) => CapabilitySnapshot) => {
      if (!state.current.active) return;
      state.current.mutation++;
      setSnapshot((previous) => (previous ? change(previous) : previous));
    },
    [],
  );
  // Refresh does not cancel an already-authorized panel request. Its merge
  // invalidates older snapshot reads; a new session still requires a fresh read.
  const props = { enabled: enabled && ready, snapshot: snapshot!, update };
  return (
    <div className="capability-settings">
      <nav className="settings-subnav" aria-label="设置分类">
        {sections.map((name) => (
          <button
            key={name}
            aria-pressed={section === name}
            onClick={() => {
              setSection(name);
              if (!["模型", "事件钩子"].includes(name)) setRequested(true);
            }}
          >
            {name}
          </button>
        ))}
      </nav>
      {!enabled && (
        <p className="note capability-session" role="status">
          正在等待本地会话，设置操作暂不可用。
        </p>
      )}
      {section === "模型" && enabled && <ModelSettings />}
      <div hidden={section !== "事件钩子"}>
        <HookSettings
          enabled={enabled}
          visible={section === "事件钩子"}
          missions={missions}
          onOpenMission={onOpenMission}
        />
      </div>
      <div
        hidden={section === "模型" || section === "事件钩子"}
        className="page capability-page"
      >
        <header className="page-header">
          <p className="eyebrow">设置 / 能力管理</p>
          <h1>{section === "模型" ? "能力管理" : section}</h1>
          <p className="muted">
            登记与观察本身不是执行授权。显式选择的受控任务可使用固定仓库内建读取与精确
            Skill 上下文绑定；只有 controller
            拥有的本地合成测试服务可在任务内逐次审批。 通用 MCP tools/call、外部
            MCP、Skill 脚本、宿主命令、Git 写入、钩子/webhook
            执行与部署仍未开放。
          </p>
        </header>
        <div className="capability-read-state">
          {loading && <span role="status">正在读取能力设置…</span>}
          {received && (
            <span className="muted">
              最近完整读取：{received}
              {error || !enabled ? "（保留上次数据）" : ""}
            </span>
          )}
          <button disabled={!enabled || loading} onClick={() => void refresh()}>
            重新读取能力设置
          </button>
        </div>
        {error && (
          <p role="alert" className="form-error">
            {error}
          </p>
        )}
        {!snapshot && !loading && (
          <p className="note">能力设置尚未读取，不能据此认定没有记录。</p>
        )}
        {snapshot && (
          <>
            <div hidden={section !== "工具与 MCP"}>
              <MCPSettings {...props} visible={section === "工具与 MCP"} />
            </div>
            <div hidden={section !== "Skills"}>
              <SkillSettings {...props} />
            </div>
            <div hidden={section !== "Git"}>
              <GitSettings {...props} catalog={catalog} />
            </div>
          </>
        )}
      </div>
    </div>
  );
}
