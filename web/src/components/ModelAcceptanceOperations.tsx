import { useEffect, useMemo, useRef, useState } from "react";
import {
  modelAcceptanceApi,
  modelAcceptanceError,
  type ModelAcceptanceError,
} from "../modelAcceptanceApi";
import type {
  ModelAccount,
  ModelAcceptanceHistory,
  ModelAcceptanceOperation,
  ModelAcceptancePhase,
} from "../types";

const phaseLabels: Record<ModelAcceptancePhase, string> = {
  connection: "连接",
  catalog: "目录",
  inference: "推理",
};
const stateLabels: Record<ModelAcceptanceOperation["state"], string> = {
  pending: "待审批",
  approved: "已批准，尚未执行",
  running: "已消耗，执行中",
  succeeded: "已成功",
  failed: "已失败",
  unknown: "结果未知",
  rejected: "已拒绝",
  expired: "已过期",
  invalidated: "已失效",
};
const phases: ModelAcceptancePhase[] = ["connection", "catalog", "inference"];
const receiptCodes = new Set([
  "auth_invalid",
  "entitlement_denied",
  "quota_exhausted",
  "rate_limited",
  "request_rejected",
  "server_error",
  "transport_unknown",
  "response_incomplete",
]);
const identifier = /^[A-Za-z0-9_-]{1,256}$/;
const digest = /^[a-f0-9]{64}$/;

function validOperation(
  value: ModelAcceptanceOperation,
  accountID: string,
): boolean {
  return (
    !!value &&
    identifier.test(value.id) &&
    value.account_id === accountID &&
    phases.includes(value.phase) &&
    Object.hasOwn(stateLabels, value.state) &&
    Number.isSafeInteger(value.revision) &&
    value.revision > 0 &&
    Number.isSafeInteger(value.claim_sequence) &&
    value.claim_sequence >= 0 &&
    value.source === "local_simulation" &&
    value.live_access === "blocked" &&
    value.quota === "unknown" &&
    value.remote_billing === "unverified" &&
    !!value.material &&
    value.material.phase === value.phase &&
    value.material.account.id === accountID &&
    digest.test(value.material_digest) &&
    (value.code === undefined || receiptCodes.has(value.code)) &&
    Array.isArray(value.catalog) &&
    value.catalog.every(
      (entry) =>
        identifier.test(entry.id) &&
        typeof entry.display_name === "string" &&
        entry.display_name.length <= 256,
    )
  );
}

function validHistory(
  value: ModelAcceptanceHistory,
  accountID: string,
): boolean {
  return (
    !!value &&
    typeof value.simulation_enabled === "boolean" &&
    Array.isArray(value.operations) &&
    value.operations.every((operation) =>
      validOperation(operation, accountID),
    ) &&
    Array.isArray(value.routes) &&
    value.routes.every(
      (route) =>
        route.environment === "simulation" &&
        identifier.test(route.id) &&
        typeof route.adapter === "string" &&
        typeof route.spend_class === "string" &&
        typeof route.base_url === "string",
    )
  );
}

function latestClaimed(
  operations: ModelAcceptanceOperation[],
  phase: ModelAcceptancePhase,
  predicate: (operation: ModelAcceptanceOperation) => boolean = () => true,
) {
  return operations
    .filter(
      (operation) =>
        operation.phase === phase &&
        operation.claim_sequence > 0 &&
        predicate(operation),
    )
    .reduce<ModelAcceptanceOperation | undefined>(
      (latest, operation) =>
        !latest || operation.claim_sequence > latest.claim_sequence
          ? operation
          : latest,
      undefined,
    );
}

export function ModelAcceptanceOperations({
  account,
  refreshAccount,
}: {
  account: ModelAccount;
  refreshAccount: () => Promise<void>;
}) {
  return (
    <AcceptanceOperations
      key={`${account.id}:${account.revision}`}
      account={account}
      refreshAccount={refreshAccount}
    />
  );
}

function AcceptanceOperations({
  account,
  refreshAccount,
}: {
  account: ModelAccount;
  refreshAccount: () => Promise<void>;
}) {
  const [snapshot, setSnapshot] = useState<ModelAcceptanceHistory>();
  const [loaded, setLoaded] = useState(false);
  const [reading, setReading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ModelAcceptanceError>();
  const [routeID, setRouteID] = useState("");
  const [model, setModel] = useState("");
  const [selected, setSelected] = useState<
    Partial<Record<ModelAcceptancePhase, string>>
  >({});
  const [unresolved, setUnresolved] = useState<{
    operationID?: string;
  }>();
  const lifecycle = useRef({ active: true, epoch: 0, locked: false });

  const clearSnapshot = () => {
    setSnapshot(undefined);
    setSelected({});
    setRouteID("");
    setModel("");
  };
  const acceptHistory = (history: ModelAcceptanceHistory) => {
    if (!validHistory(history, account.id)) throw new Error("invalid receipt");
    setSnapshot(history);
    setLoaded(true);
    setRouteID((current) =>
      history.routes.some((route) => route.id === current)
        ? current
        : (history.routes[0]?.id ?? ""),
    );
  };
  const readHistory = async () => {
    const epoch = lifecycle.current.epoch;
    setReading(true);
    setError(undefined);
    clearSnapshot();
    try {
      const history = await modelAcceptanceApi.history(account.id);
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      acceptHistory(history);
    } catch (cause) {
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      const decoded =
        cause instanceof Error && cause.message === "invalid receipt"
          ? { ...modelAcceptanceError(cause), code: "invalid_receipt" }
          : modelAcceptanceError(cause);
      setError(decoded);
      setLoaded(false);
    } finally {
      if (lifecycle.current.active && lifecycle.current.epoch === epoch)
        setReading(false);
    }
  };

  useEffect(() => {
    lifecycle.current.active = true;
    lifecycle.current.epoch++;
    lifecycle.current.locked = false;
    void readHistory();
    return () => {
      lifecycle.current.active = false;
      lifecycle.current.epoch++;
      lifecycle.current.locked = false;
    };
  }, [account.id, account.revision]);

  const operations = snapshot?.operations ?? [];
  // Connection authority is dispatch order, never response/list/proposal/time order.
  const latestConnection = useMemo(
    () => latestClaimed(operations, "connection"),
    [operations],
  );
  const latestResultAccountRevision = latestConnection?.result_account_revision;
  const connection =
    latestConnection?.state === "succeeded" &&
    typeof latestResultAccountRevision === "number" &&
    Number.isSafeInteger(latestResultAccountRevision) &&
    latestResultAccountRevision >= account.revision
      ? latestConnection
      : undefined;
  const catalog = useMemo(
    () =>
      connection
        ? latestClaimed(
            operations,
            "catalog",
            (operation) =>
              operation.state === "succeeded" &&
              operation.material.connection_id === connection.id,
          )
        : undefined,
    [operations, connection?.id],
  );
  useEffect(() => {
    const models = catalog?.catalog ?? [];
    setModel((current) =>
      models.some((entry) => entry.id === current)
        ? current
        : (models[0]?.id ?? ""),
    );
  }, [catalog?.id, catalog?.revision]);

  const replaceOperation = (operation: ModelAcceptanceOperation) => {
    if (!validOperation(operation, account.id))
      throw new Error("invalid receipt");
    setSnapshot((current) =>
      current
        ? {
            ...current,
            operations: [
              operation,
              ...current.operations.filter((item) => item.id !== operation.id),
            ],
          }
        : current,
    );
    setSelected((current) => ({ ...current, [operation.phase]: operation.id }));
  };
  const runMutation = async (
    operationID: string | undefined,
    work: () => Promise<{ operation: ModelAcceptanceOperation }>,
    after?: (operation: ModelAcceptanceOperation) => Promise<void>,
  ) => {
    if (lifecycle.current.locked || unresolved) return;
    lifecycle.current.locked = true;
    const epoch = lifecycle.current.epoch;
    setBusy(true);
    setError(undefined);
    try {
      const result = await work();
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      replaceOperation(result.operation);
      if (after) {
        try {
          await after(result.operation);
        } catch {
          if (lifecycle.current.active && lifecycle.current.epoch === epoch)
            setError({
              code: "account_refresh_failed",
              message:
                "连接回执已保存，但账户详情刷新失败；验收回执不会被清除。请显式重新打开或读取账户。",
              requiresRead: false,
            });
        }
      }
    } catch (cause) {
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      const decoded =
        cause instanceof Error && cause.message === "invalid receipt"
          ? { ...modelAcceptanceError(cause, true), code: "invalid_receipt" }
          : modelAcceptanceError(cause, true);
      setError(decoded);
      if (decoded.requiresRead) setUnresolved({ operationID });
    } finally {
      lifecycle.current.locked = false;
      if (lifecycle.current.active && lifecycle.current.epoch === epoch)
        setBusy(false);
    }
  };

  const reconcile = async () => {
    if (!unresolved || lifecycle.current.locked) return;
    lifecycle.current.locked = true;
    const epoch = lifecycle.current.epoch;
    setBusy(true);
    setError(undefined);
    clearSnapshot();
    setLoaded(false);
    try {
      let recovered: ModelAcceptanceOperation | undefined;
      if (unresolved.operationID) {
        const result = await modelAcceptanceApi.operation(
          account.id,
          unresolved.operationID,
        );
        if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
          return;
        if (!validOperation(result.operation, account.id))
          throw new Error("invalid receipt");
        recovered = result.operation;
      }
      const history = await modelAcceptanceApi.history(account.id);
      if (!lifecycle.current.active || lifecycle.current.epoch !== epoch)
        return;
      if (!validHistory(history, account.id))
        throw new Error("invalid receipt");
      const historyReceipt = recovered
        ? history.operations.find((item) => item.id === recovered.id)
        : undefined;
      const recoveredReceipt =
        historyReceipt &&
        recovered &&
        historyReceipt.revision >= recovered.revision
          ? historyReceipt
          : recovered;
      acceptHistory(
        recoveredReceipt
          ? {
              ...history,
              operations: [
                recoveredReceipt,
                ...history.operations.filter(
                  (item) => item.id !== recoveredReceipt.id,
                ),
              ],
            }
          : history,
      );
      if (recoveredReceipt)
        setSelected((current) => ({
          ...current,
          [recoveredReceipt.phase]: recoveredReceipt.id,
        }));
      setUnresolved(undefined);
    } catch (cause) {
      if (lifecycle.current.active && lifecycle.current.epoch === epoch) {
        clearSnapshot();
        setLoaded(false);
        setError(
          cause instanceof Error && cause.message === "invalid receipt"
            ? { ...modelAcceptanceError(cause), code: "invalid_receipt" }
            : modelAcceptanceError(cause),
        );
      }
    } finally {
      lifecycle.current.locked = false;
      if (lifecycle.current.active && lifecycle.current.epoch === epoch)
        setBusy(false);
    }
  };

  const mutationDisabled =
    busy || reading || !!unresolved || !loaded || !snapshot?.simulation_enabled;
  const currentAccountRevision = Math.max(
    account.revision,
    connection?.result_account_revision ?? account.revision,
  );
  const propose = (phase: ModelAcceptancePhase) => {
    const prerequisite =
      phase === "catalog"
        ? connection?.id
        : phase === "inference"
          ? catalog?.id
          : undefined;
    void runMutation(undefined, () =>
      modelAcceptanceApi.propose(account.id, {
        phase,
        route_id: routeID,
        expected_account_revision: currentAccountRevision,
        ...(prerequisite ? { prerequisite_id: prerequisite } : {}),
        ...(phase === "inference" ? { model } : {}),
      }),
    );
  };
  const chosen = (phase: ModelAcceptancePhase) =>
    operations.find((operation) => operation.id === selected[phase]);

  return (
    <section className="acceptance-operations" aria-label="单次本地模拟验收">
      <div className="section-heading">
        <div>
          <h4>单次本地模拟验收</h4>
          <p className="muted">连接、目录与推理分别审阅、批准和执行。</p>
        </div>
        <span className="route-lock">
          {snapshot?.simulation_enabled ? "仅本地模拟" : "生产真实访问保持关闭"}
        </span>
      </div>
      <p className="field-help">
        只使用本进程拥有的合成
        fixture。不会登录真实账户、读取真实凭据、自动重试、轮询、刷新、付费降级、运行脚本或写入
        Git。
      </p>
      {error && (
        <p className="form-error" role="alert">
          {error.message} 错误码：<code>{error.code}</code>
        </p>
      )}
      {unresolved && (
        <div className="note warning-note">
          <p>
            原写操作的结果未确认。不会重新提交；读取成功前所有写操作保持锁定。
          </p>
          <button
            type="button"
            disabled={busy}
            onClick={() => void reconcile()}
          >
            仅读取原验收记录
          </button>
        </div>
      )}
      <div className="acceptance-toolbar">
        <label htmlFor={`acceptance-route-${account.id}`}>本地模拟路由</label>
        <select
          id={`acceptance-route-${account.id}`}
          value={routeID}
          disabled={mutationDisabled || !snapshot?.routes.length}
          onChange={(event) => setRouteID(event.target.value)}
        >
          {(snapshot?.routes ?? []).map((route) => (
            <option key={route.id} value={route.id}>
              {route.id}
            </option>
          ))}
        </select>
        {snapshot?.routes.find((route) => route.id === routeID) && (
          <p className="field-help acceptance-route-summary">
            <code>{routeID}</code> ·{" "}
            {snapshot.routes.find((route) => route.id === routeID)!.adapter} ·{" "}
            {snapshot.routes.find((route) => route.id === routeID)!.spend_class}
          </p>
        )}
        <button
          type="button"
          disabled={reading || busy || snapshot?.simulation_enabled === false}
          onClick={() => void readHistory()}
        >
          重新读取验收记录
        </button>
      </div>
      {reading && <p role="status">正在读取验收记录…</p>}
      {!reading && loaded && operations.length === 0 && (
        <p>尚无已保存的验收材料</p>
      )}
      <ol className="acceptance-steps">
        {phases.map((phase) => {
          const phaseOperations = operations.filter(
            (operation) => operation.phase === phase,
          );
          const operation = chosen(phase);
          const prerequisiteMissing =
            (phase === "catalog" && !connection) ||
            (phase === "inference" && (!catalog || !model));
          return (
            <li key={phase}>
              <div className="acceptance-step-heading">
                <strong>{phaseLabels[phase]}</strong>
                <button
                  type="button"
                  disabled={
                    mutationDisabled ||
                    !routeID ||
                    prerequisiteMissing ||
                    !account.enabled
                  }
                  onClick={() => propose(phase)}
                >
                  创建{phaseLabels[phase]}材料
                </button>
              </div>
              {phase === "catalog" && latestConnection && !connection && (
                <p className="warning-copy">
                  最新连接回执为{stateLabels[latestConnection.state]}
                  ；较早成功不能授权目录请求。
                </p>
              )}
              {phase === "inference" && (
                <label>
                  推理模型
                  <select
                    aria-label="推理模型"
                    value={model}
                    disabled={mutationDisabled || !catalog}
                    onChange={(event) => setModel(event.target.value)}
                  >
                    {(catalog?.catalog ?? []).map((entry) => (
                      <option key={entry.id} value={entry.id}>
                        {entry.display_name} · {entry.id}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              <label>
                {phaseLabels[phase]}已保存材料
                <select
                  aria-label={`${phaseLabels[phase]}已保存材料`}
                  value={selected[phase] ?? ""}
                  onChange={(event) =>
                    setSelected((current) => ({
                      ...current,
                      [phase]: event.target.value,
                    }))
                  }
                >
                  <option value="">选择材料查看与操作</option>
                  {phaseOperations.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.id} · {stateLabels[item.state]}
                    </option>
                  ))}
                </select>
              </label>
              {operation && (
                <OperationReview
                  operation={operation}
                  disabled={mutationDisabled}
                  decide={(decision) =>
                    void runMutation(operation.id, () =>
                      modelAcceptanceApi.decide(
                        account.id,
                        operation,
                        decision,
                      ),
                    )
                  }
                  execute={() =>
                    void runMutation(
                      operation.id,
                      () => modelAcceptanceApi.execute(account.id, operation),
                      async (result) => {
                        if (
                          result.phase === "connection" &&
                          result.state === "succeeded"
                        )
                          await refreshAccount();
                      },
                    )
                  }
                />
              )}
            </li>
          );
        })}
      </ol>
      {operations.length > 0 && (
        <div className="acceptance-receipts" aria-label="本地模拟回执">
          <h5>已保存回执</h5>
          <ul>
            {operations.map((operation) => (
              <li key={operation.id}>
                <button
                  type="button"
                  className="acceptance-receipt"
                  onClick={() =>
                    setSelected((current) => ({
                      ...current,
                      [operation.phase]: operation.id,
                    }))
                  }
                >
                  <span>
                    {phaseLabels[operation.phase]} ·{" "}
                    {stateLabels[operation.state]} · 本地模拟
                  </span>
                  <code>{operation.id}</code>
                  {operation.code && <code>{operation.code}</code>}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

function OperationReview({
  operation,
  disabled,
  decide,
  execute,
}: {
  operation: ModelAcceptanceOperation;
  disabled: boolean;
  decide: (decision: "approve" | "reject") => void;
  execute: () => void;
}) {
  const material = operation.material;
  return (
    <div
      className="acceptance-review"
      aria-label={`${phaseLabels[operation.phase]}材料审阅`}
    >
      <p>
        <strong>{operation.id}</strong> · r{operation.revision} ·{" "}
        {stateLabels[operation.state]}
      </p>
      <dl>
        <dt>固定端点</dt>
        <dd>{material.route.base_url}</dd>
        <dt>账户快照</dt>
        <dd>
          {material.account.id} · r{material.account.revision}
        </dd>
        <dt>路由快照</dt>
        <dd>{material.route.id}</dd>
        <dt>凭据快照</dt>
        <dd>
          版本 {material.credential_version}
          {material.authorization && (
            <>
              {" "}
              · 授权 {material.authorization.binding.id} · generation{" "}
              {material.authorization.generation}
            </>
          )}
        </dd>
        <dt>计费边界</dt>
        <dd>
          {material.account.billing_mode} · {material.route.spend_class}
        </dd>
        <dt>输入摘要</dt>
        <dd>{material.data_summary}</dd>
        {material.model && (
          <>
            <dt>精确模型</dt>
            <dd>{material.model}</dd>
          </>
        )}
        {material.prompt_digest && (
          <>
            <dt>输入摘要哈希</dt>
            <dd>
              <code>{material.prompt_digest}</code>
            </dd>
          </>
        )}
        <dt>期限</dt>
        <dd>
          {material.deadline_seconds} 秒 · 到期 {material.expires_at}
        </dd>
        <dt>尝试预算</dt>
        <dd>
          最多 {material.max_attempts} 次 · 已记录 {operation.attempts} 次
        </dd>
        <dt>材料摘要</dt>
        <dd>
          <code>{operation.material_digest}</code>
        </dd>
      </dl>
      {operation.state === "pending" && (
        <div className="actions acceptance-actions">
          <button
            type="button"
            disabled={disabled}
            onClick={() => decide("approve")}
          >
            批准{phaseLabels[operation.phase]}材料
          </button>
          <button
            type="button"
            disabled={disabled}
            onClick={() => decide("reject")}
          >
            拒绝{phaseLabels[operation.phase]}材料
          </button>
        </div>
      )}
      {operation.state === "approved" && (
        <button type="button" disabled={disabled} onClick={execute}>
          执行{phaseLabels[operation.phase]}
        </button>
      )}
      {(operation.state === "running" || operation.attempts > 0) && (
        <p className="warning-copy">
          单次尝试已消耗；没有重试操作。结果未知也不会重放请求。
        </p>
      )}
      <p className="field-help">
        真实访问：关闭 · 远端计费：未验证 · 额度：未知
      </p>
    </div>
  );
}
