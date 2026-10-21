import { request } from "./api";
import type {
  InspectionRequest,
  RepositoryInspection,
  RepositoryImportOperation,
  RecoveryDiagnosticDTO,
} from "./repositoryImportTypes";
export const repositoryImportApi = {
  inspect: (input: InspectionRequest) =>
    request<RepositoryInspection>("/api/repository-imports/inspections", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  inspections: () =>
    request<RepositoryInspection[]>("/api/repository-imports/inspections"),
  operations: () =>
    request<RepositoryImportOperation[]>("/api/repository-imports/operations"),
  diagnose: (id: string) =>
    request<{ diagnostic: RecoveryDiagnosticDTO }>(
      `/api/missions/${encodeURIComponent(id)}/recovery-diagnostic`,
    ),
};
