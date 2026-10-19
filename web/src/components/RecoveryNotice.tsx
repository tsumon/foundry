import { useEffect, useState } from "react";
import { api } from "../api";

export function RecoveryNotice({ enabled }: { enabled: boolean }) {
  const [readOnly, setReadOnly] = useState(false);
  useEffect(() => {
    if (!enabled) return;
    let disposed = false;
    void api
      .health()
      .then((health) => {
        if (!disposed)
          setReadOnly(
            health.read_only === true || health.status === "recovery_limited",
          );
      })
      .catch(() => {
        // A failed refresh must not clear an already known read-only warning.
        // The server remains the authority for every mutation.
      });
    return () => {
      disposed = true;
    };
  }, [enabled]);
  if (!readOnly) return null;
  return (
    <div className="global-error" role="alert" aria-label="恢复受限：只读诊断">
      恢复受限：只读诊断。任务模型快照损坏或版本不支持，历史仍可查看；
      所有业务变更、模型和工具调用已禁止，自动恢复未运行。数据修复前不能继续任务。
    </div>
  );
}
