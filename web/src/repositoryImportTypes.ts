import type { FileMeta, GenerationRef } from "./types";
import type { ImportSelectionV2, InputContractV2 } from "./codingV2";
export interface ImportSpec {
  scope: string[];
  protected: string[] | null;
  acceptance: string[];
  image: string;
  verification_v2?: ImportSelectionV2;
}
export interface ImportSelection {
  inspection_id: string;
  digest: string;
}
export interface InspectionRequest {
  repository_id?: string;
  repository_revision?: number;
  repo_path?: string;
  spec: ImportSpec;
  consent: boolean;
}
export interface RepositoryInspection {
  schema_version: number;
  id: string;
  repository_id?: string;
  repository_revision?: number;
  root: string;
  branch: string;
  commit: string;
  spec: ImportSpec;
  policy_version: string;
  status: string;
  stage: string;
  code: string;
  checked_at: string;
  working_tree_status: string;
  file_count: number | null;
  total_bytes: number | null;
  context_file_count: number | null;
  context_bytes: number | null;
  context_manifest: FileMeta[] | null;
  protected_inputs: string[] | null;
  digest: string;
  verification_v2?: InputContractV2;
}
export interface RepositoryImportBinding {
  schema_version: number;
  inspection_id: string;
  inspection_digest: string;
  import_operation_id: string;
  root: string;
  commit: string;
  base_digest: string;
  import_spec_digest: string;
}
export interface RepositoryImportOperation {
  id: string;
  inspection_id: string;
  intent_digest: string;
  mission_id: string;
  status: string;
  stage: string;
  code: string;
  revision: number;
  created_at: string;
  updated_at: string;
  binding?: RepositoryImportBinding;
  generation?: GenerationRef;
}
export interface RecoveryDiagnosticDTO {
  schema_version: number;
  mission_id: string;
  revision: number;
  epoch: number;
  checked_at: string;
  stale: boolean;
  checks: Array<{
    id: string;
    status:
      "verified" | "blocked" | "unknown" | "not_applicable" | "unsupported";
    code: string;
    message: string;
    records: Array<{ kind: string; id: string }>;
  }>;
  next_actions: Array<
    "inspect_records" | "replan" | "delivery_decision" | "new_task"
  >;
}
