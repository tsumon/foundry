import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useCallback, useState } from "react";
import { expect, it, vi } from "vitest";
import { CodingTaskSetup, type CodingTaskSelection } from "./CodingTaskSetup";
import { response } from "../test/fixtures";

const capability = {
  enabled: true,
  available: true,
  reason: "",
  profile: {
    schema_version: 1,
    control_version: 1,
    id: "c4_go_model_v1",
    mode: "docker",
    platform: "linux/arm64",
    verifier: "/opt/foundry/verify-go-v1",
    docker_path: "/usr/local/bin/docker",
    endpoint: "unix:///var/run/docker.sock",
    image_id: "",
  },
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

function SetupHarness({
  onSelection,
}: {
  onSelection: (selection: CodingTaskSelection | null) => void;
}) {
  const [requested, setRequested] = useState(false);
  const [selection, setSelection] = useState<CodingTaskSelection | null>(null);
  const changeSelection = useCallback(
    (next: CodingTaskSelection | null) => {
      setSelection(next);
      onSelection(next);
    },
    [onSelection],
  );
  return (
    <CodingTaskSetup
      disabled={false}
      requested={requested}
      onRequestedChange={setRequested}
      value={selection}
      onChange={changeSelection}
    />
  );
}

it("keeps model-backed coding default-closed when metadata is missing and never retries automatically", async () => {
  const fetcher = vi.fn(async () =>
    response({ error: { message: "route missing" } }, 404),
  );
  vi.stubGlobal("fetch", fetcher);

  render(
    <CodingTaskSetup
      disabled={false}
      requested={false}
      onRequestedChange={vi.fn()}
      value={null}
      onChange={vi.fn()}
    />,
  );

  expect(await screen.findByText(/能力元数据不可用.*保持关闭/)).toBeVisible();
  expect(screen.getByLabelText(/启用账户模型与受控 Docker/)).toBeDisabled();
  expect(fetcher).toHaveBeenCalledTimes(1);
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("shows fixed policy and independent approvals without treating available metadata as authorization", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(capability)),
  );

  render(
    <CodingTaskSetup
      disabled={false}
      requested={false}
      onRequestedChange={vi.fn()}
      value={null}
      onChange={vi.fn()}
    />,
  );

  const setup = await screen.findByRole("region", {
    name: "账户模型编码设置",
  });
  expect(setup).toHaveTextContent("c4_go_model_v1");
  expect(setup).toHaveTextContent("openai");
  expect(setup).toHaveTextContent("最多 16 次模型调用");
  expect(setup).toHaveTextContent("最多 2 轮修复");
  expect(setup).toHaveTextContent("go test -json -count=1 -timeout=90s ./...");
  expect(setup).toHaveTextContent(
    /readiness.*baseline.*context.*execute.*delivery/,
  );
  expect(setup).toHaveTextContent(/可用不等于已授权/);
  expect(screen.getByLabelText(/启用账户模型与受控 Docker/)).not.toBeChecked();
});

it("requires an immutable image and emits only an explicit complete selection", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => response(capability)),
  );
  const onChange = vi.fn();
  render(<SetupHarness onSelection={onChange} />);

  fireEvent.click(await screen.findByLabelText(/启用账户模型与受控 Docker/));
  expect(screen.getByText(/尚未填写不可变镜像 ID/)).toBeVisible();
  expect(onChange).toHaveBeenLastCalledWith(null);

  fireEvent.change(screen.getByLabelText("不可变本地镜像 ID"), {
    target: { value: `sha256:${"a".repeat(64)}` },
  });
  await waitFor(() =>
    expect(onChange).toHaveBeenLastCalledWith({
      capability,
      profile: {
        ...capability.profile,
        image_id: `sha256:${"a".repeat(64)}`,
      },
    }),
  );
  fireEvent.change(screen.getByLabelText("Docker endpoint"), {
    target: { value: "tcp://127.0.0.1:2375" },
  });
  await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(null));
});

it("invalidates an enabled draft but preserves explicit intent when refreshed metadata closes", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(response(capability))
    .mockResolvedValueOnce(
      response({
        ...capability,
        available: false,
        reason: "activation expired",
      }),
    );
  vi.stubGlobal("fetch", fetcher);
  const onChange = vi.fn();
  render(<SetupHarness onSelection={onChange} />);

  fireEvent.click(await screen.findByLabelText(/启用账户模型与受控 Docker/));
  fireEvent.change(screen.getByLabelText("不可变本地镜像 ID"), {
    target: { value: `sha256:${"b".repeat(64)}` },
  });
  await waitFor(() => expect(onChange.mock.calls.at(-1)?.[0]).not.toBeNull());

  fireEvent.click(screen.getByRole("button", { name: "刷新编码能力" }));
  expect(await screen.findByText(/activation expired/)).toBeVisible();
  expect(onChange).toHaveBeenLastCalledWith(null);
  expect(screen.getByLabelText(/启用账户模型与受控 Docker/)).toBeChecked();
  expect(screen.getByLabelText(/启用账户模型与受控 Docker/)).toBeEnabled();
});
