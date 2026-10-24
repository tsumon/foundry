import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { desktopSubscriptionApi } from "../desktopSubscriptionApi";
import { modelContinuationApi } from "../modelContinuationApi";
import {
  continuationState,
  effectiveModelSnapshot,
  invalidContinuation,
} from "../modelContinuation";
import { invalidModelSnapshot } from "../modelSnapshot";
import { roles, roleNames, type Mission } from "../types";

interface Choice {
  authorization: string;
  catalog: string;
  label: string;
  expires: string;
}
export function ModelContinuation({
  mission,
  onChanged,
  disabled = false,
}: {
  mission: Mission;
  onChanged: (mission: Mission) => void;
  disabled?: boolean;
}) {
  const [choices, setChoices] = useState<Choice[]>([]);
  const [selected, setSelected] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [clock, tick] = useState(0);
  const locked = useRef(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const history = Array.isArray(mission.model_continuations)
    ? mission.model_continuations
    : [];
  // Local time updates only. Expiry never starts a request or renews authority.
  useEffect(() => {
    const expiry = [
      ...history.flatMap((c) => [c?.expires_at, c?.authorization_expires_at]),
      ...choices.map((c) => c.expires),
    ]
      .map((v) => Date.parse(v))
      .filter((v) => v > Date.now());
    if (!expiry.length) return;
    const timer = window.setTimeout(
      () => tick((n) => n + 1),
      Math.min(Math.min(...expiry) - Date.now() + 1, 2147483647),
    );
    return () => window.clearTimeout(timer);
  }, [history, choices, clock]);
  const invalid = invalidContinuation(mission) || invalidModelSnapshot(mission);
  const eligible =
    mission.coding_version === 1 &&
    !mission.tool_mode &&
    ["blocked", "paused"].includes(mission.state) &&
    !mission.plan &&
    !mission.changes?.length &&
    !mission.coding_attempts?.length &&
    !mission.approvals?.some((a) => a.kind === "execute");
  const blocked = disabled || busy || invalid || !eligible || !!error;
  const pending = history.some((c) =>
    ["prepared", "approved"].includes(continuationState(c, mission.revision)),
  );
  const effective = effectiveModelSnapshot(mission);
  const selectedChoice = choices[Number(selected)];
  async function perform(action: () => Promise<void>) {
    if (locked.current || disabled) return;
    locked.current = true;
    setBusy(true);
    try {
      await action();
    } catch (e) {
      if (alive.current) setError(errorMessage(e));
    } finally {
      locked.current = false;
      if (alive.current) setBusy(false);
    }
  }
  async function readChoices() {
    const selections = roles
      .map((role) => effective?.roles[role])
      .filter(
        (s) => s?.authorization && s.route?.adapter === "openai_siwc_responses",
      );
    const accountIDs = [
      ...new Set(selections.map((s) => s!.profile.account_id!)),
    ];
    const [grants, catalogs] = await Promise.all([
      modelContinuationApi.candidates(mission.id),
      Promise.all(
        accountIDs.map(async (id) => ({
          id,
          data: await desktopSubscriptionApi.read(id),
        })),
      ),
    ]);
    const next: Choice[] = [];
    for (const { id, data } of catalogs) {
      const affected = selections.filter((s) => s!.profile.account_id === id);
      for (const grant of grants.authorizations ?? []) {
        if (
          grant.account_id !== id ||
          grant.state !== "available" ||
          !(Date.parse(grant.expires_at) > Date.now()) ||
          affected.some((s) => s!.authorization!.id === grant.id)
        )
          continue;
        const catalogChoices = data.catalog_choices ?? [];
        for (const operation of new Set(
          catalogChoices.map((c) => c.operation_id),
        )) {
          // One approved receipt may cover different models for different roles.
          // Every affected frozen selection must be represented in that receipt.
          const matches = affected.map((s) =>
            catalogChoices.find(
              (c) =>
                c.operation_id === operation &&
                c.model === s!.profile.model &&
                c.route_id === s!.profile.route_id &&
                Date.parse(c.expires_at) > Date.now(),
            ),
          );
          if (matches.some((c) => !c)) continue;
          const expires = new Date(
            Math.min(
              Date.parse(grant.expires_at),
              ...matches.map((c) => Date.parse(c!.expires_at)),
            ),
          ).toISOString();
          next.push({
            authorization: grant.id,
            catalog: operation,
            expires,
            label: `${affected[0]!.account?.name ?? "本地账户"} · ${[...new Set(matches.map((c) => c!.model))].join(" / ")} · 本次登录授权代次 ${grant.generation} · 到期 ${expires}`,
          });
        }
      }
    }
    if (alive.current) {
      setChoices(next);
      setSelected("");
      setLoaded(true);
    }
  }
  return (
    <section className="model-continuation" aria-label="任务授权续接">
      <h3>任务授权续接</h3>
      <p>
        续接不调用模型，只更新本任务的有效授权选择；准备 → 批准 →
        应用分别操作。应用不批准上下文、执行、VM 就绪或基线，也不重试旧调用。
      </p>
      <p className="muted small">
        本地授权 ID 是每次登录的授权绑定（per-sign-in local
        authorization），不是注册客户端
        ID。原始模型快照与历史审批保留；后续调用必须取得绑定有效选择的新审批。
      </p>
      <p>
        已用 {mission.budget.used_calls} / {mission.budget.max_calls} · 剩余{" "}
        {Math.max(0, mission.budget.max_calls - mission.budget.used_calls)}。
        {mission.budget.usage_unknown
          ? "未知用量保持未知；续接不返还预算。"
          : "续接不改变调用预算。"}
      </p>
      {invalid && (
        <p className="warning-copy">
          续接历史或模型材料损坏／不受支持；所有续接与调用保持禁用。
        </p>
      )}
      {!eligible && (
        <p className="warning-copy">
          仅支持尚无批准执行计划、无候选变更的 blocked / paused
          编码任务；活动操作与调用由服务端再次检查。
        </p>
      )}
      {error && (
        <p role="alert">
          {error}。操作已停止，不自动重试；请重新查看任务的持久状态后再作决定。
        </p>
      )}
      <button disabled={blocked} onClick={() => void perform(readChoices)}>
        读取本地续接候选
      </button>
      {loaded &&
        (!choices.length ? (
          <p className="warning-copy">
            缺少可用的本地授权或目录证据。请通过独立账户流程取得证据；这里不会登录、刷新授权、准备桌面访问或查询提供方目录。
          </p>
        ) : (
          <label>
            本地续接候选
            <select
              aria-label="本地续接候选"
              value={selected}
              disabled={blocked || pending}
              onChange={(e) => setSelected(e.target.value)}
            >
              <option value="">
                请选择已有记录（服务端复核相同身份与目录）
              </option>
              {choices.map((c, i) => (
                <option key={`${c.authorization}:${c.catalog}`} value={i}>
                  {c.label}
                </option>
              ))}
            </select>
          </label>
        ))}
      <button
        disabled={
          blocked ||
          pending ||
          selected === "" ||
          !selectedChoice ||
          !(Date.parse(selectedChoice.expires) > Date.now())
        }
        onClick={() =>
          void perform(async () => {
            const result = await modelContinuationApi.prepare(
              mission.id,
              mission.revision,
              selectedChoice.authorization,
              selectedChoice.catalog,
            );
            if (alive.current) onChanged(result.mission);
          })
        }
      >
        准备续接材料
      </button>
      {history.map((c, i) => {
        const state = continuationState(c, mission.revision);
        if (state === "invalid")
          return (
            <article className="continuation-material" key={`invalid-${i}`}>
              <strong>材料损坏／不受支持</strong>
              <p>无法安全解释此材料，原始模型历史仍可查看。</p>
              <div className="button-row">
                <button disabled>批准续接材料</button>
                <button disabled>应用续接</button>
              </div>
            </article>
          );
        const label =
          (
            {
              prepared: "待批准",
              approved: "已批准，待应用",
              applied: "已应用",
              expired: "已过期",
              stale: "已失效（任务版本变化）",
              invalid: "材料损坏／不受支持",
            } as Record<string, string>
          )[state] ?? "材料不受支持";
        return (
          <article className="continuation-material" key={c?.id ?? i}>
            <strong>{label}</strong>
            <p>
              角色：
              {Array.isArray(c?.affected_roles)
                ? c.affected_roles.map((r) => roleNames[r] ?? "未知").join("、")
                : "未知"}{" "}
              · 准备于 r{c?.prepared_revision} · 材料到期{" "}
              {c?.expires_at || "未知"}
            </p>
            <p>
              本次登录本地授权 <code>{c?.authorization_id}</code> · 代次{" "}
              {c?.authorization_generation} · 授权到期{" "}
              {c?.authorization_expires_at}
            </p>
            <details>
              <summary>查看确切审批材料</summary>
              <p>
                材料 <code>{c?.id}</code> · <code>{c?.digest}</code>
              </p>
              <p>
                原有效快照 <code>{c?.predecessor_digest}</code>
              </p>
              <p>
                拟替换快照 <code>{c?.replacement_digest}</code>
              </p>
              <p>
                已记录目录 <code>{c?.catalog_operation_id}</code>
              </p>
            </details>
            <div className="button-row">
              <button
                disabled={blocked || state !== "prepared"}
                onClick={() =>
                  void perform(async () => {
                    const result = await modelContinuationApi.approve(
                      mission.id,
                      c.id,
                      mission.revision,
                      c.digest,
                    );
                    if (alive.current) onChanged(result.mission);
                  })
                }
              >
                批准续接材料
              </button>
              <button
                disabled={blocked || state !== "approved"}
                onClick={() =>
                  void perform(async () => {
                    const result = await modelContinuationApi.apply(
                      mission.id,
                      c.id,
                      mission.revision,
                      c.digest,
                    );
                    if (alive.current) onChanged(result.mission);
                  })
                }
              >
                应用续接
              </button>
            </div>
          </article>
        );
      })}
    </section>
  );
}
