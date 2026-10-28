import { c4Mission, c4Operation } from "./c4Fixtures";
import { createHash, webcrypto } from "node:crypto";
import type {
  RepositoryInspection,
  ImportSpec,
} from "../repositoryImportTypes";
export const testCrypto = webcrypto as unknown as Crypto;
export const sha = (text: string) =>
  createHash("sha256").update(text).digest("hex");
const json = (value: unknown) =>
  JSON.stringify(value).replace(
    /[<>&\u2028\u2029]/g,
    (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
export const identityV2 = {
  schema_version: 2,
  manifest_digest: "a".repeat(64),
  host_platform: "darwin/arm64",
  guest_platform: "linux/arm64",
  runtime_digest: "b".repeat(64),
  kernel_digest: "c".repeat(64),
  rootfs_digest: "d".repeat(64),
  verifier_digest: "e".repeat(64),
  protocol: "foundry-vm-v2",
  policy_version: "multilang-vm-v2",
};
const rows = [
  [
    "c4_node_model_v2",
    "javascript",
    "node-test-v2",
    "/opt/foundry/verify-node-v2",
  ],
  [
    "c4_typescript_model_v2",
    "typescript",
    "typescript-node-test-v2",
    "/opt/foundry/verify-typescript-v2",
  ],
  [
    "c4_python_model_v2",
    "python",
    "python-unittest-v2",
    "/opt/foundry/verify-python-v2",
  ],
];
export const templatesV2 = rows.map(
  ([profile_id, language, runner_id, verifier]) => ({
    schema_version: 2,
    profile_id,
    language,
    runner_id,
    verifier,
    mode: "embedded_vm",
    embedded_vm: identityV2,
    runtime_inventory_digest: "f".repeat(64),
    available: true,
    reason: "",
    limits: {
      max_calls: 16,
      max_repairs: 2,
      max_source_files: 500,
      max_source_bytes: 10485760,
      max_dependency_files: 10000,
      max_dependency_bytes: 67108864,
    },
  }),
);
const legacy = {
  schema_version: 1,
  control_version: 1,
  id: "c4_go_model_v1",
  mode: "embedded_vm",
  image_id: "",
  platform: "",
  docker_path: "",
  endpoint: "",
  verifier: "/opt/foundry/verify-go-v1",
};
export const capabilityV2 = {
  enabled: true,
  available: false,
  reason: "embedded_vm_bundle_unavailable",
  default_backend: "embedded_vm",
  profile: legacy,
  backends: [
    {
      mode: "embedded_vm",
      available: false,
      profile: legacy,
      prerequisite: "legacy absent",
    },
    {
      mode: "docker",
      available: false,
      profile: {
        ...legacy,
        mode: "docker",
        platform: "linux/arm64",
        docker_path: "/usr/bin/docker",
        endpoint: "unix:///var/run/docker.sock",
      },
      prerequisite: "explicit Docker",
    },
  ],
  profiles: templatesV2,
  limits: { max_calls: 16, max_repairs: 2 },
  provider_families: ["openai", "custom"],
  approval_required: [
    "readiness",
    "baseline",
    "context",
    "execute",
    "delivery",
  ],
};
export function admissionV2(index = 0) {
  const python = index === 2,
    dependencies = {
      schema_version: 1,
      ecosystem: python ? "python" : "node",
      bundle_digest: "1".repeat(64),
      manifest_digest: "2".repeat(64),
      file_count: 3,
      total_bytes: 120,
    };
  return {
    admission_digest: "3".repeat(64),
    dependencies,
    source_files: (python
      ? ["foundry.python.lock.json"]
      : ["package-lock.json", "package.json"]
    ).map((path) => ({ path, bytes: 32, sha256: "4".repeat(64) })),
    summary: {
      schema_version: 1,
      ecosystem: dependencies.ecosystem,
      policy: python ? "python-wheel-numeric-v1" : "node-lock-v3-ranges-v1",
      provenance: python ? "user_supplied_wheels" : "user_supplied_tree",
      admission_digest: "3".repeat(64),
      bundle_digest: dependencies.bundle_digest,
      manifest_digest: dependencies.manifest_digest,
      file_count: 3,
      total_bytes: 120,
      package_count: 1,
      packages: [
        {
          location: python ? "demo" : "node_modules/demo",
          name: "demo",
          version: "1.0.0",
        },
      ],
      omitted_count: 0,
    },
  };
}
export function sealReceipt<T extends object>(
  value: T,
): T & { digest: string } {
  const fields = { ...value } as Record<string, unknown>;
  delete fields.digest;
  const ordered = Object.fromEntries(
    Object.keys(fields)
      .sort()
      .map((k) => [k, fields[k]]),
  );
  return {
    ...value,
    digest: sha(
      `repository_inspection_v${fields.schema_version}\n` + json(ordered),
    ),
  };
}
export function legacyReceipt(
  spec: ImportSpec,
  root = "/tmp/source",
): RepositoryInspection {
  return sealReceipt({
    schema_version: 1,
    id: "inspection-1",
    root,
    branch: "main",
    commit: "a".repeat(40),
    spec,
    policy_version: "committed_text_v1",
    status: "ready",
    stage: "complete",
    code: "",
    checked_at: "2026-10-29T00:00:00Z",
    working_tree_status: "not_checked",
    file_count: 1,
    total_bytes: 10,
    context_file_count: 1,
    context_bytes: 10,
    context_manifest: [
      { path: "src/main.txt", sha256: "5".repeat(64), bytes: 10 },
    ],
    protected_inputs: [],
  });
}
export function receiptV2(index = 0, support: string[] = []) {
  const template = templatesV2[index],
    a = admissionV2(index),
    test =
      index === 2
        ? "tests/test_demo.py"
        : index === 1
          ? "tests/demo.test.ts"
          : "tests/demo.test.js";
  const source_files = [
    ...a.source_files,
    ...(index === 1
      ? [{ path: "tsconfig.json", bytes: 40, sha256: "6".repeat(64) }]
      : []),
  ];
  const selection = {
    schema_version: 2,
    profile_id: template.profile_id,
    embedded_vm: identityV2,
    runtime_inventory_digest: template.runtime_inventory_digest,
    admission_digest: a.admission_digest,
    dependencies: a.dependencies,
    source_root: ".",
    test_root: "tests",
    test_support_prefixes: support,
  };
  const contract = {
    schema_version: 2,
    profile_id: template.profile_id,
    dependencies: a.dependencies,
    admission_digest: a.admission_digest,
    source_files,
    protected: [
      ...source_files.map((f) => ({
        ...f,
        mode: 420,
        role:
          f.path === "package.json"
            ? "manifest"
            : f.path === "tsconfig.json"
              ? "config"
              : "lock",
      })),
      {
        path: test,
        mode: 420,
        bytes: 20,
        sha256: "7".repeat(64),
        role: "test",
      },
    ]
      .sort((a, b) => (a.path < b.path ? -1 : 1))
      .map(({ path, mode, bytes, sha256, role }) => ({
        path,
        mode,
        bytes,
        sha256,
        role,
      })),
    protected_prefixes: [
      ".foundry-build",
      ".venv",
      "node_modules",
      "site-packages",
      "tests",
      ...support,
    ].sort(),
    test_files: [test],
    source_roots: ["."],
    harness_version: "foundry-test-v2",
    ...(index === 1
      ? {
          typescript: {
            config_path: "tsconfig.json",
            config_digest: "6".repeat(64),
            input_files: ["src/main.ts", test],
            output_root: "/foundry-build/compiled",
          },
        }
      : {}),
    protected_inputs_digest: "",
    execution_contract_digest: "",
  };
  const protectedDigest = sha(
      "foundry:c4:v2:protected-inputs\n" +
        json({
          protected: contract.protected,
          protected_prefixes: contract.protected_prefixes,
        }),
    ),
    executionDigest = sha(
      "foundry:c4:v2:execution-contract\n" + json(contract),
    );
  contract.protected_inputs_digest = protectedDigest;
  contract.execution_contract_digest = executionDigest;
  const r = legacyReceipt({
    scope: ["src/"],
    protected: [],
    acceptance: [template.verifier],
    image: "",
    verification_v2: selection,
  } as ImportSpec);
  return sealReceipt({
    ...r,
    schema_version: 2,
    file_count: source_files.length + 2,
    total_bytes: contract.protected.reduce((total, f) => total + f.bytes, 10),
    policy_version: "committed_text_c4_v2",
    protected_inputs: contract.protected.map((f) => f.path),
    verification_v2: contract,
  });
}
export function profileV2(index = 0) {
  const r = receiptV2(index),
    t = templatesV2[index];
  return {
    schema_version: 2,
    control_version: 1,
    id: t.profile_id,
    mode: "embedded_vm",
    image_id: "",
    platform: "linux/arm64",
    docker_path: "",
    endpoint: "",
    verifier: t.verifier,
    embedded_vm: identityV2,
    v2: {
      language: t.language,
      runner_id: t.runner_id,
      runtime_inventory_digest: t.runtime_inventory_digest,
      dependency_bundle_digest: r.verification_v2.dependencies.bundle_digest,
      protected_inputs_digest: r.verification_v2.protected_inputs_digest,
      execution_contract_digest: r.verification_v2.execution_contract_digest,
    },
  };
}

export function evidenceV2(index = 0) {
  const p = profileV2(index),
    o = {
      ...c4Operation,
      readiness_id: "ready-v2",
      readiness_digest: "6".repeat(64),
    };
  const m = {
    ...c4Mission,
    import_version: 1,
    coding_version: 1,
    workspace_mode: "generations_v1" as const,
    spec: {
      ...c4Mission.spec,
      c4: p,
      acceptance: [p.verifier],
      image: "",
      image_id: "",
      protected: [
        ...receiptV2(index).protected_inputs,
        ...receiptV2(index).verification_v2.protected_prefixes,
      ].sort(),
    },
    c4_profile_binding: {
      revision: c4Mission.revision,
      digest: o.profile_digest,
      profile: p,
    },
    c4_operations: [o],
    c4_controls: [
      {
        schema_version: 1,
        id: "ready-v2",
        kind: "readiness",
        mission_id: c4Mission.id,
        epoch: c4Mission.epoch,
        plan_revision: 0,
        base_commit: c4Mission.base_commit,
        base_digest: c4Mission.repository_import!.base_digest,
        profile_digest: o.profile_digest,
        activation_digest: "f".repeat(64),
        activation_expires_at: "2026-10-29T10:00:00Z",
        source_kind: "embedded_vm_c4_control",
        prepared_revision: 1,
        prepared_at: o.prepared_at,
        expires_at: "2026-10-29T10:00:00Z",
        material_digest: o.readiness_digest,
        state: "completed",
        approval: { ...o.approval!, digest: o.readiness_digest },
        dispatch: o.dispatch,
        result: { ...o.result!, status: "ready" },
      },
    ],
  };
  return m;
}
