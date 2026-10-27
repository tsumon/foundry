import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";
import { SessionRenewal } from "./SessionRenewal";
import { sessionRenewalApi } from "../sessionRenewalApi";
import type { ModelAccount } from "../types";
vi.mock("../sessionRenewalApi", () => ({
  sessionRenewalApi: { read: vi.fn(), mutate: vi.fn() },
}));
const account = { id: "a", revision: 1, enabled: true } as ModelAccount;
const disabled = {
  account_id: "a",
  account_revision: 1,
  revision: 0,
  enabled: false,
  registration_retained: true,
  access_state: "expired",
  state: "disabled",
};
afterEach(() => vi.resetAllMocks());
describe("independent session renewal", () => {
  it("distinguishes unsent local credential failure without requesting another login or retry", async () => {
    vi.mocked(sessionRenewalApi.read).mockResolvedValue({
      ...disabled, enabled: true, state: "recovery_required",
      receipt: { old_generation: 1, new_generation: 0,
        stage: "recovery_required", category: "credential_preflight", certainty: "not_sent" },
    });
    render(<SessionRenewal account={account} />);
    await screen.findByText(/本地凭据检查未完成.*未发送/);
    expect(screen.queryByText(/需要显式恢复或重新登录/)).not.toBeInTheDocument();
    expect(sessionRenewalApi.mutate).not.toHaveBeenCalled();
  });
  it("closes mutations after failed explicit reread until a successful reread", async () => {
    vi.mocked(sessionRenewalApi.read)
      .mockResolvedValueOnce(disabled)
      .mockRejectedValueOnce(Error("read unavailable"))
      .mockResolvedValueOnce(disabled);
    render(<SessionRenewal account={account} />);
    await screen.findByText(/续期已关闭/);
    expect(screen.getByRole("button", { name: "准备续期材料" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "读取续期状态" }));
    await screen.findByRole("alert");
    for (const name of ["准备续期材料", "批准并开启续期", "关闭续期"]) {
      expect(screen.getByRole("button", { name })).toBeDisabled();
    }
    fireEvent.click(screen.getByRole("button", { name: "准备续期材料" }));
    expect(sessionRenewalApi.mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "读取续期状态" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "准备续期材料" }),
      ).toBeEnabled(),
    );
    expect(sessionRenewalApi.mutate).not.toHaveBeenCalled();
  });
  it("mounts metadata only and requires separate prepare and approval", async () => {
    vi.mocked(sessionRenewalApi.read).mockResolvedValue(disabled);
    vi.mocked(sessionRenewalApi.mutate).mockResolvedValue({
      ...disabled,
      revision: 1,
      material: {
        id: "m",
        revision: 1,
        account_id: "a",
        account_revision: 1,
        expires_at: "2099-01-01T00:00:00Z",
        binding: {
          id: "grant",
          account_id: "a",
          issuer: "https://auth.openai.com",
          subject: "synthetic",
          client_id: "oaiapp_synthetic",
          scopes: ["offline_access"],
          spend_class: "subscription_only",
        },
        resource: "https://api.openai.com/v1",
        token_endpoint: "https://auth.openai.com/api/accounts/oauth/token",
        credential_store: "system credential store",
        approval_scope: "Credential renewal only",
        schedule: "30 seconds",
        failure_behavior: "No retry",
      },
    });
    render(<SessionRenewal account={account} />);
    await screen.findByText(/续期已关闭/);
    expect(sessionRenewalApi.mutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "准备续期材料" }));
    await screen.findByText("Credential renewal only");
    expect(sessionRenewalApi.mutate).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "批准并开启续期" }));
    await waitFor(() =>
      expect(sessionRenewalApi.mutate).toHaveBeenCalledWith("a", {
        action: "approve",
        material_id: "m",
        expected_revision: 1,
      }),
    );
  });
  it("does not retry rejected or unknown POST", async () => {
    vi.mocked(sessionRenewalApi.read).mockResolvedValue(disabled);
    vi.mocked(sessionRenewalApi.mutate).mockRejectedValue(Error("unknown"));
    render(<SessionRenewal account={account} />);
    await screen.findByText(/续期已关闭/);
    fireEvent.click(screen.getByRole("button", { name: "准备续期材料" }));
    await screen.findByRole("alert");
    expect(sessionRenewalApi.mutate).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "准备续期材料" })).toBeDisabled();
  });
  it("ignores stale results after account switching", async () => {
    let finish!: (value: typeof disabled) => void;
    vi.mocked(sessionRenewalApi.read)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValue({
        ...disabled,
        account_id: "b",
        registration_retained: false,
      });
    const view = render(<SessionRenewal account={account} />);
    view.rerender(<SessionRenewal account={{ ...account, id: "b" }} />);
    await screen.findByText(/注册尚不可用/);
    finish({ ...disabled, enabled: true, state: "enabled" });
    await waitFor(() =>
      expect(screen.queryByText(/续期已开启/)).not.toBeInTheDocument(),
    );
    expect(sessionRenewalApi.mutate).not.toHaveBeenCalled();
  });
});
