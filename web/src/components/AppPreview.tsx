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
  return (
    <section className="app-preview" aria-label="应用预览">
      <div className="section-heading">
        <h3>{initial ? "初始模板（尚未实现需求）" : "看一下应用"}</h3>
        <button onClick={() => void open()} disabled={busy || disabled}>
          {busy ? "正在读取候选…" : initial ? "预览初始模板" : "预览当前应用"}
        </button>
      </div>
      <p className="muted small">
        预览与工作台隔离，不联网。这里不启动服务器；预览中的存储受浏览器沙盒限制。结构测试、业务验收和人工预览分别记录。
      </p>
      {error && <p role="alert">{error}</p>}
      {preview?.key === key && (
        <>
          <iframe
            title="应用隔离预览"
            sandbox="allow-scripts"
            referrerPolicy="no-referrer"
            src={`/api/missions/${encodeURIComponent(mission.id)}/app-preview/frame?digest=${encodeURIComponent(preview.digest)}`}
          />
          <button onClick={() => setPreview(undefined)}>关闭预览</button>
        </>
      )}
    </section>
  );
}
