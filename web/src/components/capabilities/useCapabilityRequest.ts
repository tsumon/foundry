import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { APIError } from "../../api";

// Backend UTC RFC3339Nano timestamps have variable fractional precision.
// Pad it before comparison, retaining nanoseconds that Date.parse would discard.
export function compareCapabilityTime(left: string, right: string): number {
  const normalized = (value: string) => {
    const match =
      /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
    return match ? `${match[1]}.${(match[2] ?? "").padEnd(9, "0")}Z` : "";
  };
  return normalized(left).localeCompare(normalized(right));
}

// Never reflect network/system/remote messages, which may contain secrets.
export function capabilityError(error: unknown): string {
  if (error instanceof APIError) {
    if (error.status === 409)
      return "版本或活动操作冲突：草稿已保留。重新读取后，请明确选择当前版本再编辑；不会自动覆盖。";
    if (error.status === 401 || error.status === 403)
      return "会话不可用，请重新连接本地工作台。草稿已保留。";
    if (error.status === 400 || error.status === 413)
      return "输入不符合支持的格式或大小限制。草稿已保留，请检查内容。";
    if (error.status === 404)
      return "未找到该记录。请重新读取；不会自动重发操作。";
  }
  return "请求未完成，请检查本地服务。草稿已保留；不会自动重试操作。";
}

// One panel's immediate lock and lifecycle epoch; no retries or background work.
export function useCapabilityRequest(enabled: boolean) {
  const state = useRef({ active: false, epoch: 0, busy: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // Match the committed controls' enabled state before they can be interacted
  // with; a passive effect leaves an enabled DOM / inactive guard race window.
  useLayoutEffect(() => {
    const current = state.current;
    current.active = enabled;
    current.epoch++;
    current.busy = false;
    setBusy(false);
    return () => {
      current.active = false;
      current.epoch++;
    };
  }, [enabled]);
  const run = useCallback(
    async (work: (valid: () => boolean) => Promise<void>) => {
      const current = state.current;
      if (!current.active || current.busy) return;
      const epoch = current.epoch;
      const valid = () => current.active && current.epoch === epoch;
      current.busy = true;
      setBusy(true);
      setError("");
      try {
        await work(valid);
      } catch (e) {
        if (valid()) setError(capabilityError(e));
      } finally {
        if (valid()) {
          current.busy = false;
          setBusy(false);
        }
      }
    },
    [],
  );
  return {
    busy,
    error,
    setError,
    run,
    locked: () => !state.current.active || state.current.busy,
  };
}
