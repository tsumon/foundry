import { useEffect, useState } from "react";
import { api } from "../api";

export function RecoveryNotice({ enabled }: { enabled: boolean }) {
  const [readOnly, setReadOnly] = useState(false);
  const [reason, setReason] = useState<string>();
  useEffect(() => {
    if (!enabled) return;
    let disposed = false;
    void api
      .health()
      .then((health) => {
        if (!disposed) {
          setReason(health.recovery_error);
          setReadOnly(
            health.read_only === true || health.status === "recovery_limited",
          );
        }
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
      恢复受限：只读诊断。
      {recoveryDescription(reason)}历史仍可查看；
      {reason === "c4_runtime_mismatch" &&
        "原基线、证据和已用预算保留，但旧审批不授权新沙盒；不会自动更换绑定。"}
      所有业务变更、模型和工具调用已禁止，自动恢复未运行。恢复条件满足前不能继续任务。
    </div>
  );
}

// Only allowlisted descriptions enter the UI; never render raw diagnostic
// strings, which may contain paths or other private details from future servers.
function recoveryDescription(reason?: string): string {
  switch (reason) {
    case "invalid_mission_model_snapshot":
      return "任务模型快照损坏或版本不支持，";
    case "c4_runtime_mismatch":
      return "当前安装的内置沙盒与任务冻结的版本或摘要不一致，";
    case "c4_unavailable":
      return "C4 配置、控制记录或任务绑定不可用，";
    case "coding_workbench_unavailable":
      return "当前安装的编码工作台运行环境不可用，";
    default:
      return "恢复条件未满足或诊断类型不受支持，";
  }
}
