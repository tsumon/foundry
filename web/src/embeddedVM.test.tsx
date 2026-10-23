import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useCallback, useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import {
  c4BaselinePassed,
  c4CandidatePassed,
  c4ReadinessFresh,
  c4ControlSource,
  c4OperationLabel,
  c4Supported,
  isModelBackedC4,
  type C4Control,
} from "./c4";
import { decisionMaterial } from "./decisionMaterials";
import { parseCodingTaskCapability } from "./codingTaskApi";
import {
  c4CandidateOperation,
  c4Mission,
  c4Operation,
  c4Profile,
} from "./test/c4Fixtures";
import vmStatusContract from "./test/c4VMStatusContract.json";
import {
  CodingTaskSetup,
  type CodingTaskSelection,
} from "./components/CodingTaskSetup";
import { C4Decision, C4Verification } from "./components/C4Verification";
import { api } from "./api";
import { response } from "./test/fixtures";

const identity = {
  schema_version: 1,
  manifest_digest: "1".repeat(64),
  host_platform: "darwin/arm64",
  guest_platform: "linux/arm64",
  runtime_digest: "2".repeat(64),
  kernel_digest: "3".repeat(64),
  rootfs_digest: "4".repeat(64),
  verifier_digest: "5".repeat(64),
  protocol: "foundry-vm-v1",
  policy_version: "go-vm-v1",
};
const profile = {
  ...c4Profile,
  id: "c4_go_model_v1",
  mode: "embedded_vm",
  control_version: 1,
  image_id: "",
  docker_path: "",
  endpoint: "",
  embedded_vm: identity,
};
const docker = {
  ...c4Profile,
  id: "c4_go_model_v1",
  mode: "docker",
  control_version: 1,
};
const metadata = {
  enabled: true,
  available: true,
  reason: "",
  default_backend: "embedded_vm",
  profile,
  backends: [
    {
      mode: "embedded_vm",
      available: true,
      profile,
      prerequisite: "hardware virtualization",
    },
    {
      mode: "docker",
      available: true,
      profile: docker,
      prerequisite: "explicit Docker selection",
    },
  ],
  limits: { max_calls: 16, max_repairs: 2 },
  provider_families: ["openai"],
  approval_required: [
    "readiness",
    "baseline",
    "context",
    "execute",
    "delivery",
  ],
};
const vmMission = {
  ...c4Mission,
  spec: { ...c4Mission.spec, c4: profile },
  c4_profile_binding: {
    revision: c4Mission.revision,
    profile: structuredClone(profile),
    digest: c4Operation.profile_digest,
  },
};
afterEach(() => vi.restoreAllMocks());

it("accepts server-frozen VM metadata and keeps missing bundle blocked without discarding Docker choice", () => {
  expect(parseCodingTaskCapability(metadata)).not.toBeNull();
  const absent = { ...profile, platform: "", embedded_vm: undefined };
  expect(
    parseCodingTaskCapability({
      ...metadata,
      available: false,
      reason: "embedded_vm_bundle_unavailable",
      profile: absent,
      backends: [
        { ...metadata.backends[0], available: false, profile: absent },
        metadata.backends[1],
      ],
    }),
  ).not.toBeNull();
});

it("recognizes VM profiles as real model-backed declarations without relabeling unknown backends synthetic", () => {
  expect(c4Supported(vmMission)).toBe(true);
  expect(isModelBackedC4(vmMission)).toBe(true);
  expect(c4ControlSource(vmMission)).toBe("embedded_vm_c4_control");
  expect(
    c4ControlSource({
      ...vmMission,
      spec: { ...vmMission.spec, c4: { ...profile, mode: "future" } },
    }),
  ).toBeNull();
});

it.each([
  { host_platform: "darwin/amd64" },
  { guest_platform: "linux/amd64" },
  { schema_version: "1" },
  { protocol: "future" },
  { policy_version: "future" },
  { manifest_digest: "1".repeat(63) },
  { runtime_digest: 2 },
  { kernel_digest: null },
  { rootfs_digest: "G".repeat(64) },
  { verifier_digest: undefined },
])("rejects malformed VM identity %j without coercion", (change) => {
  const invalid = { ...profile, embedded_vm: { ...identity, ...change } };
  expect(
    c4Supported({
      ...vmMission,
      spec: { ...vmMission.spec, c4: invalid as typeof profile },
    }),
  ).toBe(false);
  expect(
    parseCodingTaskCapability({ ...metadata, profile: invalid }),
  ).toBeNull();
});

it("rejects mixed authority and mismatched backend metadata", () => {
  for (const change of [
    { docker_path: "/usr/bin/docker" },
    { endpoint: "unix:///run/docker.sock" },
    { image_id: docker.image_id },
  ]) {
    expect(
      c4Supported({
        ...vmMission,
        spec: { ...vmMission.spec, c4: { ...profile, ...change } },
      }),
    ).toBe(false);
  }
  expect(
    parseCodingTaskCapability({
      ...metadata,
      backends: [{ ...metadata.backends[0], profile: docker }],
    }),
  ).toBeNull();
  expect(
    parseCodingTaskCapability({ ...metadata, default_backend: "future" }),
  ).toBeNull();
});

function Setup({
  selected,
}: {
  selected: (s: CodingTaskSelection | null) => void;
}) {
  const [requested, setRequested] = useState(false);
  const [value, setValue] = useState<CodingTaskSelection | null>(null);
  const change = useCallback(
    (s: CodingTaskSelection | null) => {
      setValue(s);
      selected(s);
    },
    [selected],
  );
  return (
    <CodingTaskSetup
      disabled={false}
      requested={requested}
      onRequestedChange={setRequested}
      value={value}
      onChange={change}
    />
  );
}

it("defaults to VM but emits its unchanged identity only after explicit enable; switching Docker is explicit", async () => {
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(response(metadata));
  const selected = vi.fn();
  render(<Setup selected={selected} />);
  const backend = await screen.findByRole("combobox", { name: "测试沙箱后端" });
  expect(backend).toHaveValue("embedded_vm");
  expect(screen.getByRole("checkbox")).not.toBeChecked();
  expect(selected.mock.calls.every(([s]) => s === null)).toBe(true);
  expect(screen.getByText(identity.manifest_digest)).toBeVisible();
  fireEvent.click(screen.getByRole("checkbox"));
  await waitFor(() =>
    expect(selected).toHaveBeenLastCalledWith(
      expect.objectContaining({ profile }),
    ),
  );
  fireEvent.change(backend, { target: { value: "docker" } });
  await waitFor(() =>
    expect(selected).toHaveBeenLastCalledWith(
      expect.objectContaining({ profile: docker }),
    ),
  );
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("does not auto-fallback when VM assets are absent but allows a user to select Docker", async () => {
  const absent = { ...profile, platform: "", embedded_vm: undefined };
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    response({
      ...metadata,
      available: false,
      reason: "embedded_vm_bundle_unavailable",
      profile: absent,
      backends: [
        { ...metadata.backends[0], available: false, profile: absent },
        metadata.backends[1],
      ],
    }),
  );
  const selected = vi.fn();
  render(<Setup selected={selected} />);
  const backend = await screen.findByRole("combobox", { name: "测试沙箱后端" });
  expect(backend).toHaveValue("embedded_vm");
  expect(screen.getByRole("checkbox")).toBeDisabled();
  fireEvent.change(backend, { target: { value: "docker" } });
  expect(screen.getByRole("checkbox")).not.toBeChecked();
  fireEvent.click(screen.getByRole("checkbox"));
  await waitFor(() =>
    expect(selected).toHaveBeenLastCalledWith(
      expect.objectContaining({ profile: docker }),
    ),
  );
});

it("shows VM identity and exact VM instance labels without claiming a model account was used", () => {
  render(<C4Verification mission={vmMission} />);
  expect(screen.getByText(identity.manifest_digest)).toBeVisible();
  expect(screen.getByText("VM 实例声明 / 精确 ID")).toBeVisible();
  expect(screen.queryByText("容器声明 / 精确 ID")).not.toBeInTheDocument();
  expect(screen.getByText(/本地模型协议夹具不代表真实账户调用/)).toBeVisible();
});

it("permits explicit VM readiness preparation with real execution metadata but never executes on render", async () => {
  const m = { ...vmMission, c4_operations: [], c4_controls: [] };
  vi.spyOn(api, "c4").mockResolvedValue({
    mission_id: m.id,
    revision: m.revision,
    profile,
    operations: [],
    ...vmStatusContract,
    control: {
      ...vmStatusContract.control,
      activation_digest: "f".repeat(64),
      expires_at: new Date(Date.now() + 600000).toISOString(),
      expired: false,
    },
  });
  const post = vi.spyOn(api, "c4Action").mockResolvedValue({ mission: m });
  render(<C4Decision mission={m} busy={false} onUpdate={vi.fn()} />);
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "准备就绪材料" })).toBeEnabled(),
  );
  expect(post).not.toHaveBeenCalled();
  expect(screen.getByRole("button", { name: "执行就绪检查" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "准备就绪材料" }));
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(m.id, {
      action: "prepare_readiness",
      revision: m.revision,
    }),
  );
});

function boundVMFixture() {
  const control: C4Control = {
    schema_version: 1,
    id: "ready",
    kind: "readiness",
    mission_id: vmMission.id,
    epoch: vmMission.epoch,
    plan_revision: 0,
    base_commit: vmMission.base_commit,
    base_digest: vmMission.repository_import!.base_digest,
    profile_digest: c4Operation.profile_digest,
    activation_digest: "f".repeat(64),
    activation_expires_at: "2026-10-23T10:00:00Z",
    source_kind: "embedded_vm_c4_control",
    prepared_revision: 1,
    prepared_at: c4Operation.prepared_at,
    expires_at: "2026-10-23T10:00:00Z",
    material_digest: "6".repeat(64),
    state: "completed",
    approval: { ...c4Operation.approval!, digest: "6".repeat(64) },
    dispatch: c4Operation.dispatch,
    result: { ...c4Operation.result!, status: "ready" },
  };
  const operation = {
    ...c4Operation,
    readiness_id: "ready",
    readiness_digest: control.material_digest,
  };
  return {
    ...vmMission,
    c4_controls: [control],
    c4_operations: [operation],
  };
}

it("distinguishes VM bound success from synthetic and requires exact approval, cleanup and instance identity", () => {
  const m = boundVMFixture();
  const operation = m.c4_operations[0];
  const control = m.c4_controls[0];
  expect(c4BaselinePassed(m)).toBe(true);
  expect(c4OperationLabel(m, operation)).toBe(
    "内置 VM 验证通过 · 已记录隔离执行",
  );
  for (const change of [
    { approval: undefined },
    { cleanup: [] },
    { result: { ...operation.result!, tests: 0 } },
    {
      container_id: "process-123",
      cleanup: [{ ...operation.cleanup![0], container_id: "process-123" }],
    },
  ]) {
    expect(
      c4BaselinePassed({ ...m, c4_operations: [{ ...operation, ...change }] }),
    ).toBe(false);
  }
  expect(
    c4BaselinePassed({
      ...m,
      c4_controls: [{ ...control, approval: undefined }],
    }),
  ).toBe(false);
  expect(
    c4BaselinePassed({
      ...m,
      c4_controls: [{ ...control, source_kind: "docker_c4_control" }],
    }),
  ).toBe(false);
});

it("never promotes truncated VM baseline or candidate facts to green", () => {
  const m = boundVMFixture();
  const baseline = m.c4_operations[0];
  const candidate = {
    ...c4CandidateOperation,
    readiness_id: baseline.readiness_id,
    readiness_digest: baseline.readiness_digest,
  };
  const current = {
    ...m,
    candidate_digest: candidate.candidate_digest!,
    candidate_generation: candidate.candidate_generation!,
    approvals: [
      ...m.approvals,
      {
        kind: "execute",
        plan_revision: candidate.plan_revision,
        digest: candidate.approval!.digest,
        revision: candidate.approval!.revision,
        created_at: candidate.approval!.at,
      },
    ],
    c4_operations: [baseline, candidate],
  };
  expect(c4CandidatePassed(current)).toBe(true);
  const truncatedBaseline = {
    ...baseline,
    result: { ...baseline.result!, truncated: true },
  };
  const truncatedCandidate = {
    ...candidate,
    result: { ...candidate.result!, truncated: true },
  };
  expect(
    c4BaselinePassed({ ...current, c4_operations: [truncatedBaseline] }),
  ).toBe(false);
  expect(
    c4CandidatePassed({
      ...current,
      c4_operations: [baseline, truncatedCandidate],
    }),
  ).toBe(false);
  expect(c4OperationLabel(current, truncatedBaseline)).not.toContain(
    "验证通过",
  );
  expect(c4OperationLabel(current, truncatedCandidate)).not.toContain(
    "验证通过",
  );
});

it.each([
  ["manifest_digest", "6".repeat(64)],
  ["runtime_digest", "6".repeat(64)],
  ["kernel_digest", "6".repeat(64)],
  ["rootfs_digest", "6".repeat(64)],
  ["verifier_digest", "6".repeat(64)],
  ["policy_version", "future"],
  ["protocol", "future"],
  ["host_platform", "windows/amd64"],
  ["guest_platform", "linux/amd64"],
])(
  "rejects old VM success and readiness after current identity %s changes",
  (key, value) => {
    const m = boundVMFixture();
    const control = m.c4_controls[0];
    control.result = {
      ...control.result!,
      valid_until: "2026-10-23T10:00:00Z",
    };
    const status = {
      ...vmStatusContract.control,
      activation_digest: control.activation_digest,
      expires_at: control.activation_expires_at,
    };
    expect(
      c4ReadinessFresh(m, Date.parse("2026-10-23T09:00:00Z"), status),
    ).toBe(true);
    const changed = {
      ...m,
      spec: {
        ...m.spec,
        c4: { ...profile, embedded_vm: { ...identity, [key]: value } },
      },
    };
    expect(c4BaselinePassed(changed)).toBe(false);
    expect(
      c4ReadinessFresh(changed, Date.parse("2026-10-23T09:00:00Z"), status),
    ).toBe(false);
    expect(c4OperationLabel(changed, m.c4_operations[0])).not.toContain(
      "验证通过",
    );
  },
);

it.each([
  undefined,
  null,
  {},
  {
    revision: c4Mission.revision + 1,
    profile,
    digest: c4Operation.profile_digest,
  },
  { revision: c4Mission.revision, profile, digest: "malformed" },
  { revision: c4Mission.revision, profile, digest: "9".repeat(64) },
])("requires exact response binding for VM historical facts: %j", (binding) => {
  const m = {
    ...boundVMFixture(),
    c4_profile_binding: binding,
  } as unknown as typeof vmMission;
  expect(c4BaselinePassed(m)).toBe(false);
});

it("names the VM in decision material without claiming local or real account model evidence from the profile", () => {
  const material = decisionMaterial({
    ...vmMission,
    state: "draft",
    c4_operations: [],
  });
  const source = material.facts.find((f) => f.label === "来源")!.value;
  expect(source).toContain("内置 VM");
  expect(source).toContain("模型调用回执");
  expect(source).not.toContain("synthetic_stub");
});

it("rejects malformed profile-binding types without throwing or treating them as authority", () => {
  const m = {
    ...boundVMFixture(),
    c4_profile_binding: {
      revision: vmMission.revision,
      digest: c4Operation.profile_digest,
      profile: { ...docker, endpoint: 42 },
    },
  } as unknown as typeof vmMission;
  expect(c4BaselinePassed(m)).toBe(false);
});

it("accepts Windows amd64 VM identity only with its matching guest platform", () => {
  const windows = {
    ...profile,
    platform: "linux/amd64",
    embedded_vm: {
      ...identity,
      host_platform: "windows/amd64",
      guest_platform: "linux/amd64",
    },
  };
  expect(
    c4Supported({ ...vmMission, spec: { ...vmMission.spec, c4: windows } }),
  ).toBe(true);
});

it("preserves explicit Docker choice across metadata refresh instead of silently changing backend", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
    response(metadata),
  );
  const selected = vi.fn();
  render(<Setup selected={selected} />);
  const backend = await screen.findByRole("combobox", { name: "测试沙箱后端" });
  fireEvent.change(backend, { target: { value: "docker" } });
  fireEvent.click(screen.getByRole("checkbox"));
  await waitFor(() =>
    expect(selected).toHaveBeenLastCalledWith(
      expect.objectContaining({ profile: docker }),
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "刷新编码能力" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "刷新编码能力" })).toBeEnabled(),
  );
  expect(backend).toHaveValue("docker");
  expect(selected).toHaveBeenLastCalledWith(
    expect.objectContaining({ profile: docker }),
  );
});

it("renders malformed persisted VM identity as unavailable, never as validated runtime facts", () => {
  render(
    <C4Verification
      mission={{
        ...vmMission,
        spec: {
          ...vmMission.spec,
          c4: {
            ...profile,
            embedded_vm: { ...identity, manifest_digest: "not-a-digest" },
          },
        },
      }}
    />,
  );
  expect(screen.getByText(/内置 VM 资源未安装或未验证/)).toBeVisible();
  expect(screen.queryByLabelText("冻结 VM 身份")).not.toBeInTheDocument();
});
