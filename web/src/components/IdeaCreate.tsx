import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { ideaApi } from "../ideaApi";
import type { Mission, Profile } from "../types";

export function IdeaCreate({
  onCancel,
  onCreated,
  active = true,
  onBusyChange,
}: {
  onCancel: () => void;
  onCreated: (mission: Mission) => void;
  active?: boolean;
  onBusyChange?: (busy: boolean) => void;
}) {
  const [title, setTitle] = useState("");
  const [goal, setGoal] = useState("");
  const [acceptance, setAcceptance] = useState("");
  const [profileID, setProfileID] = useState("");
  const [maxCalls, setMaxCalls] = useState(16);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [available, setAvailable] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const lock = useRef(false);
  useEffect(() => {
    if (!active) return;
    let alive = true;
    setLoading(true);
    void Promise.all([api.profiles(), ideaApi.capability()])
      .then(([models, capability]) => {
        if (!alive) return;
        const options = models.profiles ?? [];
        setProfiles(options);
        setProfileID((current) =>
          options.some((p) => p.id === current)
            ? current
            : models.settings.default_profile_id,
        );
        setAvailable(capability.available === true);
      })
      .catch((cause) => {
        if (alive) setError(errorMessage(cause));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [active]);
  const requirements = acceptance
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
  const canCreate =
    active &&
    !loading &&
    available &&
    profiles.some((p) => p.id === profileID) &&
    title.trim() &&
    goal.trim() &&
    requirements.length > 0 &&
    maxCalls >= 1 &&
    maxCalls <= 16 &&
    Number.isInteger(maxCalls);
  async function create(event: React.FormEvent) {
    event.preventDefault();
    if (!canCreate || lock.current) return;
    lock.current = true;
    setBusy(true);
    onBusyChange?.(true);
    setError("");
    try {
      const result = await ideaApi.create({
        title: title.trim(),
        goal: goal.trim(),
        acceptance: requirements,
        profile_id: profileID,
        max_calls: maxCalls,
      });
      onCreated(result.mission);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      lock.current = false;
      setBusy(false);
      onBusyChange?.(false);
    }
  }
  return (
    <section className="idea-create" aria-label="从想法创建应用">
      <header className="delivery-header">
        <div className="eyebrow">从想法到可用应用</div>
        <h1>你想做什么？</h1>
        <p className="muted">
          描述目标，Foundry
          为你建项目、规划、编码和验证。完成后预览应用，下载完整源码。
        </p>
      </header>
      <form onSubmit={(event) => void create(event)}>
        <fieldset disabled={busy}>
          <label>
            应用名称
            <input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              maxLength={200}
              placeholder="例如：一人公司的客户跟进看板"
              required
            />
          </label>
          <label>
            描述你的想法
            <textarea
              value={goal}
              onChange={(e) => setGoal(e.target.value)}
              maxLength={12000}
              rows={5}
              placeholder="给谁使用？要解决什么问题？希望怎样操作？"
              required
            />
          </label>
          <label>
            怎样算做好了
            <textarea
              value={acceptance}
              onChange={(e) => setAcceptance(e.target.value)}
              maxLength={4000}
              rows={4}
              placeholder={
                "每行一项，例如：\n可以添加客户并记录下一步行动\n按跟进状态筛选\n空列表和无效输入有清晰提示"
              }
              required
            />
          </label>
          <div className="form-columns">
            <label>
              任务模型
              <select
                value={profileID}
                onChange={(e) => setProfileID(e.target.value)}
                required
              >
                <option value="">选择已配置的模型</option>
                {profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} · {p.model}
                  </option>
                ))}
              </select>
            </label>
            <label>
              调用上限
              <input
                type="number"
                min={1}
                max={16}
                step={1}
                value={maxCalls}
                onChange={(e) => setMaxCalls(Number(e.target.value))}
                required
              />
            </label>
          </div>
        </fieldset>
        <div className="idea-output">
          <strong>交付：浏览器应用 + 完整源码 ZIP</strong>
          <p>
            HTML、CSS、JavaScript，无需安装依赖。当前适合交互工具和本地数据应用；服务端、数据库、在线服务及
            DMG / EXE 应用生成尚未包含。
          </p>
        </div>
        <p className="field-help">
          创建只准备项目，不调用模型或启动
          VM。随后分别批准隔离验证、上下文共享、计划执行和交付；调用上限覆盖规划、编码、修复和审查。
        </p>
        {!loading && !available && (
          <p role="status" className="note warning-note">
            内置应用运行时不可用。请使用带完整沙盒的新版桌面安装包；不会改为宿主机执行。
          </p>
        )}
        {!loading && profiles.length === 0 && (
          <p className="note">
            先在设置中添加模型账户与模型配置，回来后这里会保留已填写的想法。
          </p>
        )}
        {error && (
          <p role="alert" className="form-error">
            {error}
          </p>
        )}
        <div className="actions form-actions">
          <button
            className="primary"
            type="submit"
            disabled={!canCreate || busy}
          >
            {busy ? "正在创建项目…" : "创建应用任务"}
          </button>
          <button type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
        </div>
      </form>
    </section>
  );
}
