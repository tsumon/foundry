import { request } from "./api";
import {
  parseRepositoryInspection,
  inspectionSpecMatches,
} from "./repositoryImportValidation";
import type {
  InspectionRequest,
  RepositoryInspection,
  RepositoryImportOperation,
  RecoveryDiagnosticDTO,
} from "./repositoryImportTypes";
export const repositoryImportApi = {
  inspect: async (input: InspectionRequest) => {
    const value = await request<unknown>(
      "/api/repository-imports/inspections",
      { method: "POST", body: JSON.stringify(input) },
    );
    const receipt = await parseRepositoryInspection(value);
    if (
      !receipt ||
      !inspectionSpecMatches(receipt.spec, input.spec) ||
      (input.repository_id !== undefined &&
        (receipt.repository_id !== input.repository_id ||
          receipt.repository_revision !== input.repository_revision))
    )
      throw new Error("检查回执不完整、摘要不匹配或格式不受支持；不能创建任务");
    return receipt;
  },
  inspections: async () => {
    const values = await request<unknown>(
      "/api/repository-imports/inspections",
    );
    if (!Array.isArray(values) || values.length > 100)
      throw new Error("检查历史格式不受支持");
    const receipts = await Promise.all(values.map(parseRepositoryInspection));
    if (receipts.some((r) => !r)) throw new Error("检查历史存在无效回执");
    return receipts as RepositoryInspection[];
  },
  operations: () =>
    request<RepositoryImportOperation[]>("/api/repository-imports/operations"),
  diagnose: (id: string) =>
    request<{ diagnostic: RecoveryDiagnosticDTO }>(
      `/api/missions/${encodeURIComponent(id)}/recovery-diagnostic`,
    ),
};
