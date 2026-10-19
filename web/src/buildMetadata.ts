const unavailable = "不可用";

export interface BuildMetadata {
  version: string;
  timestamp: string;
}

export function normalizeBuildMetadata(
  version: string | undefined,
  timestamp: string | undefined,
): BuildMetadata {
  return {
    version: version?.trim() || unavailable,
    timestamp: timestamp?.trim() || unavailable,
  };
}

export const buildMetadata = normalizeBuildMetadata(
  typeof __APP_VERSION__ === "string" ? __APP_VERSION__ : undefined,
  typeof __BUILD_TIMESTAMP__ === "string" ? __BUILD_TIMESTAMP__ : undefined,
);
