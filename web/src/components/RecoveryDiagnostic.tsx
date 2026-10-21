import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { repositoryImportApi } from "../repositoryImportApi";
import type { RecoveryDiagnosticDTO } from "../repositoryImportTypes";
import type { Mission } from "../types";
const dimensions: Record<string, string> = {
  candidate_record: "候选选择",
  candidate: "候选完整性",
  base_authority: "冻结基线与授权",
  import_provenance: "导入来源",
  coding_trace: "执行轨迹",
  invocations: "模型调用",
  verification: "验证绑定",
  reviews: "审查绑定",
  export: "导出",
  local_operation: "本地操作",
};
const statuses: Record<string, string> = {
  verified: "已核对",
  blocked: "阻止",
  unknown: "未知",
  not_applicable: "不适用",
  unsupported: "不支持",
};
export function RecoveryDiagnostic({
  mission,
  onOpenDecision,
}: {
  mission: Mission;
  onOpenDecision?: () => void;
}) {
  const missionIdentity = `${mission.id}:${mission.revision}:${mission.epoch}`;
  const epoch = useRef({ identity: missionIdentity, value: 0 });
  if (epoch.current.identity !== missionIdentity)
    epoch.current = {
      identity: missionIdentity,
      value: epoch.current.value + 1,
    };
  const identity = `${missionIdentity}:${epoch.current.value}`;
  const current = useRef(identity);
  current.current = identity;
  const alive = useRef(true);
  const [pending, setPending] = useState("");
  const lock = useRef("");
  const [result, setResult] = useState<RecoveryDiagnosticDTO>();
  const [checkedIdentity, setCheckedIdentity] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const stale =
    result &&
    (checkedIdentity !== identity ||
      result.stale ||
      result.mission_id !== mission.id ||
      result.revision !== mission.revision ||
      result.epoch !== mission.epoch);
  const diagnose = async () => {
    if (lock.current === identity) return;
    lock.current = identity;
    setPending(identity);
    setError("");
    try {
      const { diagnostic } = await repositoryImportApi.diagnose(mission.id);
      if (alive.current && current.current === identity) {
        setResult(diagnostic);
        setCheckedIdentity(identity);
      }
    } catch (cause) {
      if (alive.current && current.current === identity)
        setError(errorMessage(cause));
    } finally {
      if (lock.current === identity) lock.current = "";
      if (alive.current && current.current === identity) setPending("");
    }
  };
  return (
    <div className="note" aria-label="恢复状态核对">
      <button
        type="button"
        disabled={pending === identity}
        onClick={() => void diagnose()}
      >
        {pending === identity ? "正在核对…" : "核对恢复状态"}
      </button>
      <p className="field-help">
        手动只读核对；不执行、不修复、不重试，不替代原审批。实时操作仍由服务端重新验证。
      </p>
      {error && <p role="alert">核对失败：{error}</p>}
      {!result && <p>恢复状态：未检查</p>}
      {result && (
        <>
          <p role="status">
            检查于 {result.checked_at} · 任务 r{result.revision} / epoch{" "}
            {result.epoch} ·{" "}
            {stale ? "已过期，请手动重新核对" : "只读事实，非操作许可"}
          </p>
          <ul>
            {result.checks.map((c) => (
              <li key={c.id}>
                <strong>
                  {dimensions[c.id] ?? c.id} · {statuses[c.status] ?? "未知"} (
                  {c.status})
                </strong>{" "}
                · {c.code}
                <p>{c.message}</p>
                {c.records?.map((r) => (
                  <code key={`${r.kind}:${r.id}`}>
                    {r.kind}: {r.id}{" "}
                  </code>
                ))}
              </li>
            ))}
          </ul>
          {!stale &&
            result.next_actions.some(
              (a) => a === "replan" || a === "delivery_decision",
            ) && (
              <p>
                <a href="#decision-drawer" onClick={onOpenDecision}>
                  返回原决策面板核对
                  {result.next_actions.includes("replan")
                    ? "重新规划"
                    : "交付决定"}
                </a>
                ；仍需原守卫与独立审批。
              </p>
            )}
          {!stale && result.next_actions.includes("new_task") && (
            <p>需要新的任务；请使用已有“新建任务”入口。此处不能修复原数据。</p>
          )}
        </>
      )}
    </div>
  );
}

export function RepositorySource({ mission }: { mission: Mission }) {
  const source =
    mission.import_version === 1 ? mission.repository_import : undefined;
  return (
    <section className="note" aria-label="任务导入来源">
      <h3>任务导入来源</h3>
      {!source ? (
        <p>
          {mission.import_version
            ? "导入绑定缺失或不支持；不可视为已核对"
            : "历史 / 专用任务：未绑定 C3 导入回执，不补填来源证明"}
        </p>
      ) : (
        <dl className="metadata compact-metadata">
          <dt>检查回执</dt>
          <dd>
            <code>{source.inspection_id}</code>
            <br />
            <code>{source.inspection_digest}</code>
          </dd>
          <dt>导入操作</dt>
          <dd>
            <code>{source.import_operation_id}</code>
          </dd>
          <dt>冻结来源</dt>
          <dd>
            <code>{source.root}</code>
            <br />
            <code>{source.commit}</code>
          </dd>
          <dt>基线摘要</dt>
          <dd>
            <code>{source.base_digest}</code>
          </dd>
          <dt>导入规格摘要</dt>
          <dd>
            <code>{source.import_spec_digest}</code>
          </dd>
        </dl>
      )}
    </section>
  );
}
