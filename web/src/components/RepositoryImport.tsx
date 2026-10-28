import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { languageContracts } from "../codingV2";
import { repositoryImportApi } from "../repositoryImportApi";
import type {
  InspectionRequest,
  RepositoryInspection,
  RepositoryImportOperation,
} from "../repositoryImportTypes";

const selectable = (receipt: RepositoryInspection | null) =>
  receipt?.status === "ready" &&
  (receipt.schema_version === 1 ||
    (receipt.schema_version === 2 &&
      !!receipt.verification_v2 &&
      !!receipt.spec.verification_v2)) &&
  !!receipt.id &&
  !!receipt.digest;

export function RepositoryImport({
  request,
  disabled,
  onInspected,
}: {
  request: InspectionRequest;
  disabled: boolean;
  onInspected: (receipt: RepositoryInspection | null) => void;
}) {
  const [receipt, setReceipt] = useState<RepositoryInspection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const alive = useRef(true);
  const lock = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const inspect = async () => {
    if (lock.current || disabled) return;
    lock.current = true;
    setBusy(true);
    setError("");
    setReceipt(null);
    onInspected(null);
    try {
      const result = await repositoryImportApi.inspect({
        ...request,
        consent: true,
      });
      if (!alive.current) return;
      setReceipt(result);
      onInspected(selectable(result) ? result : null);
    } catch (cause) {
      if (alive.current) setError(errorMessage(cause));
    } finally {
      if (alive.current) {
        lock.current = false;
        setBusy(false);
      }
    }
  };
  return (
    <section className="note" aria-label="仓库导入检查">
      <h2>C3 · 仓库导入</h2>
      <p className="field-help">
        点击即同意一次只读的本地已提交文件检查；登记不等于许可。检查不调用模型、不授权上下文、执行或交付。
      </p>
      <button
        type="button"
        disabled={disabled || busy}
        onClick={() => void inspect()}
      >
        {busy ? "正在检查…" : "检查可导入性"}
      </button>
      <p role="status">
        {selectable(receipt)
          ? "可创建任务"
          : receipt?.status === "ready"
            ? "不支持的检查格式；不可创建任务"
            : receipt
              ? `不可导入 · ${receipt.stage} / ${receipt.code}`
              : "未检查"}
      </p>
      {error && <p role="alert">{error}</p>}
      {receipt && (
        <dl className="metadata compact-metadata">
          <dt>检查回执</dt>
          <dd>
            <code>{receipt.id}</code> · {receipt.checked_at}
          </dd>
          <dt>已提交来源</dt>
          <dd>
            <code>{receipt.root}</code>
            <br />
            <code>{receipt.commit || "未检查"}</code>
          </dd>
          <dt>快照 / 上下文</dt>
          <dd>
            {receipt.file_count ?? "未知"} 文件 /{" "}
            {receipt.total_bytes ?? "未知"} bytes ·{" "}
            {receipt.context_file_count ?? "未知"} 文件 /{" "}
            {receipt.context_bytes ?? "未知"} bytes
          </dd>
          {receipt.verification_v2 && (
            <>
              <dt>固定框架 / 选择的测试文件</dt>
              <dd>
                {
                  languageContracts[receipt.verification_v2.profile_id]
                    .framework
                }{" "}
                · {receipt.verification_v2.harness_version} ·{" "}
                {receipt.verification_v2.profile_id} ·{" "}
                {receipt.verification_v2.test_files.length} 个测试文件（未执行）
                <ul>
                  {receipt.verification_v2.test_files.map((p) => (
                    <li key={p}>
                      <code>{p}</code>
                    </li>
                  ))}
                </ul>
              </dd>
              <dt>源代码根 / 测试根</dt>
              <dd>
                {receipt.spec.verification_v2?.source_root} /{" "}
                {receipt.spec.verification_v2?.test_root}；不扩大允许修改范围
              </dd>
              <dt>保护目录前缀</dt>
              <dd>{receipt.verification_v2.protected_prefixes.join(" · ")}</dd>
              <dt>输入合约 / 保护输入摘要</dt>
              <dd>
                <code>{receipt.verification_v2.execution_contract_digest}</code>
                <br />
                <code>{receipt.verification_v2.protected_inputs_digest}</code>
              </dd>
              <dt>依赖准入 / 依赖包摘要</dt>
              <dd>
                <code>{receipt.verification_v2.admission_digest}</code>
                <br />
                <code>
                  {receipt.verification_v2.dependencies.bundle_digest}
                </code>
              </dd>
              <dt>依赖文件 / bytes</dt>
              <dd>
                {receipt.verification_v2.dependencies.file_count} /{" "}
                {receipt.verification_v2.dependencies.total_bytes}；非测试用例数
              </dd>
            </>
          )}
          <dt>回执摘要</dt>
          <dd>
            <code>{receipt.digest}</code>
          </dd>
          <dt>保护输入</dt>
          <dd>
            {receipt.protected_inputs?.join(" · ") || "无已列出的保护输入"}
          </dd>
        </dl>
      )}
      <p className="field-help">
        排除未提交及未跟踪文件。工作树状态：未检查（not_checked）。Docker / C4
        就绪：未检查（not_checked）。
      </p>
    </section>
  );
}

export function ImportHistory() {
  const [operations, setOperations] = useState<RepositoryImportOperation[]>([]);
  const [inspections, setInspections] = useState<RepositoryInspection[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    let alive = true;
    void Promise.all([
      repositoryImportApi.operations(),
      repositoryImportApi.inspections(),
    ])
      .then(([ops, rows]) => {
        if (alive) {
          setOperations(Array.isArray(ops) ? ops : []);
          setInspections(Array.isArray(rows) ? rows : []);
        }
      })
      .catch((cause) => {
        if (alive) setError(errorMessage(cause));
      });
    return () => {
      alive = false;
    };
  }, []);
  return (
    <details className="note">
      <summary>最近导入记录（只读）</summary>
      {error && <p role="status">历史读取失败：{error}；不产生导入资格。</p>}
      <p className="field-help">
        仅显示已存记录，不重读来源。预留任务 ID
        不表示任务已创建；不自动重试、恢复或清理。
      </p>
      {operations.map((o) => (
        <p key={o.id}>
          <code>{o.id}</code> · {o.status} / {o.stage} {o.code} · 预留任务{" "}
          <code>{o.mission_id}</code> ·{" "}
          {o.status === "committed" ? "已提交" : "未确认创建任务"}
        </p>
      ))}
      {inspections.map((i) => (
        <p key={i.id}>
          回执 <code>{i.id}</code> · {i.status} / {i.code} · {i.checked_at}
        </p>
      ))}
      {!error && !operations.length && !inspections.length && (
        <p>暂无已读取记录</p>
      )}
    </details>
  );
}
