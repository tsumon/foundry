import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import {
  desktopSubscriptionApi,
  type DesktopPreparation,
} from "../desktopSubscriptionApi";
import type { ModelAccount, ModelRoute, Profile } from "../types";

const blockers: Record<string, string> = {
  diagnostic_failed:
    "本机检查失败或清理未确认；准备已停止。保留私有日志，仅可进行精确项恢复，不会自动重试。",
  readiness_expired: "连接准备已过期；需要显式重新准备，并重新审批材料。",
  authorization_expired:
    "授权已过期；目录与新任务选择关闭，不会刷新或重新登录。",
  authorization_unavailable: "缺少可用授权；先完成独立连接审批。",
  prerequisite_unavailable: "缺少当前成功连接证据；目录仍关闭。",
  admission_unavailable: "本进程准入不可用；操作关闭。",
};
export function DesktopSubscription({
  account,
  routes,
  onPrepared,
  onProfileSaved,
}: {
  account: ModelAccount;
  routes: ModelRoute[];
  onPrepared: () => void;
  onProfileSaved: (p: Profile) => void;
}) {
  const [snapshot, setSnapshot] = useState<DesktopPreparation>();
  const [route, setRoute] = useState("");
  const [choice, setChoice] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const [message, setMessage] = useState("");
  const lock = useRef(false),
    epoch = useRef(0);
  const eligible = routes.filter(
    (r) =>
      r.account_id === account.id &&
      r.environment === "live" &&
      r.adapter === "openai_siwc_responses" &&
      r.base_url === "https://api.openai.com/v1" &&
      r.extra_usage === "off",
  );
  const accept = (next: DesktopPreparation) => {
    if (
      !next?.preparation ||
      !["unprepared", "connection", "catalog"].includes(
        next.preparation.phase,
      ) ||
      typeof next.preparation.connection_ready !== "boolean" ||
      typeof next.preparation.catalog_ready !== "boolean" ||
      !Array.isArray(next.catalog_choices)
    )
      throw Error("invalid metadata");
    setSnapshot(next);
    setChoice("");
    setUnknown(false);
  };
  useEffect(() => {
    const current = ++epoch.current;
    setSnapshot(undefined);
    setRoute("");
    setChoice("");
    setName("");
    setMessage("");
    setUnknown(false);
    void desktopSubscriptionApi
      .read(account.id)
      .then((next) => {
        if (epoch.current === current) accept(next);
      })
      .catch(() => {
        if (epoch.current === current)
          setMessage("准备状态未能读取；不会推断为已连接。");
      });
    return () => {
      epoch.current++;
    };
  }, [account.id, account.revision]);
  const run = async (action: () => Promise<void>, mutation = false) => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    try {
      await action();
    } catch {
      if (mutation) {
        setUnknown(true);
        setMessage("操作结果未知或已被拒绝；请显式读取原记录，不会自动重试。");
      } else setMessage("本地证据暂时不可读取；操作保持关闭。");
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  const read = () =>
    run(async () => {
      const current = epoch.current;
      const next = await desktopSubscriptionApi.read(account.id);
      if (current === epoch.current) accept(next);
    });
  const prepare = (phase: "connection" | "catalog") =>
    run(async () => {
      const current = epoch.current;
      const next = await desktopSubscriptionApi.prepare(
        account.id,
        phase,
        route,
        account.revision,
      );
      if (current !== epoch.current) return;
      accept(next);
      onPrepared();
    }, true);
  const choices = (snapshot?.catalog_choices ?? []).filter(
    (c) =>
      eligible.some((r) => r.id === c.route_id) &&
      /^[A-Za-z0-9_-]{1,256}$/.test(c.operation_id) &&
      typeof c.model === "string" &&
      c.model.length > 0 &&
      c.model.length <= 256 &&
      typeof c.display_name === "string" &&
      Date.parse(c.expires_at) > Date.now(),
  );
  const selected = choices.find(
    (c) => `${c.operation_id}:${c.model}` === choice,
  );
  const state = snapshot?.preparation;
  const stopped =
    state?.blocker === "diagnostic_failed" ||
    state?.blocker === "admission_unavailable";
  return (
    <section className="note" aria-label="Mac 订阅账户准备">
      <h4>Mac 订阅账户准备</h4>
      <p>
        准备连接会执行 Foundry 专属合成凭据的 Keychain
        写入、读取和精确删除检查；只有确认清理成功才获得十分钟本进程准备权限。不会登录、打开浏览器或自动批准材料。
      </p>
      <p>
        连接、目录与未来任务选择互相独立；准备目录不查询模型。无刷新、重试或付费
        API 回退。
      </p>
      {message && <p role="alert">{message}</p>}
      {state && (
        <p>
          {state.blocker
            ? (blockers[state.blocker] ??
              "准备不可用；状态未知，操作保持关闭。")
            : state.connection_ready
              ? "连接准备就绪；下一步生成并独立审批连接材料。"
              : state.catalog_ready
                ? "目录准备就绪；下一步生成并独立审批目录材料。"
                : "尚未准备；真实登录与模型查询均未开始。"}
        </p>
      )}
      <label>
        订阅准备路由
        <select
          value={route}
          disabled={busy || unknown}
          onChange={(e) => setRoute(e.target.value)}
        >
          <option value="">请选择官方订阅路由</option>
          {eligible.map((r) => (
            <option key={r.id} value={r.id}>
              {r.id} · subscription_only
            </option>
          ))}
        </select>
      </label>
      {!eligible.length && (
        <p>请先在模型路由区域为本账户创建官方 SIWC 路由；这只保存元数据。</p>
      )}
      <div className="actions">
        <button
          disabled={
            busy ||
            unknown ||
            !snapshot ||
            !eligible.some((r) => r.id === route) ||
            stopped
          }
          onClick={() => void prepare("connection")}
        >
          准备连接（本机检查）
        </button>
        <button
          disabled={
            busy ||
            unknown ||
            !snapshot ||
            !eligible.some((r) => r.id === route) ||
            stopped
          }
          onClick={() => void prepare("catalog")}
        >
          独立准备目录
        </button>
        <button disabled={busy} onClick={() => void read()}>
          读取准备与本地目录证据
        </button>
      </div>
      <h5>未来任务模型选择</h5>
      {!choices.length && (
        <p>
          当前没有可用于新任务的目录证据，或授权已过期。读取只检查本地记录，不访问提供方。
        </p>
      )}
      <label>
        目录模型选择
        <select
          value={choice}
          disabled={busy || unknown}
          onChange={(e) => setChoice(e.target.value)}
        >
          <option value="">请选择已记录模型</option>
          {choices.map((c) => (
            <option
              key={`${c.operation_id}:${c.model}`}
              value={`${c.operation_id}:${c.model}`}
            >
              {c.display_name} · {c.model}
            </option>
          ))}
        </select>
      </label>
      <label>
        新订阅连接名称
        <input
          value={name}
          maxLength={200}
          disabled={busy || unknown}
          onChange={(e) => setName(e.target.value)}
        />
      </label>
      <button
        disabled={busy || unknown || !selected || !name.trim()}
        onClick={() =>
          void run(async () => {
            if (!selected || Date.parse(selected.expires_at) <= Date.now())
              return;
            const current = epoch.current;
            const result = await api.saveProfile({
              name: name.trim(),
              kind: "direct",
              protocol: "responses",
              base_url: account.base_url,
              account_id: account.id,
              route_id: selected.route_id,
              model: selected.model,
              catalog_operation_id: selected.operation_id,
            });
            if (current !== epoch.current) return;
            onProfileSaved(result.profile);
            setMessage(
              "模型连接已保存；请在角色模型分配中显式选择。仅影响新任务，目录记录不授权推理。",
            );
          }, true)
        }
      >
        保存为未来任务的模型连接
      </button>
      <p>
        保存后请显式分配角色；已有任务快照不变。任务仍需要独立的预算、计划、沙盒与执行批准。
      </p>
    </section>
  );
}
