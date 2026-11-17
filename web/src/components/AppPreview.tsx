import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { ideaApi } from "../ideaApi";
import type { Mission } from "../types";

export function AppPreview({
  mission,
  disabled = false,
}: {
  mission: Mission;
  disabled?: boolean;
}) {
  const initial = mission.state === "draft" && !mission.changes?.length;
  const key = `${mission.id}:${mission.candidate_digest}:${mission.revision}`;
  const current = useRef(key);
  current.current = key;
  const [preview, setPreview] = useState<{ key: string; digest: string }>();
  const [viewport, setViewport] = useState<"desktop" | "tablet" | "mobile">(
    "desktop",
  );
  const [tall, setTall] = useState(false);
  const [frameRevision, setFrameRevision] = useState(0);
  const [checkedMap, setCheckedMap] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  useEffect(() => {
    setError("");
    setBusy(false);
    return () => controller.current?.abort();
  }, [key]);
  async function open() {
    if (busy || disabled) return;
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    setBusy(true);
    setError("");
    try {
      const result = await ideaApi.preview(mission.id, abort.signal);
      if (current.current !== key || abort.signal.aborted) return;
      if (!result.html || result.candidate_digest !== mission.candidate_digest)
        throw new Error("候选已改变，请读取当前任务后重新预览");
      setPreview({ key, digest: result.candidate_digest });
    } catch (cause) {
      if (!abort.signal.aborted && current.current === key)
        setError(errorMessage(cause));
    } finally {
      if (current.current === key) setBusy(false);
    }
  }
  const qaReview = mission.reviews
    ?.filter(
      (r) =>
        r.role === "qa" && r.candidate_digest === mission.candidate_digest,
    )
    .at(-1);
  const requirements = mission.idea_project?.acceptance ?? [];
  return (
    <section className="app-preview" aria-label="应用预览">
      <div className="section-heading">
        <h3>{initial ? "初始模板（尚未实现需求）" : "看一下应用"}</h3>
        <button onClick={() => void open()} disabled={busy || disabled}>
          {busy ? "正在读取候选…" : initial ? "预览初始模板" : "预览当前应用"}
        </button>
      </div>
      <p className="muted small">
        预览与工作台隔离，不联网；本地存储使用预览内存会话，关闭或重置后清空。这里不启动服务器，预览不替代结构测试或交付审批。
      </p>
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      {preview?.key === key && (
        <>
          <div className="preview-toolbar" aria-label="预览视口控制">
            <div className="preview-viewports" role="group" aria-label="屏幕尺寸">
              {(
                [
                  ["desktop", "桌面 100%"],
                  ["tablet", "平板 768px"],
                  ["mobile", "手机 390px"],
                ] as const
              ).map(([mode, label]) => (
                <button
                  key={mode}
                  type="button"
                  aria-pressed={viewport === mode}
                  className={viewport === mode ? "selected" : ""}
                  onClick={() => setViewport(mode)}
                >
                  {label}
                </button>
              ))}
            </div>
            <div className="preview-actions">
              <button type="button" onClick={() => setTall((v) => !v)}>
                {tall ? "标准高度" : "展开高度"}
              </button>
              <button
                type="button"
                onClick={() => setFrameRevision((n) => n + 1)}
              >
                重置应用状态
              </button>
              <button type="button" onClick={() => setPreview(undefined)}>
                关闭预览
              </button>
            </div>
          </div>
          <div
            className={
              requirements.length > 0 && !initial
                ? "preview-workbench preview-with-checklist"
                : "preview-workbench"
            }
          >
            <div className={`preview-frame-shell viewport-${viewport}`}>
              <iframe
                key={`${key}:${frameRevision}`}
                title="应用隔离预览"
                className={tall ? "preview-tall" : ""}
                sandbox="allow-scripts"
                referrerPolicy="no-referrer"
                src={`/api/missions/${encodeURIComponent(mission.id)}/app-preview/frame?digest=${encodeURIComponent(preview.digest)}`}
              />
            </div>
            {requirements.length > 0 && !initial && (
              <aside
                className="preview-checklist"
                aria-label="对照需求逐条验收"
              >
                <h4>对照需求验收清单</h4>
                <p className="muted small">
                  仅作本人预览核对参考；正式批准请在右侧决策栏完成。
                </p>
                <ul>
                  {requirements.map((item, idx) => {
                    const checkKey = `${key}:${idx}`;
                    const qaCheck = qaReview?.checks?.find(
                      (c) => c.id === `requirement:${idx}`,
                    );
                    return (
                      <li key={idx}>
                        <label className="check-label">
                          <input
                            type="checkbox"
                            checked={!!checkedMap[checkKey]}
                            onChange={(e) =>
                              setCheckedMap((old) => ({
                                ...old,
                                [checkKey]: e.target.checked,
                              }))
                            }
                          />
                          <span>{item}</span>
                        </label>
                        {qaCheck && (
                          <p className="muted small">
                            QA：
                            {qaCheck.passed ? "通过" : "未通过"} ·{" "}
                            {qaCheck.summary}
                          </p>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </aside>
            )}
          </div>
        </>
      )}
    </section>
  );
}
