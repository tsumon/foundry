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
