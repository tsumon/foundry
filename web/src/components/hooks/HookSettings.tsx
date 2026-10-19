import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { APIError } from "../../api";
import { hookApi } from "../../hookApi";
import type {
  HookMissionChoice,
  HookRule,
  HookRuleInput,
  HookSnapshot,
} from "../../hookTypes";
import { HookRecords } from "./HookRecords";

interface RuleDraft extends HookRuleInput {
  id: string;
}

const newDraft = (): RuleDraft => ({
  id: "",
  expected_revision: 0,
  name: "",
  enabled: false,
  event_kinds: [],
  mission_id: "",
  action: "record_activity",
});
const draftFromRule = (rule: HookRule): RuleDraft => ({
  id: rule.id,
  expected_revision: rule.revision,
  name: rule.name,
  enabled: rule.enabled,
  event_kinds: [...rule.event_kinds],
  mission_id: rule.mission_id,
  action: "record_activity",
});
const hookReadError =
  "事件钩子读取失败，请检查本地服务。保留上次数据；数据不可用不等于没有规则。";
const hookSaveError = (cause: unknown) => {
  if (cause instanceof APIError && cause.status === 409)
    return "版本冲突：草稿和原 expected revision 已保留，且未自动重送。请明确选择最新规则后再编辑。";
  if (
    cause instanceof APIError &&
    (cause.status === 401 || cause.status === 403)
  )
    return "会话不可用：草稿已保留，且未自动重试。";
  if (cause instanceof APIError && cause.status === 400)
    return "规则输入无效：草稿已保留，请检查名称、事件和任务范围。";
  return "保存未完成：草稿已保留，请检查本地服务；不会自动重试。";
};

export function HookSettings({
  enabled,
  visible,
  missions,
  onOpenMission,
}: {
  enabled: boolean;
  visible: boolean;
  missions: HookMissionChoice[];
  onOpenMission: (id: string) => void;
}) {
  const [snapshot, setSnapshot] = useState<HookSnapshot | null>(null);
  const [draft, setDraft] = useState<RuleDraft>(newDraft);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [readError, setReadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [notice, setNotice] = useState("");
  const [received, setReceived] = useState("");
  const lifecycle = useRef({
    mounted: false,
    writeEpoch: 0,
    active: false,
    epoch: 0,
    request: 0,
    mutation: 0,
    draftVersion: 0,
    busy: false,
    reading: false,
    attempted: false,
    refreshRequired: false,
    controller: null as AbortController | null,
  });

  useLayoutEffect(() => {
    const current = lifecycle.current;
    current.mounted = true;
    return () => {
      current.mounted = false;
      current.writeEpoch++;
      current.busy = false;
    };
  }, []);

  useLayoutEffect(() => {
    const current = lifecycle.current;
    current.active = enabled && visible;
    current.epoch++;
    current.reading = false;
    setLoading(false);
    if (!enabled) current.attempted = false;
    if (!current.active) {
      current.controller?.abort();
      current.controller = null;
    }
    return () => {
      current.controller?.abort();
      current.controller = null;
      current.active = false;
      current.epoch++;
      current.reading = false;
      current.attempted = false;
    };
  }, [enabled, visible]);

  const refresh = useCallback(
    async ({
      allowDuringSave = false,
      replaceCurrent = false,
    }: {
      allowDuringSave?: boolean;
      replaceCurrent?: boolean;
    } = {}) => {
      const current = lifecycle.current;
      if (!current.active) {
        if (replaceCurrent) current.refreshRequired = true;
        return;
      }
      if (current.busy && !allowDuringSave) return;
      if (current.reading) {
        if (!replaceCurrent) return;
        current.controller?.abort();
        current.controller = null;
        current.request++;
        current.reading = false;
      }
      const epoch = current.epoch;
      const request = ++current.request;
      const mutation = current.mutation;
      const controller = new AbortController();
      current.controller = controller;
      current.reading = true;
      current.refreshRequired = false;
      const valid = () =>
        current.active &&
        current.epoch === epoch &&
        current.request === request;
      setLoading(true);
      setReadError("");
      try {
        const next = await hookApi.snapshot(controller.signal);
        if (valid() && current.mutation === mutation) {
          setSnapshot(next);
          setReceived(new Date().toISOString());
        }
      } catch (cause) {
        if (
          valid() &&
          !(cause instanceof DOMException && cause.name === "AbortError")
        )
          setReadError(hookReadError);
      } finally {
        if (valid()) {
          setLoading(false);
          current.reading = false;
          current.controller = null;
        }
      }
    },
    [],
  );

  useEffect(() => {
    if (!enabled || !visible) return;
    if (lifecycle.current.refreshRequired) {
      void refresh({ allowDuringSave: true, replaceCurrent: true });
    } else if (!snapshot && !lifecycle.current.attempted) {
      lifecycle.current.attempted = true;
      void refresh();
    }
  }, [enabled, refresh, snapshot, visible]);

  const save = async () => {
    const current = lifecycle.current;
    if (!current.active || current.busy) return;
    const writeEpoch = current.writeEpoch;
    const draftVersion = current.draftVersion;
    current.busy = true;
    setBusy(true);
    setSaveError("");
    setNotice("");
    const input: HookRuleInput = {
      ...(draft.id ? { id: draft.id } : {}),
      expected_revision: draft.expected_revision,
      name: draft.name.trim(),
      enabled: draft.enabled,
      event_kinds: [...draft.event_kinds],
      mission_id: draft.mission_id,
      action: "record_activity",
    };
    const valid = () => current.mounted && current.writeEpoch === writeEpoch;
    try {
      const { rule } = await hookApi.saveRule(input);
      if (valid()) {
        current.mutation++;
        setSnapshot((previous) =>
          previous
            ? {
                ...previous,
                rules: [
                  ...previous.rules.filter((item) => item.id !== rule.id),
                  rule,
                ],
              }
            : previous,
        );
        if (current.draftVersion === draftVersion)
          setDraft(draftFromRule(rule));
        setNotice(`已保存规则 r${rule.revision}。观察规则不会执行动作。`);
      }
    } catch (cause) {
      if (valid()) {
        setSaveError(hookSaveError(cause));
        if (cause instanceof APIError && cause.status === 409) {
          current.refreshRequired = true;
          void refresh({ allowDuringSave: true, replaceCurrent: true });
        }
      }
    } finally {
      if (valid()) {
        current.busy = false;
        setBusy(false);
      }
    }
  };

  const unicodeLength = Array.from(draft.name.trim()).length;
  const validDraft =
    unicodeLength >= 1 &&
    unicodeLength <= 80 &&
    !/[\u0000-\u001f\u007f]/u.test(draft.name) &&
    draft.event_kinds.length > 0 &&
    (!draft.mission_id ||
      missions.some((mission) => mission.id === draft.mission_id));
  const locked =
    !enabled ||
    !visible ||
    busy ||
    lifecycle.current.busy ||
    (!snapshot && loading);
  const updateDraft = (update: (current: RuleDraft) => RuleDraft) => {
    lifecycle.current.draftVersion++;
    setDraft(update);
  };
  const replaceDraft = (next: RuleDraft) => {
    lifecycle.current.draftVersion++;
    setDraft(next);
  };
  const toggleKind = (kind: string) =>
    updateDraft((current) => ({
      ...current,
      event_kinds: current.event_kinds.includes(kind)
        ? current.event_kinds.filter((item) => item !== kind)
        : [...current.event_kinds, kind],
    }));

  return (
    <div className="page capability-page hook-settings">
      <header className="page-header">
        <p className="eyebrow">设置 / 观察层</p>
        <h1>事件钩子</h1>
        <p className="muted">
          只观察已提交事件并写入本地记录；不调用模型、工具、命令或网络，也不改变任务与审批。
        </p>
      </header>
      <div className="capability-read-state">
        <div>
          {loading && <span role="status">正在读取事件钩子…</span>}
          {received && (
            <span className="muted">
              最近完整读取：{received}
              {readError ? "（保留上次数据）" : ""}
            </span>
          )}
        </div>
        <button
          disabled={!enabled || !visible || loading || busy}
          onClick={() => void refresh()}
        >
          重新读取事件钩子
        </button>
      </div>
      {readError && (
        <p className="form-error" role="alert">
          {readError}
        </p>
      )}
      {!snapshot && !loading && !readError && (
        <p className="note">事件钩子尚未读取，不能据此认定没有规则。</p>
      )}
      {snapshot && (
        <>
          <div className="hook-runtime-summary" aria-label="事件钩子状态">
            <span>初始水位：{snapshot.processor.initialized_after_seq}</span>
            <span>当前水位：{snapshot.processor.cursor}</span>
            <span>最新事件：{snapshot.processor.latest_event_seq}</span>
            <span>待扫描事件：{snapshot.processor.pending_events}</span>
            <span>运行状态：{snapshot.runtime.state}</span>
            <span>
              最近处理：{snapshot.processor.last_processed_at || "尚无"}
            </span>
            <span>最近尝试：{snapshot.runtime.last_attempt_at || "尚无"}</span>
            <span>最近成功：{snapshot.runtime.last_success_at || "尚无"}</span>
            {snapshot.runtime.error_code && (
              <span>错误码：{snapshot.runtime.error_code}</span>
            )}
          </div>
          <div className="hook-settings-layout">
            <section className="connection-section">
              <div className="hook-section-heading">
                <div>
                  <h2>{draft.id ? "编辑观察规则" : "新增观察规则"}</h2>
                  <p className="muted">
                    冲突刷新不会替换这里的草稿；请从规则列表明确选择最新版本。
                  </p>
                </div>
                <button
                  disabled={locked}
                  onClick={() => {
                    replaceDraft(newDraft());
                    setSaveError("");
                    setNotice("");
                  }}
                >
                  新建规则
                </button>
              </div>
              <form
                className="connection-form hook-rule-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  if (!locked && validDraft) void save();
                }}
              >
                <label htmlFor="hook-rule-name">规则名称</label>
                <input
                  id="hook-rule-name"
                  value={draft.name}
                  disabled={locked}
                  onChange={(event) =>
                    updateDraft((current) => ({
                      ...current,
                      name: event.target.value,
                    }))
                  }
                />
                <p className="field-help">
                  1–80 个 Unicode 字符，不接受空名称或控制字符。
                </p>
                <fieldset disabled={locked} className="hook-kind-list">
                  <legend>观察事件</legend>
                  {snapshot.hook_points.map((point) => (
                    <label className="capability-check" key={point.kind}>
                      <input
                        type="checkbox"
                        aria-label={point.label}
                        checked={draft.event_kinds.includes(point.kind)}
                        onChange={() => toggleKind(point.kind)}
                      />
                      <span>
                        <strong>{point.label}</strong> <code>{point.kind}</code>
                        <small>{point.description}</small>
                      </span>
                    </label>
                  ))}
                </fieldset>
                <label htmlFor="hook-mission-scope">任务范围</label>
                <select
                  id="hook-mission-scope"
                  value={draft.mission_id}
                  disabled={locked}
                  onChange={(event) =>
                    updateDraft((current) => ({
                      ...current,
                      mission_id: event.target.value,
                    }))
                  }
                >
                  <option value="">全部任务</option>
                  {missions.map((mission) => (
                    <option key={mission.id} value={mission.id}>
                      {mission.title}
                    </option>
                  ))}
                </select>
                <label className="capability-check hook-enable">
                  <input
                    type="checkbox"
                    checked={draft.enabled}
                    disabled={locked}
                    onChange={(event) =>
                      updateDraft((current) => ({
                        ...current,
                        enabled: event.target.checked,
                      }))
                    }
                  />
                  <span>启用规则</span>
                </label>
                <div className="capability-observation">
                  <h3>固定处理器</h3>
                  <code>record_activity</code>
                  <p>只记录，无执行动作。事件发生不等于验证成功。</p>
                </div>
                {saveError && (
                  <p className="form-error" role="alert">
                    {saveError}
                  </p>
                )}
                {notice && (
                  <p className="notice" role="status">
                    {notice}
                  </p>
                )}
                <div className="actions">
                  <button
                    className="primary"
                    type="submit"
                    disabled={locked || !validDraft}
                  >
                    保存观察规则
                  </button>
                </div>
              </form>
            </section>
            <aside className="saved-connections hook-rule-list">
              <h2>已登记规则</h2>
              {!snapshot.rules.length && (
                <p className="note">已完成读取，目前没有规则。</p>
              )}
              {snapshot.rules.map((item) => (
                <article className="capability-record" key={item.id}>
                  <div className="capability-record-header">
                    <div>
                      <h3>{item.name}</h3>
                      <p>
                        r{item.revision} · {item.enabled ? "已启用" : "未启用"}{" "}
                        · 生效水位后 #{item.effective_after_seq}
                      </p>
                    </div>
                    <button
                      disabled={locked}
                      onClick={() => replaceDraft(draftFromRule(item))}
                    >
                      编辑规则 {item.name} r{item.revision}
                    </button>
                  </div>
                  <p>范围：{item.mission_id || "全部任务"}</p>
                  <p>事件：{item.event_kinds.join("、")}</p>
                  <p>动作：{item.action}</p>
                </article>
              ))}
            </aside>
          </div>
          <HookRecords
            missionID=""
            enabled={enabled}
            visible={visible}
            heading="最近观察记录"
            onOpenMission={onOpenMission}
            missionLabels={Object.fromEntries(
              missions.map((mission) => [mission.id, mission.title]),
            )}
          />
        </>
      )}
    </div>
  );
}
