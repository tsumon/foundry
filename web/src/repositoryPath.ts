// UI admission only; the native controller independently validates all paths.
export function supportedRepositoryPath(value: string): boolean {
  if (value.startsWith("/") && !value.startsWith("//")) return true;
  if (!/^[A-Za-z]:[\\/]/.test(value)) return false;
  return value.slice(3).split(/[\\/]/).every((part) =>
    !!part && part !== "." && part !== ".." && !/[. ]$/.test(part) &&
    !/[:*?"<>|~\x00-\x1f]/.test(part) &&
    !/^(CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³])(?:\.|$)/i.test(part),
  );
}
