import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CreateMission } from "./CreateMission";
import { mission, profile, response } from "../test/fixtures";
import {
  admissionV2,
  capabilityV2,
  receiptV2,
  sealReceipt,
  testCrypto,
} from "../test/codingV2Fixtures";
function setup(
  options: {
    admit?: (body: any) => Promise<Response>;
    inspect?: (body: any) => Promise<Response>;
    catalog?: () => unknown;
  } = {},
) {
  vi.stubGlobal("crypto", testCrypto);
  const requests: Array<{ path: string; body: any }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const body = init?.body ? JSON.parse(String(init.body)) : null;
      if (init?.method === "POST") requests.push({ path, body });
      if (path === "/api/profiles")
        return response({
          profiles: [profile],
          settings: { default_profile_id: "p1", role_bindings: {} },
        });
      if (path === "/api/health") return response({ capabilities: {} });
      if (path === "/api/role-model-policy") return response({ state: {} });
      if (path === "/api/capabilities") return response({ repositories: [] });
      if (path === "/api/coding-tasks" && !init?.method)
        return response(options.catalog?.() ?? capabilityV2);
      if (path === "/api/coding-dependencies")
        return (
          options.admit?.(body) ??
          response(
            admissionV2(
              body.profile_id === "c4_python_model_v2"
                ? 2
                : body.profile_id === "c4_typescript_model_v2"
                  ? 1
                  : 0,
            ),
          )
        );
      if (path === "/api/repository-imports/inspections") {
        if (!init?.method) return response([]);
        if (options.inspect) return options.inspect(body);
        const index =
          body.spec.verification_v2.profile_id === "c4_python_model_v2"
            ? 2
            : body.spec.verification_v2.profile_id === "c4_typescript_model_v2"
              ? 1
              : 0;
        const r = receiptV2(
          index,
          body.spec.verification_v2.test_support_prefixes,
        );
        return response(sealReceipt({ ...r, spec: body.spec }));
      }
      if (path === "/api/repository-imports/operations") return response([]);
      if (path === "/api/coding-tasks" && init?.method === "POST")
        return response({ mission });
      throw new Error(`Forbidden fixture endpoint ${path}`);
    }),
  );
  const created = vi.fn();
  render(<CreateMission onCancel={() => {}} onCreated={created} />);
  return { requests, created };
}
async function selectLanguage(index = 0) {
  await screen.findByRole("option", { name: "测试连接 · fixture-model" });
  for (const [label, value] of [
    ["任务标题", "Synthetic v2"],
    ["交付目标", "Update source"],
    ["Git 仓库绝对路径", "/tmp/source"],
    ["允许修改范围（每行一项）", "src/"],
  ])
    fireEvent.change(screen.getByLabelText(label), { target: { value } });
  fireEvent.change(await screen.findByLabelText("语言 / 固定测试配置"), {
    target: { value: capabilityV2.profiles[index].profile_id },
  });
  fireEvent.click(screen.getByLabelText(/启用账户模型与内置 VM/));
  fireEvent.change(screen.getByLabelText("本地依赖输入目录"), {
    target: { value: "/synthetic/dependencies" },
  });
}
async function admit() {
  fireEvent.click(
    screen.getByLabelText(/同意本次有界本地依赖读取、复制与存储/),
  );
  fireEvent.click(screen.getByRole("button", { name: "导入本地依赖" }));
  await screen.findByText("已保留依赖准入结果");
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "检查可导入性" })).toBeEnabled(),
  );
}
const createButton = () =>
  screen.getByRole("button", { name: "创建任务并检查上下文" });
it.each([0, 1, 2])(
  "creates closed language %i only with separate copy consent and exact inspected receipt",
  async (index) => {
    const { requests, created } = setup();
    await selectLanguage(index);
    expect(screen.getByRole("button", { name: "导入本地依赖" })).toBeDisabled();
    expect(createButton()).toBeDisabled();
    expect(requests).toEqual([]);
    await admit();
    expect(requests).toHaveLength(1);
    expect(requests[0]).toMatchObject({
      path: "/api/coding-dependencies",
      body: {
        consent: true,
        repo_path: "/tmp/source",
        profile_id: capabilityV2.profiles[index].profile_id,
      },
    });
    expect(screen.getByText(/3 文件.*120 bytes.*1 包/)).toBeVisible();
    expect(screen.getByText(/不证明发布者真实性/)).toBeVisible();
    expect(createButton()).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
    await screen.findByText("可创建任务");
    expect(screen.getByText(/1 个测试文件/)).toBeVisible();
    expect(
      screen.queryByText("内置 VM 验证通过 · 已记录隔离执行"),
    ).not.toBeInTheDocument();
    fireEvent.click(createButton());
    await waitFor(() => expect(created).toHaveBeenCalledWith(mission));
    expect(requests.map((r) => r.path)).toEqual([
      "/api/coding-dependencies",
      "/api/repository-imports/inspections",
      "/api/coding-tasks",
    ]);
    expect(requests[1].body.spec).toMatchObject({
      image: "",
      acceptance: [capabilityV2.profiles[index].verifier],
      verification_v2: {
        source_root: ".",
        test_root: "tests",
        test_support_prefixes: [],
      },
    });
    expect(requests[2].body.spec.c4).toEqual({
      schema_version: 2,
      id: capabilityV2.profiles[index].profile_id,
      mode: "embedded_vm",
    });
    expect(requests[2].body.import_selection.digest).toMatch(/^[a-f0-9]{64}$/);
    expect(requests[2].body.spec).not.toHaveProperty("verification_v2");
    expect(requests[2].body.spec.protected).toEqual([]);
  },
);
it.each([
  "源代码根目录",
  "测试根目录",
  "测试支持目录（每行一项）",
  "本地依赖输入目录",
  "Git 仓库绝对路径",
  "允许修改范围（每行一项）",
  "受保护文件（每行一项）",
])(
  "invalidates an inspected receipt immediately after %s changes",
  async (label) => {
    setup();
    await selectLanguage();
    await admit();
    fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
    await screen.findByText("可创建任务");
    fireEvent.change(screen.getByLabelText(label), {
      target: {
        value:
          label === "源代码根目录"
            ? "src"
            : label === "测试根目录"
              ? "test"
              : "changed",
      },
    });
    expect(createButton()).toBeDisabled();
    expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
  },
);
it("discards late source inspection after a bound selection changes away and back", async () => {
  let finish!: (r: Response) => void;
  setup({
    inspect: () =>
      new Promise((r) => {
        finish = r;
      }),
  });
  await selectLanguage();
  await admit();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  fireEvent.change(screen.getByLabelText("源代码根目录"), {
    target: { value: "src" },
  });
  fireEvent.change(screen.getByLabelText("源代码根目录"), {
    target: { value: "." },
  });
  await act(async () => finish(response(receiptV2())));
  expect(createButton()).toBeDisabled();
  expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
});
it.each([false, true])(
  "never selects a failed/unknown publication admission (%s)",
  async (unknown) => {
    setup({
      admit: async () =>
        unknown
          ? response({ ...admissionV2(), admission_digest: "" })
          : response(
              {
                error: {
                  code: "invalid_dependencies",
                  message: "local dependency import refused",
                },
              },
              400,
            ),
    });
    await selectLanguage();
    await admitFailure();
    expect(createButton()).toBeDisabled();
    expect(screen.queryByText("已保留依赖准入结果")).not.toBeInTheDocument();
  },
);
async function admitFailure() {
  fireEvent.click(
    screen.getByLabelText(/同意本次有界本地依赖读取、复制与存储/),
  );
  fireEvent.click(screen.getByRole("button", { name: "导入本地依赖" }));
  await screen.findByRole("alert");
}
it("keeps selected language visible when refresh removes its runtime, without resurrecting inspection", async () => {
  let fresh = false;
  setup({
    catalog: () =>
      fresh
        ? {
            ...capabilityV2,
            profiles: capabilityV2.profiles.map((p) => ({
              ...p,
              available: false,
              reason: "embedded_vm_v2_bundle_unavailable",
              embedded_vm: undefined,
              runtime_inventory_digest: undefined,
            })),
          }
        : capabilityV2,
  });
  await selectLanguage();
  await admit();
  fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
  await screen.findByText("可创建任务");
  fresh = true;
  fireEvent.click(screen.getByRole("button", { name: "刷新编码能力" }));
  await waitFor(() =>
    expect(screen.getByLabelText("语言 / 固定测试配置")).toHaveTextContent(
      "embedded_vm_v2_bundle_unavailable",
    ),
  );
  expect(screen.getByLabelText("语言 / 固定测试配置")).toHaveValue(
    "c4_node_model_v2",
  );
  expect(createButton()).toBeDisabled();
  expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
});
it("discards late dependency import when source changes away and back", async () => {
  let finish!: (r: Response) => void;
  setup({
    admit: () =>
      new Promise((r) => {
        finish = r;
      }),
  });
  await selectLanguage();
  fireEvent.click(
    screen.getByLabelText(/同意本次有界本地依赖读取、复制与存储/),
  );
  fireEvent.click(screen.getByRole("button", { name: "导入本地依赖" }));
  fireEvent.change(screen.getByLabelText("Git 仓库绝对路径"), {
    target: { value: "/changed" },
  });
  fireEvent.change(screen.getByLabelText("Git 仓库绝对路径"), {
    target: { value: "/tmp/source" },
  });
  await act(async () => finish(response(admissionV2())));
  expect(screen.queryByText("已保留依赖准入结果")).not.toBeInTheDocument();
  expect(createButton()).toBeDisabled();
});
it.each([
  "runtime_inventory_digest",
  "manifest_digest",
  "runtime_digest",
  "kernel_digest",
  "rootfs_digest",
  "verifier_digest",
])(
  "requires a fresh admission and inspection after catalog %s changes",
  async (field) => {
    let current = structuredClone(capabilityV2);
    setup({ catalog: () => current });
    await selectLanguage();
    await admit();
    fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
    await screen.findByText("可创建任务");
    current = structuredClone(current);
    if (field === "runtime_inventory_digest")
      current.profiles[0].runtime_inventory_digest = "9".repeat(64);
    else
      (current.profiles[0].embedded_vm as unknown as Record<string, string>)[
        field
      ] = "9".repeat(64);
    fireEvent.click(screen.getByRole("button", { name: "刷新编码能力" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "刷新编码能力" }),
      ).toBeEnabled(),
    );
    expect(createButton()).toBeDisabled();
    expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
    expect(screen.queryByText("已保留依赖准入结果")).not.toBeInTheDocument();
    expect(screen.getByLabelText("语言 / 固定测试配置")).toHaveValue(
      "c4_node_model_v2",
    );
  },
);
it.each(["language", "mode"])(
  "cannot reuse a receipt after changing coding %s away and back",
  async (kind) => {
    setup();
    await selectLanguage();
    await admit();
    fireEvent.click(screen.getByRole("button", { name: "检查可导入性" }));
    await screen.findByText("可创建任务");
    if (kind === "language") {
      fireEvent.change(screen.getByLabelText("语言 / 固定测试配置"), {
        target: { value: "c4_python_model_v2" },
      });
      fireEvent.change(screen.getByLabelText("语言 / 固定测试配置"), {
        target: { value: "c4_node_model_v2" },
      });
    } else {
      fireEvent.click(screen.getByLabelText(/启用账户模型与内置 VM/));
      fireEvent.click(screen.getByLabelText(/启用账户模型与内置 VM/));
    }
    expect(createButton()).toBeDisabled();
    expect(screen.queryByText("可创建任务")).not.toBeInTheDocument();
  },
);
it("keeps v2 on VM after a previous explicit Go Docker selection and a capability refresh", async () => {
  setup();
  await screen.findByLabelText("测试沙箱后端");
  fireEvent.change(screen.getByLabelText("测试沙箱后端"), {
    target: { value: "docker" },
  });
  fireEvent.change(screen.getByLabelText("语言 / 固定测试配置"), {
    target: { value: "c4_node_model_v2" },
  });
  fireEvent.click(screen.getByRole("button", { name: "刷新编码能力" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "刷新编码能力" })).toBeEnabled(),
  );
  expect(screen.getByLabelText(/启用账户模型与内置 VM/)).toBeVisible();
  expect(screen.queryByLabelText("测试沙箱后端")).not.toBeInTheDocument();
});
it("dispatches at most one dependency copy while the explicit action is pending", async () => {
  let finish!: (r: Response) => void;
  const { requests } = setup({
    admit: () =>
      new Promise((r) => {
        finish = r;
      }),
  });
  await selectLanguage();
  fireEvent.click(
    screen.getByLabelText(/同意本次有界本地依赖读取、复制与存储/),
  );
  const button = screen.getByRole("button", { name: "导入本地依赖" });
  act(() => {
    fireEvent.click(button);
    fireEvent.click(button);
  });
  expect(
    requests.filter((r) => r.path === "/api/coding-dependencies"),
  ).toHaveLength(1);
  await act(async () => finish(response(admissionV2())));
});
