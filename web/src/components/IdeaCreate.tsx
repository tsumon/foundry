import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { ideaApi } from "../ideaApi";
import type { Mission, Profile } from "../types";

const starterTemplates = [
  {
    label: "客户跟进看板",
    title: "一人公司客户跟进看板",
    goal: "为一人公司管理潜在客户、跟进阶段、下次沟通日期和备注记录，支持本地保存和 JSON 导出备份。",
    acceptance:
      "可以新增和编辑客户卡片并选择跟进状态\n支持按状态筛选和关键词搜索\n支持将客户数据导出和导入 JSON 备份，刷新页面后数据保留",
  },
  {
    label: "报价利润计算器",
    title: "项目报价与利润计算器",
    goal: "快速计算外包/产品服务报价，支持按人天成本、税率、风险缓冲和折扣拆解明细，并生成可复制的报价单摘要。",
    acceptance:
      "可以添加多项工作条目并实时计算总报价、税额与净利润率\n输入非数字或负数时给出明确校验提示\n支持一键复制结构化报价摘要",
  },
  {
    label: "内容排期清单",
    title: "内容发布排期与检查清单",
    goal: "管理周度视频/文章选题、发布渠道、发布日期和发布前核对项，在日历/列表视图间切换。",
    acceptance:
      "可以添加选题并设定渠道、日期与状态\n支持按发布状态与渠道过滤列表\n统计本周待发布与已完成数量，空状态有明确引导",
  },
  {
    label: "本地收支台账",
    title: "独立开发者收支台账",
    goal: "记录订阅收入、一次性回款与服务器/模型支出，按月份汇总净收入并支持导出 CSV 对账。",
    acceptance:
      "可以录入收入与支出流水并按月份筛选\n自动计算总收入、总支出与净结余\n支持导出当前筛选流水为 CSV 文件",
  },
] as const;

function nextVersionTitle(title: string): string {
  const base = title.trim();
  if (!base) return "";
  const match = /^(.*)\s+v(\d+)$/i.exec(base);
  const candidate = match
    ? `${match[1]} v${Number(match[2]) + 1}`
    : `${base} v2`;
  return candidate.slice(0, 200);
}

export function IdeaCreate({
  onCancel,
  onCreated,
  active = true,
  onBusyChange,
  continueFrom,
  onOpenSettings,
}: {
  onCancel: () => void;
  onCreated: (mission: Mission) => void;
  active?: boolean;
  onBusyChange?: (busy: boolean) => void;
  continueFrom?: Mission;
  onOpenSettings?: () => void;
}) {
  const [title, setTitle] = useState(() =>
    continueFrom ? nextVersionTitle(continueFrom.title) : "",
  );
  const [goal, setGoal] = useState("");
  const [acceptance, setAcceptance] = useState(() =>
    continueFrom?.idea_project?.acceptance.join("\n") ?? "",
  );
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
            : continueFrom?.spec.profile_id &&
                options.some((p) => p.id === continueFrom.spec.profile_id)
              ? continueFrom.spec.profile_id
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
  }, [active, continueFrom?.spec.profile_id]);
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
      const payload = {
        title: title.trim(),
        goal: goal.trim(),
        acceptance: requirements,
        profile_id: profileID,
        max_calls: maxCalls,
      };
      const result = continueFrom
        ? await ideaApi.continueIdea(continueFrom.id, payload)
        : await ideaApi.create(payload);
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
        <div className="eyebrow">
          {continueFrom ? "基于已有版本继续迭代" : "从想法到可用应用"}
        </div>
        <h1>{continueFrom ? "下一轮想怎么改进？" : "你想做什么？"}</h1>
        <p className="muted">
          {continueFrom
            ? "将在已有候选代码基础上新建独立任务；原任务、基线与账本保持只读封存不变。"
            : "描述目标，Foundry 为你建项目、规划、编码和验证。完成后预览应用，下载完整源码或单文件 HTML。"}
        </p>
      </header>
      {continueFrom && (
        <div className="note" role="status">
          <strong>基线来源：{continueFrom.title}</strong>
          <p className="muted small">
            任务 ID <code>{continueFrom.id}</code> · 继承候选摘要{" "}
            <code>{continueFrom.candidate_digest.slice(0, 12)}…</code>
          </p>
        </div>
      )}
      {!continueFrom && (
        <div className="idea-templates" aria-label="常见场景快速填充">
          <span className="muted small">快速填入参考模板：</span>
          <div className="template-pills">
            {starterTemplates.map((tpl) => (
              <button
                key={tpl.label}
                type="button"
                disabled={busy}
                onClick={() => {
                  setTitle(tpl.title);
                  setGoal(tpl.goal);
                  setAcceptance(tpl.acceptance);
                }}
              >
                {tpl.label}
              </button>
            ))}
          </div>
        </div>
      )}
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
        {maxCalls > 0 && maxCalls < 6 && (
          <p className="field-help warning-copy">
            提示：完整流程（调研、规划、编码、验证与独立审查）通常需要 6～12
            次调用；上限过小可能在中途耗尽。
          </p>
        )}
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
          <div className="note">
            <p>先在设置中添加模型账户与模型配置，回来后这里会保留已填写的想法。</p>
            {onOpenSettings && (
              <button type="button" onClick={onOpenSettings}>
                前往配置模型
              </button>
            )}
          </div>
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
            {busy
              ? "正在创建项目…"
              : continueFrom
                ? "创建改进任务"
                : "创建应用任务"}
          </button>
          <button type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
        </div>
      </form>
    </section>
  );
}
