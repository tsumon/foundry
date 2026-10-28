import type { ImportSpec, RepositoryInspection } from "./repositoryImportTypes";
import {
  boundedInteger,
  dependencyKeys,
  exactKeys,
  identityKeys,
  importSelectionV2Valid,
  inputContractV2Valid,
  languageContracts,
  materialEqual,
  sha256Valid,
} from "./codingV2";

// Go encoding/json escapes these characters even inside nested RawMessages.
const goJSON = (value: unknown) =>
  JSON.stringify(value).replace(
    /[<>&\u2028\u2029]/g,
    (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
const ordered = (value: Record<string, unknown>, keys: string[]) =>
  Object.fromEntries(
    keys.filter((k) => Object.hasOwn(value, k)).map((k) => [k, value[k]]),
  );
async function digest(prefix: string, value: unknown) {
  const bytes = new TextEncoder().encode(prefix + goJSON(value));
  return Array.from(
    new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
}
function selectionJSON(v: Record<string, unknown>) {
  return {
    ...ordered(v, ["schema_version", "profile_id"]),
    embedded_vm: ordered(
      v.embedded_vm as Record<string, unknown>,
      identityKeys,
    ),
    ...ordered(v, ["runtime_inventory_digest", "admission_digest"]),
    dependencies: ordered(
      v.dependencies as Record<string, unknown>,
      dependencyKeys,
    ),
    ...ordered(v, ["source_root", "test_root", "test_support_prefixes"]),
  };
}
function specJSON(s: ImportSpec) {
  return {
    scope: s.scope,
    protected: s.protected,
    acceptance: s.acceptance,
    image: s.image,
    ...(s.verification_v2
      ? {
          verification_v2: selectionJSON(
            s.verification_v2 as unknown as Record<string, unknown>,
          ),
        }
      : {}),
  };
}
function inputJSON(
  c: NonNullable<RepositoryInspection["verification_v2"]>,
  unsigned = false,
) {
  return {
    schema_version: c.schema_version,
    profile_id: c.profile_id,
    dependencies: ordered(
      c.dependencies as unknown as Record<string, unknown>,
      dependencyKeys,
    ),
    admission_digest: c.admission_digest,
    source_files: c.source_files.map((f) => ({
      path: f.path,
      bytes: f.bytes,
      sha256: f.sha256,
    })),
    protected: c.protected.map((f) => ({
      path: f.path,
      mode: f.mode,
      bytes: f.bytes,
      sha256: f.sha256,
      role: f.role,
    })),
    protected_prefixes: c.protected_prefixes,
    test_files: c.test_files,
    source_roots: c.source_roots,
    harness_version: c.harness_version,
    ...(c.typescript
      ? {
          typescript: {
            config_path: c.typescript.config_path,
            config_digest: c.typescript.config_digest,
            input_files: c.typescript.input_files,
            output_root: c.typescript.output_root,
          },
        }
      : {}),
    protected_inputs_digest: unsigned ? "" : c.protected_inputs_digest,
    execution_contract_digest: unsigned ? "" : c.execution_contract_digest,
  };
}
const text = (v: unknown, max: number, required = false): v is string =>
  typeof v === "string" &&
  new TextEncoder().encode(v).length <= max &&
  !/[\u0000-\u001f\u007f-\u009f]/.test(v) &&
  (!required || !!v.trim());
const relative = (v: unknown): v is string =>
  text(v, 4096, true) &&
  !v.startsWith("/") &&
  !v.includes("\\") &&
  v.split("/").every((p) => !!p && p !== "." && p !== "..");
const absolute = (v: unknown): v is string =>
  text(v, 4096, true) && (v.startsWith("/") || /^[A-Za-z]:[\\/]/.test(v));
function specValid(s: unknown): s is ImportSpec {
  if (
    !exactKeys(
      s,
      ["scope", "protected", "acceptance", "image"],
      ["verification_v2"],
    ) ||
    !Array.isArray(s.scope) ||
    !s.scope.length ||
    s.scope.length > 500 ||
    !(
      s.protected === null ||
      (Array.isArray(s.protected) && s.protected.length <= 500)
    ) ||
    ![...s.scope, ...((s.protected as unknown[]) ?? [])].every(
      (p) =>
        typeof p === "string" && relative(p.endsWith("/") ? p.slice(0, -1) : p),
    ) ||
    !Array.isArray(s.acceptance) ||
    s.acceptance.length < 1 ||
    s.acceptance.length > 12 ||
    !s.acceptance.every((c) => text(c, 8192, true)) ||
    !text(s.image, 512)
  )
    return false;
  return (
    new TextEncoder().encode(
      s.image +
        s.scope.join("") +
        ((s.protected as string[]) ?? []).join("") +
        s.acceptance.join(""),
    ).length <= 262144 &&
    (s.verification_v2 === undefined ||
      importSelectionV2Valid(s.verification_v2))
  );
}
export const inspectionSpecMatches = (a: ImportSpec, b: ImportSpec) =>
  materialEqual(
    { ...a, protected: a.protected ?? [] },
    { ...b, protected: b.protected ?? [] },
  );
const stages: Record<string, string[]> = {
  inputs: ["invalid_verification_inputs"],
  dependencies: ["invalid_dependencies"],
  identity: [
    "repository_unavailable",
    "unsupported_layout",
    "missing_head",
    "inspection_failed",
    "git_unavailable",
  ],
  location: ["source_data_overlap"],
  tree: [
    "objects_unavailable",
    "unsupported_entry",
    "unsafe_path",
    "case_collision",
    "binary_file",
    "tree_limit",
    "context_limit",
    "empty_context",
    "inspection_failed",
  ],
  acceptance: ["unsupported_acceptance"],
};
export async function parseRepositoryInspection(
  value: unknown,
): Promise<RepositoryInspection | null> {
  if (
    !exactKeys(
      value,
      [
        "schema_version",
        "id",
        "root",
        "branch",
        "commit",
        "spec",
        "policy_version",
        "status",
        "stage",
        "code",
        "checked_at",
        "working_tree_status",
        "file_count",
        "total_bytes",
        "context_file_count",
        "context_bytes",
        "context_manifest",
        "protected_inputs",
        "digest",
      ],
      ["repository_id", "repository_revision", "verification_v2"],
    )
  )
    return null;
  const r = value;
  if (
    (r.schema_version !== 1 && r.schema_version !== 2) ||
    typeof r.id !== "string" ||
    !/^[a-zA-Z0-9_-]{1,64}$/.test(r.id) ||
    !specValid(r.spec) ||
    !text(r.root, 4096) ||
    (r.root !== "" && !absolute(r.root)) ||
    !text(r.branch, 1024) ||
    typeof r.commit !== "string" ||
    (r.commit !== "" && !/^([a-f0-9]{40}|[a-f0-9]{64})$/.test(r.commit)) ||
    !sha256Valid(r.digest) ||
    r.working_tree_status !== "not_checked" ||
    typeof r.checked_at !== "string" ||
    !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]00:00)$/.test(
      r.checked_at,
    ) ||
    !Number.isFinite(Date.parse(r.checked_at)) ||
    typeof r.stage !== "string" ||
    typeof r.code !== "string"
  )
    return null;
  if (
    (r.repository_id !== undefined &&
      (typeof r.repository_id !== "string" ||
        !/^[a-zA-Z0-9_-]{1,64}$/.test(r.repository_id) ||
        !boundedInteger(r.repository_revision, Number.MAX_SAFE_INTEGER, 1))) ||
    (r.repository_id === undefined && r.repository_revision !== undefined)
  )
    return null;
  if (
    r.schema_version === 1
      ? r.policy_version !== "committed_text_v1" ||
        r.spec.verification_v2 !== undefined ||
        r.verification_v2 !== undefined
      : r.policy_version !== "committed_text_c4_v2" ||
        !importSelectionV2Valid(r.spec.verification_v2)
  )
    return null;
  if (r.status === "ready") {
    if (
      r.stage !== "complete" ||
      r.code !== "" ||
      !r.root ||
      !r.commit ||
      !boundedInteger(r.file_count, 500, 1) ||
      !boundedInteger(r.total_bytes, 10485760) ||
      !boundedInteger(r.context_file_count, 30, 1) ||
      r.context_file_count > r.file_count ||
      !boundedInteger(r.context_bytes, 204800) ||
      r.context_bytes > r.total_bytes ||
      !Array.isArray(r.context_manifest) ||
      r.context_manifest.length !== r.context_file_count ||
      !(
        r.protected_inputs === null ||
        (Array.isArray(r.protected_inputs) && r.protected_inputs.length <= 500)
      )
    )
      return null;
    const seen = new Set<string>();
    let bytes = 0;
    for (const f of r.context_manifest) {
      if (
        !exactKeys(f, ["path", "sha256", "bytes"]) ||
        !relative(f.path) ||
        !sha256Valid(f.sha256) ||
        !boundedInteger(f.bytes, 204800) ||
        seen.has(f.path.toLowerCase())
      )
        return null;
      seen.add(f.path.toLowerCase());
      bytes += f.bytes;
    }
    if (
      bytes !== r.context_bytes ||
      (r.protected_inputs !== null &&
        !r.protected_inputs.every(
          (p, i) =>
            relative(p) &&
            (i === 0 || p > (r.protected_inputs as string[])[i - 1]),
        ))
    )
      return null;
    if (r.schema_version === 2) {
      if (!inputContractV2Valid(r.verification_v2)) return null;
      const c = r.verification_v2,
        s = r.spec.verification_v2!;
      const prefixes = [
        ...new Set([
          ".foundry-build",
          ".venv",
          "node_modules",
          "site-packages",
          s.test_root,
          ...s.test_support_prefixes,
        ]),
      ].sort();
      if (
        c.profile_id !== s.profile_id ||
        c.admission_digest !== s.admission_digest ||
        !materialEqual(c.dependencies, s.dependencies) ||
        !materialEqual(c.source_roots, [s.source_root]) ||
        !materialEqual(c.protected_prefixes, prefixes) ||
        !materialEqual(
          r.protected_inputs,
          c.protected.map((f) => f.path),
        ) ||
        c.test_files.some((p) => !p.startsWith(s.test_root + "/")) ||
        r.spec.image !== "" ||
        !materialEqual(r.spec.acceptance, [
          languageContracts[s.profile_id].verifier,
        ])
      )
        return null;
      const canonical = inputJSON(c),
        p = await digest("foundry:c4:v2:protected-inputs\n", {
          protected: canonical.protected,
          protected_prefixes: canonical.protected_prefixes,
        }),
        e = await digest(
          "foundry:c4:v2:execution-contract\n",
          inputJSON(c, true),
        );
      if (p !== c.protected_inputs_digest || e !== c.execution_contract_digest)
        return null;
    }
  } else if (r.status === "blocked") {
    const timeout =
      (r.code === "timeout" || r.code === "canceled") &&
      Object.hasOwn(stages, r.stage);
    if (
      !(stages[r.stage]?.includes(r.code) || timeout) ||
      (r.schema_version === 1 &&
        (r.stage === "inputs" || r.stage === "dependencies")) ||
      r.verification_v2 !== undefined ||
      [r.file_count, r.total_bytes, r.context_file_count, r.context_bytes].some(
        (x) => x !== null,
      ) ||
      !(
        r.context_manifest === null ||
        (Array.isArray(r.context_manifest) && !r.context_manifest.length)
      ) ||
      !(
        r.protected_inputs === null ||
        (Array.isArray(r.protected_inputs) && !r.protected_inputs.length)
      )
    )
      return null;
  } else return null;
  const canonical = {
    ...r,
    spec: specJSON(r.spec),
    context_manifest: Array.isArray(r.context_manifest)
      ? r.context_manifest.map((f) => ({
          path: f.path,
          sha256: f.sha256,
          bytes: f.bytes,
        }))
      : null,
    ...(r.verification_v2
      ? {
          verification_v2: inputJSON(
            r.verification_v2 as NonNullable<
              RepositoryInspection["verification_v2"]
            >,
          ),
        }
      : {}),
  };
  const fields = Object.fromEntries(
    Object.keys(canonical)
      .filter((k) => k !== "digest")
      .sort()
      .map((k) => [k, canonical[k as keyof typeof canonical]]),
  );
  if (
    new TextEncoder().encode(goJSON(canonical)).length >
      (r.schema_version === 2 ? 2097152 : 1048576) ||
    (await digest(`repository_inspection_v${r.schema_version}\n`, fields)) !==
      r.digest
  )
    return null;
  return r as unknown as RepositoryInspection;
}
