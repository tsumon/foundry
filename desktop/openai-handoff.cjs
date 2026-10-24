"use strict";
// No global localhost allowance: the owning backend must confirm its live,
// waiting session immediately before the main process launches the URL once.
function createOpenAIHandoff({ platform, mode, isCurrent, verify, open }) {
  const launched = new Set();
  let pending = false;
  return async (raw) => {
    if (
      platform !== "darwin" ||
      mode !== "coding" ||
      !isCurrent() ||
      pending ||
      launched.has(raw)
    )
      return false;
    const match = /^http:\/\/127\.0\.0\.1:([1-9][0-9]{0,4})\/auth\/start$/.exec(
      raw,
    );
    if (!match || Number(match[1]) > 65535) return false;
    pending = true;
    try {
      if (!(await verify(raw)) || !isCurrent()) return false;
      launched.add(raw); // Even an uncertain OS launch never retries.
      await open(raw);
      return true;
    } catch {
      return false;
    } finally {
      pending = false;
    }
  };
}
module.exports = { createOpenAIHandoff };
