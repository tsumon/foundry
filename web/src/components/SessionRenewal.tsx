import { useEffect, useRef, useState } from "react";
import type { ModelAccount } from "../types";
import {
  sessionRenewalApi,
  type RenewalStatus,
  type RenewalAction,
} from "../sessionRenewalApi";

export function SessionRenewal({ account }: { account: ModelAccount }) {
  const [status, setStatus] = useState<RenewalStatus>();
  const [message, setMessage] = useState("");
  const [unknown, setUnknown] = useState(false);
  const [busy, setBusy] = useState(false);
  const epoch = useRef(0),
    lock = useRef(false);
  const accept = (next: RenewalStatus) => {
    if (
      !next ||
      next.account_id !== account.id ||
      next.account_revision !== account.revision ||
      typeof next.enabled !== "boolean" ||
      !Number.isSafeInteger(next.revision) ||
      ![
        "disabled",
        "enabled",
        "blocked",
        "in_progress",
        "recovery_required",
        "login_required",
      ].includes(next.state)
    )
      throw Error("invalid renewal metadata");
    setStatus(next);
    setUnknown(false);
  };
  useEffect(() => {
    const current = ++epoch.current;
    setStatus(undefined);
    setMessage("");
    setUnknown(false);
    setBusy(false);
    lock.current = false;
    void sessionRenewalApi
      .read(account.id)
      .then((next) => {
        if (current === epoch.current) accept(next);
      })
      .catch(() => {
        if (current === epoch.current)
          setMessage("续期状态不可读取；操作保持关闭。");
      });
    return () => {
      epoch.current++;
    };
  }, [account.id, account.revision]);
  const run = async (action?: RenewalAction) => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    const current = epoch.current;
    try {
      const next = action
        ? await sessionRenewalApi.mutate(account.id, action)
        : await sessionRenewalApi.read(account.id);
      if (current === epoch.current) accept(next);
    } catch {
      if (current === epoch.current) {
        if (action) setUnknown(true);
        setMessage(
          action
            ? "操作结果未知或已被拒绝；请显式读取状态，不会自动重试。"
            : "续期状态不可读取；操作保持关闭。",
        );
      }
    } finally {
      if (current === epoch.current) {
        lock.current = false;
        setBusy(false);
      }
    }
  };
  const material = status?.material;
  const materialCurrent =
    material &&
    material.account_id === account.id &&
    material.account_revision === account.revision &&
    material.revision === status?.revision &&
    Date.parse(material.expires_at) > Date.now();
  const blocked = busy || unknown || !status;
  return (
    <section className="note" aria-label="会话续期">
      <h4>保持登录：独立会话续期</h4>
      <p>
        只维护当前订阅注册的凭据，不批准模型调用、目录、任务继续、预算、计划、沙盒或付费
        API 回退。安装更新不会开启续期。
      </p>
      {message && <p role="alert">{message}</p>}
      {status && (
        <>
          <p>
            {status.enabled ? "续期已开启" : "续期已关闭"} ·{" "}
            {status.registration_retained ? "注册已保留" : "注册尚不可用"} ·{" "}
            {status.access_state === "usable"
              ? "访问凭据可用"
              : status.access_state === "expired"
                ? "访问凭据已过期"
                : "访问凭据不可用"}
          </p>
          {status.state === "in_progress" && (
            <p>
              已有请求正在处理；关闭续期会阻止后续派发。另一进程已发出的请求可能持续至有界超时，结果需读取确认。
            </p>
          )}
          {status.state === "recovery_required" && (
            <p>续期需要显式恢复或重新登录；不会重放旧令牌。</p>
          )}
          {status.state === "login_required" && (
            <p>注册需要重新登录；不会自动打开浏览器。</p>
          )}
          {status.state === "blocked" && (
            <p>账户或授权已变化，续期已阻止；需要新的审批材料。</p>
          )}
          {status.due_at && (
            <p>最早计划时间：{status.due_at}（仅桌面运行期间每 30 秒检查）</p>
          )}
        </>
      )}
      <button disabled={busy} onClick={() => void run()}>
        读取续期状态
      </button>{" "}
      <button
        disabled={blocked || status?.enabled || status?.state === "in_progress"}
        onClick={() =>
          void run({
            action: "prepare",
            expected_account_revision: account.revision,
          })
        }
      >
        准备续期材料
      </button>
      {material && (
        <details open>
          <summary>独立续期审批材料</summary>
          <p>{material.approval_scope}</p>
          <p>
            注册：{material.binding.id} · 客户端：{material.binding.client_id}
          </p>
          <p>
            颁发者：{material.binding.issuer} · 资源：{material.resource}
          </p>
          <p>令牌端点：{material.token_endpoint}</p>
          <p>原授权范围：{material.binding.scopes.join(", ")}</p>
          <p>{material.credential_store}</p>
          <p>{material.schedule}</p>
          <p>{material.failure_behavior}</p>
          <p>材料有效期：{material.expires_at}</p>
        </details>
      )}
      <button
        disabled={
          blocked ||
          !materialCurrent ||
          status?.enabled ||
          status?.state === "in_progress"
        }
        onClick={() =>
          material &&
          void run({
            action: "approve",
            material_id: material.id,
            expected_revision: status!.revision,
          })
        }
      >
        批准并开启续期
      </button>{" "}
      <button
        disabled={blocked || !status?.enabled}
        onClick={() =>
          status &&
          void run({ action: "disable", expected_revision: status.revision })
        }
      >
        关闭续期
      </button>
    </section>
  );
}
