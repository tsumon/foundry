import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import { DesktopSubscription } from "./DesktopSubscription";
import { desktopSubscriptionApi } from "../desktopSubscriptionApi";
import { api } from "../api";
import type { ModelAccount, ModelRoute } from "../types";
vi.mock("../desktopSubscriptionApi", () => ({
  desktopSubscriptionApi: { read: vi.fn(), prepare: vi.fn() },
}));
vi.mock("../api", () => ({
  api: { saveProfile: vi.fn() },
  APIError: class extends Error {},
}));
const account = {
  id: "a",
  revision: 1,
  name: "Synthetic",
  provider: "openai",
  base_url: "https://api.openai.com/v1",
  auth_method: "openai_siwc",
  billing_mode: "subscription",
  enabled: true,
  current_credential_version: 0,
} as ModelAccount;
const route = {
  id: "r",
  account_id: "a",
  adapter: "openai_siwc_responses",
  protocol: "responses",
  base_url: account.base_url,
  environment: "live",
  spend_class: "subscription_only",
  extra_usage: "off",
} as ModelRoute;
const closed = {
  preparation: {
    phase: "unprepared",
    connection_ready: false,
    catalog_ready: false,
  },
  catalog_choices: [],
};
afterEach(() => vi.resetAllMocks());
describe("desktop subscription preparation", () => {
  it("explicitly prepares the independent diagnostic gate without dispatch", async () => {
    vi.mocked(desktopSubscriptionApi.read).mockResolvedValue(closed);
    vi.mocked(desktopSubscriptionApi.prepare).mockResolvedValue({
      ...closed,
      preparation: {
        ...closed.preparation,
        phase: "inference",
        route_id: "r",
        inference_ready: true,
      },
    } as never);
    const prepared = vi.fn();
    render(
      <DesktopSubscription
        account={account}
        routes={[route]}
        onPrepared={prepared}
        onProfileSaved={() => {}}
      />,
    );
    await screen.findByText(/尚未准备/);
    fireEvent.change(screen.getByLabelText("订阅准备路由"), {
      target: { value: "r" },
    });
    fireEvent.click(screen.getByRole("button", { name: "独立准备单次诊断" }));
    await screen.findByText(/单次诊断准备就绪/);
    expect(desktopSubscriptionApi.prepare).toHaveBeenCalledExactlyOnceWith(
      "a",
      "inference",
      "r",
      1,
    );
    expect(prepared).toHaveBeenCalledOnce();
    expect(api.saveProfile).not.toHaveBeenCalled();
  });
  it("reads metadata only; preparation does not approve, execute or open a browser", async () => {
    vi.mocked(desktopSubscriptionApi.read).mockResolvedValue(closed);
    vi.mocked(desktopSubscriptionApi.prepare).mockResolvedValue({
      ...closed,
      preparation: {
        phase: "connection",
        route_id: "r",
        connection_ready: true,
        catalog_ready: false,
      },
    });
    const prepared = vi.fn();
    render(
      <DesktopSubscription
        account={account}
        routes={[route]}
        onPrepared={prepared}
        onProfileSaved={() => {}}
      />,
    );
    await screen.findByText(/尚未准备/);
    expect(desktopSubscriptionApi.prepare).not.toHaveBeenCalled();
    expect(api.saveProfile).not.toHaveBeenCalled();
    expect(screen.getByText(/Foundry 专属合成凭据/)).toBeVisible();
    fireEvent.change(screen.getByLabelText("订阅准备路由"), {
      target: { value: "r" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "准备连接（本机检查）" }),
    );
    await screen.findByText(/连接准备就绪/);
    expect(desktopSubscriptionApi.prepare).toHaveBeenCalledWith(
      "a",
      "connection",
      "r",
      1,
    );
    expect(prepared).toHaveBeenCalledOnce();
    expect(api.saveProfile).not.toHaveBeenCalled();
  });
  it.each([
    ["diagnostic_failed", "检查失败"],
    ["readiness_expired", "准备已过期"],
    ["authorization_expired", "授权已过期"],
  ])("shows stopped %s without retry", async (blocker, label) => {
    vi.mocked(desktopSubscriptionApi.read).mockResolvedValue({
      ...closed,
      preparation: { ...closed.preparation, blocker },
    });
    render(
      <DesktopSubscription
        account={account}
        routes={[route]}
        onPrepared={() => {}}
        onProfileSaved={() => {}}
      />,
    );
    await screen.findByText(new RegExp(label));
    expect(desktopSubscriptionApi.prepare).not.toHaveBeenCalled();
  });
  it("locks unknown preparation until explicit metadata read", async () => {
    vi.mocked(desktopSubscriptionApi.read).mockResolvedValue(closed);
    vi.mocked(desktopSubscriptionApi.prepare).mockRejectedValue(
      Error("private detail must not render"),
    );
    render(
      <DesktopSubscription
        account={account}
        routes={[route]}
        onPrepared={() => {}}
        onProfileSaved={() => {}}
      />,
    );
    await screen.findByText(/尚未准备/);
    fireEvent.change(screen.getByLabelText("订阅准备路由"), {
      target: { value: "r" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "准备连接（本机检查）" }),
    );
    await screen.findByText(/结果未知/);
    expect(
      screen.getByRole("button", { name: "准备连接（本机检查）" }),
    ).toBeDisabled();
    expect(screen.queryByText(/private detail/)).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: "读取准备与本地目录证据" }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "准备连接（本机检查）" }),
      ).toBeEnabled(),
    );
    expect(desktopSubscriptionApi.prepare).toHaveBeenCalledOnce();
  });
  it("only explicit current catalog choice saves a future-task profile", async () => {
    vi.mocked(desktopSubscriptionApi.read).mockResolvedValue({
      ...closed,
      catalog_choices: [
        {
          operation_id: "cat",
          route_id: "r",
          model: "gpt-5.1",
          display_name: "Synthetic model",
          expires_at: "2099-01-01T00:00:00Z",
        },
      ],
    });
    vi.mocked(api.saveProfile).mockResolvedValue({
      profile: { id: "p" } as never,
    });
    const saved = vi.fn();
    render(
      <DesktopSubscription
        account={account}
        routes={[route]}
        onPrepared={() => {}}
        onProfileSaved={saved}
      />,
    );
    await screen.findByRole("option", { name: /Synthetic model/ });
    expect(api.saveProfile).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText("目录模型选择"), {
      target: { value: "cat:gpt-5.1" },
    });
    fireEvent.change(screen.getByLabelText("新订阅连接名称"), {
      target: { value: "Future task" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "保存为未来任务的模型连接" }),
    );
    await waitFor(() => expect(saved).toHaveBeenCalledOnce());
    expect(api.saveProfile).toHaveBeenCalledWith(
      expect.objectContaining({
        account_id: "a",
        route_id: "r",
        catalog_operation_id: "cat",
        model: "gpt-5.1",
        name: "Future task",
      }),
    );
  });
});
