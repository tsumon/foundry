import type { C4VMIdentity } from "./c4";

export const languageContracts = {
  c4_node_model_v2: {
    language: "javascript",
    label: "JavaScript",
    runner: "node-test-v2",
    verifier: "/opt/foundry/verify-node-v2",
    framework: "Node test runner",
    command: "/opt/foundry/verify-node-v2",
  },
  c4_typescript_model_v2: {
    language: "typescript",
    label: "TypeScript",
    runner: "typescript-node-test-v2",
    verifier: "/opt/foundry/verify-typescript-v2",
    framework: "TypeScript 编译 → Node test runner",
    command: "/opt/foundry/verify-typescript-v2",
  },
  c4_python_model_v2: {
    language: "python",
    label: "Python",
    runner: "python-unittest-v2",
    verifier: "/opt/foundry/verify-python-v2",
    framework: "Python unittest",
    command: "/opt/foundry/verify-python-v2",
  },
} as const;
export type LanguageProfileID = keyof typeof languageContracts;
export interface C4ProfileV2 {
  language: string;
  runner_id: string;
  runtime_inventory_digest: string;
  dependency_bundle_digest: string;
  protected_inputs_digest: string;
  execution_contract_digest: string;
}
export interface CodingProfileTemplateV2 {
  schema_version: 2;
  profile_id: LanguageProfileID;
  language: string;
  runner_id: string;
  verifier: string;
  mode: "embedded_vm";
  embedded_vm?: C4VMIdentity;
  runtime_inventory_digest?: string;
  available: boolean;
  reason: string;
  limits: {
    max_calls: number;
    max_repairs: number;
    max_source_files: number;
    max_source_bytes: number;
    max_dependency_files: number;
    max_dependency_bytes: number;
  };
}
export interface DependencyBindingV2 {
  schema_version: 1;
  ecosystem: "node" | "python";
  bundle_digest: string;
  manifest_digest: string;
  file_count: number;
  total_bytes: number;
}
export interface SourceDigestV2 {
  path: string;
  bytes: number;
  sha256: string;
}
export interface ProtectedInputV2 extends SourceDigestV2 {
  mode: number;
  role: "test" | "helper" | "manifest" | "config" | "lock";
}
export interface ImportSelectionV2 {
  schema_version: 2;
  profile_id: LanguageProfileID;
  embedded_vm: C4VMIdentity;
  runtime_inventory_digest: string;
  admission_digest: string;
  dependencies: DependencyBindingV2;
  source_root: "." | "src";
  test_root: "test" | "tests";
  test_support_prefixes: string[];
}
export interface InputContractV2 {
  schema_version: 2;
  profile_id: LanguageProfileID;
  dependencies: DependencyBindingV2;
  admission_digest: string;
  source_files: SourceDigestV2[];
  protected: ProtectedInputV2[];
  protected_prefixes: string[];
  test_files: string[];
  source_roots: Array<"." | "src">;
  harness_version: "foundry-test-v2";
  typescript?: {
    config_path: "tsconfig.json";
    config_digest: string;
    input_files: string[];
    output_root: "/foundry-build/compiled";
  };
  protected_inputs_digest: string;
  execution_contract_digest: string;
}
export interface DependencyAdmission {
  admission_digest: string;
  dependencies: DependencyBindingV2;
  source_files: SourceDigestV2[];
  summary: {
    schema_version: 1;
    ecosystem: "node" | "python";
    policy: string;
    provenance: string;
    admission_digest: string;
    bundle_digest: string;
    manifest_digest: string;
    file_count: number;
    total_bytes: number;
    package_count: number;
    packages: Array<{ location: string; name: string; version: string }>;
    omitted_count: number;
  };
}
export type RepositorySelector =
  | { repo_path: string; repository_id?: never; repository_revision?: never }
  | { repository_id: string; repository_revision: number; repo_path?: never };
export type DependencyImportRequest = RepositorySelector & {
  profile_id: LanguageProfileID;
  embedded_vm: C4VMIdentity;
  runtime_inventory_digest: string;
  input_directory: string;
  consent: boolean;
};

export const isRecord = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);
export const exactKeys = (
  v: unknown,
  required: string[],
  optional: string[] = [],
): v is Record<string, unknown> =>
  isRecord(v) &&
  required.every((k) => Object.hasOwn(v, k)) &&
  Object.keys(v).every((k) => required.includes(k) || optional.includes(k));
export const sha256Valid = (v: unknown): v is string =>
  typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
export const boundedInteger = (v: unknown, max: number, min = 0): v is number =>
  Number.isSafeInteger(v) && (v as number) >= min && (v as number) <= max;
export const languageProfileID = (v: unknown): v is LanguageProfileID =>
  typeof v === "string" && Object.hasOwn(languageContracts, v);
export const identityKeys = [
  "schema_version",
  "manifest_digest",
  "host_platform",
  "guest_platform",
  "runtime_digest",
  "kernel_digest",
  "rootfs_digest",
  "verifier_digest",
  "protocol",
  "policy_version",
];
export function identityV2Valid(v: unknown): v is C4VMIdentity {
  return (
    exactKeys(v, identityKeys) &&
    v.schema_version === 2 &&
    v.protocol === "foundry-vm-v2" &&
    v.policy_version === "multilang-vm-v2" &&
    ((v.host_platform === "darwin/arm64" &&
      v.guest_platform === "linux/arm64") ||
      (v.host_platform === "windows/amd64" &&
        v.guest_platform === "linux/amd64")) &&
    [
      v.manifest_digest,
      v.runtime_digest,
      v.kernel_digest,
      v.rootfs_digest,
      v.verifier_digest,
    ].every(sha256Valid)
  );
}
const stable = (v: unknown): unknown =>
  Array.isArray(v)
    ? v.map(stable)
    : isRecord(v)
      ? Object.fromEntries(
          Object.keys(v)
            .sort()
            .map((k) => [k, stable(v[k])]),
        )
      : v;
export const materialEqual = (a: unknown, b: unknown) =>
  JSON.stringify(stable(a)) === JSON.stringify(stable(b));
export function templateV2Valid(v: unknown): v is CodingProfileTemplateV2 {
  if (
    !exactKeys(
      v,
      [
        "schema_version",
        "profile_id",
        "language",
        "runner_id",
        "verifier",
        "mode",
        "available",
        "reason",
        "limits",
      ],
      ["embedded_vm", "runtime_inventory_digest"],
    ) ||
    !languageProfileID(v.profile_id)
  )
    return false;
  const c = languageContracts[v.profile_id],
    limits = v.limits;
  const reason = [
    "",
    "embedded_vm_v2_bundle_unavailable",
    "coding_runtime_unavailable_or_expired",
    "coding_workbench_disabled",
    "recovery_read_only",
  ];
  return (
    v.schema_version === 2 &&
    v.language === c.language &&
    v.runner_id === c.runner &&
    v.verifier === c.verifier &&
    v.mode === "embedded_vm" &&
    typeof v.available === "boolean" &&
    typeof v.reason === "string" &&
    reason.includes(v.reason) &&
    v.available === (v.reason === "") &&
    exactKeys(limits, [
      "max_calls",
      "max_repairs",
      "max_source_files",
      "max_source_bytes",
      "max_dependency_files",
      "max_dependency_bytes",
    ]) &&
    materialEqual(limits, {
      max_calls: 16,
      max_repairs: 2,
      max_source_files: 500,
      max_source_bytes: 10485760,
      max_dependency_files: 10000,
      max_dependency_bytes: 67108864,
    }) &&
    (v.embedded_vm === undefined && v.runtime_inventory_digest === undefined
      ? !v.available && v.reason === "embedded_vm_v2_bundle_unavailable"
      : identityV2Valid(v.embedded_vm) &&
        sha256Valid(v.runtime_inventory_digest))
  );
}
export function profileV2Valid(v: unknown): boolean {
  if (
    !exactKeys(v, [
      "schema_version",
      "control_version",
      "id",
      "mode",
      "image_id",
      "platform",
      "docker_path",
      "endpoint",
      "verifier",
      "embedded_vm",
      "v2",
    ]) ||
    !languageProfileID(v.id) ||
    !identityV2Valid(v.embedded_vm)
  )
    return false;
  const c = languageContracts[v.id],
    p = v.v2;
  return (
    v.schema_version === 2 &&
    v.control_version === 1 &&
    v.mode === "embedded_vm" &&
    v.image_id === "" &&
    v.docker_path === "" &&
    v.endpoint === "" &&
    v.platform === v.embedded_vm.guest_platform &&
    v.verifier === c.verifier &&
    exactKeys(p, [
      "language",
      "runner_id",
      "runtime_inventory_digest",
      "dependency_bundle_digest",
      "protected_inputs_digest",
      "execution_contract_digest",
    ]) &&
    p.language === c.language &&
    p.runner_id === c.runner &&
    [
      p.runtime_inventory_digest,
      p.dependency_bundle_digest,
      p.protected_inputs_digest,
      p.execution_contract_digest,
    ].every(sha256Valid)
  );
}
export const dependencyKeys = [
  "schema_version",
  "ecosystem",
  "bundle_digest",
  "manifest_digest",
  "file_count",
  "total_bytes",
];
export function dependencyBindingValid(v: unknown): v is DependencyBindingV2 {
  return (
    exactKeys(v, dependencyKeys) &&
    v.schema_version === 1 &&
    (v.ecosystem === "node" || v.ecosystem === "python") &&
    sha256Valid(v.bundle_digest) &&
    sha256Valid(v.manifest_digest) &&
    boundedInteger(v.file_count, 10000) &&
    boundedInteger(v.total_bytes, 67108864)
  );
}
export function portablePath(v: unknown): v is string {
  return (
    typeof v === "string" &&
    v.length > 0 &&
    v.length <= 1024 &&
    /^[\x20-\x7e]+$/.test(v) &&
    !/[\\:*?"<>|~]/.test(v) &&
    v.split("/").length <= 32 &&
    v
      .split("/")
      .every(
        (p) =>
          !!p &&
          p !== "." &&
          p !== ".." &&
          !/[. ]$/.test(p) &&
          !/^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\.|$)/i.test(p),
      )
  );
}
export const sortedPaths = (v: unknown): v is string[] =>
  Array.isArray(v) &&
  v.length <= 500 &&
  v.every((p, i) => portablePath(p) && (i === 0 || p > v[i - 1])) &&
  new Set(v.map((p) => p.toLowerCase())).size === v.length;
export function importSelectionV2Valid(v: unknown): v is ImportSelectionV2 {
  return (
    exactKeys(v, [
      "schema_version",
      "profile_id",
      "embedded_vm",
      "runtime_inventory_digest",
      "admission_digest",
      "dependencies",
      "source_root",
      "test_root",
      "test_support_prefixes",
    ]) &&
    v.schema_version === 2 &&
    languageProfileID(v.profile_id) &&
    identityV2Valid(v.embedded_vm) &&
    sha256Valid(v.runtime_inventory_digest) &&
    sha256Valid(v.admission_digest) &&
    dependencyBindingValid(v.dependencies) &&
    v.dependencies.ecosystem ===
      (v.profile_id === "c4_python_model_v2" ? "python" : "node") &&
    (v.source_root === "." || v.source_root === "src") &&
    (v.test_root === "tests" ||
      (v.test_root === "test" && v.profile_id !== "c4_python_model_v2")) &&
    sortedPaths(v.test_support_prefixes)
  );
}
export const sourceDigestValid = (v: unknown): v is SourceDigestV2 =>
  exactKeys(v, ["path", "bytes", "sha256"]) &&
  portablePath(v.path) &&
  boundedInteger(v.bytes, 10485760) &&
  sha256Valid(v.sha256);
const metadataNames = (id: LanguageProfileID) =>
  id === "c4_python_model_v2"
    ? ["foundry.python.lock.json"]
    : id === "c4_typescript_model_v2"
      ? ["package-lock.json", "package.json", "tsconfig.json"]
      : ["package-lock.json", "package.json"];
export function inputContractV2Valid(v: unknown): v is InputContractV2 {
  if (
    !exactKeys(
      v,
      [
        "schema_version",
        "profile_id",
        "dependencies",
        "admission_digest",
        "source_files",
        "protected",
        "protected_prefixes",
        "test_files",
        "source_roots",
        "harness_version",
        "protected_inputs_digest",
        "execution_contract_digest",
      ],
      ["typescript"],
    ) ||
    v.schema_version !== 2 ||
    !languageProfileID(v.profile_id) ||
    !dependencyBindingValid(v.dependencies) ||
    v.dependencies.ecosystem !==
      (v.profile_id === "c4_python_model_v2" ? "python" : "node") ||
    !sha256Valid(v.admission_digest) ||
    !sha256Valid(v.protected_inputs_digest) ||
    !sha256Valid(v.execution_contract_digest) ||
    v.harness_version !== "foundry-test-v2" ||
    (!materialEqual(v.source_roots, ["."]) &&
      !materialEqual(v.source_roots, ["src"])) ||
    !sortedPaths(v.protected_prefixes) ||
    !sortedPaths(v.test_files) ||
    !v.test_files.length ||
    !Array.isArray(v.source_files) ||
    !v.source_files.every(sourceDigestValid) ||
    !materialEqual(
      v.source_files.map((f) => f.path),
      metadataNames(v.profile_id),
    ) ||
    !Array.isArray(v.protected) ||
    v.protected.length > 500
  )
    return false;
  const protectedFiles = v.protected;
  if (
    !protectedFiles.every(
      (f) =>
        exactKeys(f, ["path", "mode", "bytes", "sha256", "role"]) &&
        portablePath(f.path) &&
        (f.mode === 420 || f.mode === 493) &&
        boundedInteger(f.bytes, 10485760) &&
        sha256Valid(f.sha256) &&
        ["test", "helper", "manifest", "config", "lock"].includes(
          f.role as string,
        ),
    ) ||
    !sortedPaths(protectedFiles.map((f) => f.path)) ||
    protectedFiles.reduce((n, f) => n + f.bytes, 0) > 10485760
  )
    return false;
  const files = new Map(protectedFiles.map((f) => [f.path, f]));
  if (
    !v.source_files.every((f) => {
      const p = files.get(f.path);
      return (
        p &&
        p.bytes === f.bytes &&
        p.sha256 === f.sha256 &&
        p.role ===
          (f.path === "package.json"
            ? "manifest"
            : f.path === "tsconfig.json"
              ? "config"
              : "lock")
      );
    })
  )
    return false;
  const prefixes = v.protected_prefixes,
    tests = v.test_files;
  const root = tests[0].split("/")[0],
    python = v.profile_id === "c4_python_model_v2",
    ts = v.profile_id === "c4_typescript_model_v2";
  if (
    (root !== "tests" && (root !== "test" || python)) ||
    !v.protected_prefixes.includes(root) ||
    ![".foundry-build", ".venv", "node_modules", "site-packages"].every((p) =>
      prefixes.includes(p),
    ) ||
    !v.test_files.every(
      (p) =>
        p.startsWith(root + "/") &&
        files.get(p)?.role === "test" &&
        (python
          ? /\/test_[^/]*\.py$/.test(p)
          : ts
            ? /\.test\.ts$/.test(p)
            : /\.test\.(js|mjs|cjs)$/.test(p)),
    ) ||
    protectedFiles.some((f) => f.role === "test" && !tests.includes(f.path))
  )
    return false;
  // The entire portable namespace must agree about directory spelling and file ownership.
  const names = new Map<string, string>(),
    fileSet = new Set(protectedFiles.map((f) => f.path.toLowerCase()));
  for (const p of [
    ...v.protected_prefixes,
    ...protectedFiles.map((f) => f.path),
    ...(ts && isRecord(v.typescript) && Array.isArray(v.typescript.input_files)
      ? v.typescript.input_files
      : []),
  ]) {
    if (!portablePath(p)) return false;
    const parts = p.split("/");
    for (let i = 1; i <= parts.length; i++) {
      const prefix = parts.slice(0, i).join("/"),
        key = prefix.toLowerCase();
      if (
        (names.has(key) && names.get(key) !== prefix) ||
        (i < parts.length && fileSet.has(key))
      )
        return false;
      names.set(key, prefix);
    }
  }
  if (
    protectedFiles.some((f) =>
      f.path
        .split("/")
        .some((p: string) =>
          [".foundry-build", ".venv", "node_modules", "site-packages"].includes(
            p.toLowerCase(),
          ),
        ),
    )
  )
    return false;
  if (!ts) return v.typescript === undefined;
  const t = v.typescript;
  if (
    !exactKeys(t, [
      "config_path",
      "config_digest",
      "input_files",
      "output_root",
    ]) ||
    !sortedPaths(t.input_files)
  )
    return false;
  const inputs = t.input_files;
  return (
    t.config_path === "tsconfig.json" &&
    t.config_digest === files.get("tsconfig.json")?.sha256 &&
    t.output_root === "/foundry-build/compiled" &&
    inputs.every((p) => p.endsWith(".ts")) &&
    tests.every((p) => inputs.includes(p))
  );
}
export function admissionValid(
  v: unknown,
  id: LanguageProfileID,
): v is DependencyAdmission {
  if (
    !exactKeys(v, [
      "admission_digest",
      "dependencies",
      "source_files",
      "summary",
    ]) ||
    !sha256Valid(v.admission_digest) ||
    !dependencyBindingValid(v.dependencies) ||
    v.dependencies.ecosystem !==
      (id === "c4_python_model_v2" ? "python" : "node") ||
    !Array.isArray(v.source_files) ||
    !v.source_files.every(sourceDigestValid) ||
    !materialEqual(
      v.source_files.map((f) => f.path),
      metadataNames(id === "c4_typescript_model_v2" ? "c4_node_model_v2" : id),
    )
  )
    return false;
  const s = v.summary,
    b = v.dependencies,
    python = id === "c4_python_model_v2";
  if (
    !exactKeys(s, [
      "schema_version",
      "ecosystem",
      "policy",
      "provenance",
      "admission_digest",
      "bundle_digest",
      "manifest_digest",
      "file_count",
      "total_bytes",
      "package_count",
      "packages",
      "omitted_count",
    ]) ||
    !Array.isArray(s.packages)
  )
    return false;
  const packages = s.packages;
  return (
    s.schema_version === 1 &&
    s.ecosystem === b.ecosystem &&
    s.policy ===
      (python ? "python-wheel-numeric-v1" : "node-lock-v3-ranges-v1") &&
    s.provenance === (python ? "user_supplied_wheels" : "user_supplied_tree") &&
    s.admission_digest === v.admission_digest &&
    s.bundle_digest === b.bundle_digest &&
    s.manifest_digest === b.manifest_digest &&
    s.file_count === b.file_count &&
    s.total_bytes === b.total_bytes &&
    boundedInteger(s.package_count, 512) &&
    boundedInteger(s.omitted_count, 512) &&
    Array.isArray(s.packages) &&
    s.package_count === s.packages.length + s.omitted_count &&
    s.packages.every(
      (p, i) =>
        exactKeys(p, ["location", "name", "version"]) &&
        typeof p.location === "string" &&
        portablePath(p.location) &&
        typeof p.name === "string" &&
        p.name.length > 0 &&
        p.name.length <= 1024 &&
        typeof p.version === "string" &&
        p.version.length > 0 &&
        p.version.length <= 1024 &&
        !/[\u0000-\u001f]/.test(p.name + p.version) &&
        (i === 0 || p.location > packages[i - 1].location),
    ) &&
    new TextEncoder().encode(JSON.stringify(s)).length <= 65536
  );
}
