import { useEffect, useState } from "react";
import { APIError } from "../api";
import {
  accountError,
  credentialIntents,
  modelAccountApi,
  type CredentialIntent,
} from "../modelAccountApi";
import type { ModelAccount, ModelCredential } from "../types";
import { useCapabilityRequest } from "./capabilities/useCapabilityRequest";

const states = {
  pending: "写入待确认",
  available: "已存储（非推理验证）",
  failed: "写入失败",
  interrupted: "写入已中断",
  revoked: "已撤销",
};
export function ModelAccountCredentials({
  account,
  credentials,
  refresh,
}: {
  account: ModelAccount;
  credentials: ModelCredential[];
  refresh: () => Promise<void>;
}) {
  const [key, setKey] = useState("");
  const [intent, setIntent] = useState(() => credentialIntents.get(account.id));
  const [notice, setNotice] = useState("");
  const { busy, error, setError, run } = useCapabilityRequest(true);
  useEffect(() => {
    const pending = credentials.find((v) => v.state === "pending");
    if (pending && !credentialIntents.has(account.id)) {
      const next = {
        operation_id: pending.operation_id,
        kind: "publish" as const,
      };
      credentialIntents.set(account.id, next);
      setIntent(next);
    }
  }, [account.id, credentials]);
  const remember = (next: CredentialIntent) => {
    credentialIntents.set(account.id, next);
    setIntent(next);
  };
  const settle = (original: CredentialIntent) => {
    // The module map outlives this component. A remount may settle A and start
    // B before A's old POST/GET finishes. Only the exact captured local intent
    // may be removed, including distinct revoke attempts for the same version.
    if (credentialIntents.get(account.id) === original)
      credentialIntents.delete(account.id);
  };
  const mutate = (version?: ModelCredential) => {
    if (credentialIntents.has(account.id) || (!version && !key)) return;
    void run(async (valid) => {
      const next = {
        operation_id: version?.operation_id ?? crypto.randomUUID(),
        kind: version ? ("revoke" as const) : ("publish" as const),
        ...(version ? { version: version.version } : {}),
      };
      remember(next);
      // The request owns the transient key only until fetch settles. Clear the
      // editable copy immediately, including uncertain and rejected responses.
      const submitted = key;
      setKey("");
      setNotice("");
      try {
        const { operation } = version
          ? await modelAccountApi.revoke(
              account.id,
              version.version,
              account.revision,
            )
          : await modelAccountApi.publish(account.id, {
              expected_revision: account.revision,
              operation_id: next.operation_id,
              key: submitted,
            });
        if (operation.state !== "pending") settle(next);
        if (valid()) {
          setIntent(credentialIntents.get(account.id));
          setNotice("操作结果已记录；凭据存储不表示账户已验证或可推理。");
          await refresh();
        }
      } catch (cause) {
        // A definite rejected client request cannot have accepted a mutation.
        // In-progress conflicts and server failures may have durable intent.
        if (
          cause instanceof APIError &&
          cause.status < 500 &&
          cause.code !== "model_credential_in_progress"
        )
          settle(next);
        if (valid()) {
          setIntent(credentialIntents.get(account.id));
          setError(accountError(cause));
          if (cause instanceof APIError && cause.status === 409)
            await refresh();
        }
      }
    });
  };
  const reconcile = () =>
    void run(async (valid) => {
      // A previous mount may have definitively settled its map entry while
      // this mount still displays that original ID. Keep the explicit GET
      // usable; never turn this retained nonsecret identity into a new POST.
      const original = credentialIntents.get(account.id) ?? intent;
      if (!original) return;
      try {
        const { operation } = await modelAccountApi.operation(
          account.id,
          original.operation_id,
        );
        // An available receipt does not settle an uncertain revoke: only revoked
        // proves it finished. Continue GET-only rather than replaying the POST.
        const settled =
          original.kind === "revoke"
            ? operation.state === "revoked"
            : operation.state !== "pending";
        if (settled) settle(original);
        if (valid()) {
          setIntent(credentialIntents.get(account.id));
          setNotice(
            settled
              ? "已查询原操作记录，没有重新提交凭据。"
              : "原操作仍未确认；仅查询，没有重新提交。",
          );
          await refresh();
        }
      } catch (cause) {
        if (valid()) setError(accountError(cause));
      }
    });
  return (
    <section
      className="account-credentials"
      aria-label={`${account.name} 凭据版本`}
    >
      <h3>追加式凭据版本</h3>
      <p className="muted">
        凭据仅短暂用于本次写入；不显示尾号，不存浏览器。真实账户调用尚未开放。
      </p>
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {intent && (
        <div className="note warning-note">
          <p>
            原操作 <code>{intent.operation_id}</code>{" "}
            结果未确认。新增与撤销保持锁定，离开设置不会生成新操作。
          </p>
          <button type="button" disabled={busy} onClick={reconcile}>
            仅查询原操作结果
          </button>
        </div>
      )}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          mutate();
        }}
      >
        <label htmlFor="account-secret">凭据</label>
        <input
          id="account-secret"
          type="password"
          autoComplete="new-password"
          spellCheck={false}
          value={key}
          disabled={busy || !!intent}
          onChange={(e) => setKey(e.target.value)}
        />
        <div className="actions">
          <button type="submit" disabled={busy || !!intent || !key}>
            保存新版本
          </button>
          <button type="button" disabled={busy} onClick={() => setKey("")}>
            取消凭据输入
          </button>
        </div>
      </form>
      <p className="warning-copy">
        撤销会阻止仍使用该版本的旧任务后续调用；历史证据和版本记录保留。不会自动切换新版本。
      </p>
      {!credentials.length && <p>尚无凭据版本记录</p>}
      <ul className="account-version-list">
        {credentials.map((v) => (
          <li key={v.version}>
            <strong>
              版本 {v.version} · {states[v.state] ?? "未知状态"}
            </strong>
            <span>{v.cleanup_pending ? "清理待处理；不可使用" : ""}</span>
            <code>操作 {v.operation_id}</code>
            <button
              type="button"
              aria-label={`撤销 ${account.name} 版本 ${v.version}`}
              disabled={busy || !!intent || v.state !== "available"}
              onClick={() => mutate(v)}
            >
              撤销版本 {v.version}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
