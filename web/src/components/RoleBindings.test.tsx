import { render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { RoleBindings } from "./RoleBindings";
import type { ModelAccount, Profile } from "../types";

it("catalog-bound roles describe backend authorization recheck without claiming quota", () => {
  const account = {
    id: "a",
    auth_method: "openai_siwc",
    billing_mode: "subscription",
    name: "Synthetic",
  } as ModelAccount;
  const profile = {
    id: "p",
    name: "Catalog model",
    account_id: "a",
    catalog_operation_id: "catalog-current",
  } as Profile;
  render(
    <RoleBindings
      profiles={[profile]}
      accounts={[account]}
      settings={{ default_profile_id: "p", role_bindings: {} }}
      disabled={false}
      onChange={vi.fn()}
      onSave={vi.fn()}
    />,
  );
  const row = screen.getByRole("row", { name: /Worker/ });
  expect(row).toHaveTextContent("授权有效性由后端复核");
  expect(row).toHaveTextContent("额度未知");
  expect(row).not.toHaveTextContent("身份未验证");
});

it("labels every unresolved role as unconfigured instead of a legacy connection", () => {
  render(
    <RoleBindings
      profiles={[]}
      accounts={[]}
      settings={{ default_profile_id: "", role_bindings: {} }}
      disabled={false}
      onChange={vi.fn()}
      onSave={vi.fn()}
    />,
  );
  const rows = screen.getAllByRole("row").slice(1);
  expect(rows).toHaveLength(7);
  for (const row of rows) {
    expect(within(row).getByText("未配置")).toBeVisible();
    expect(within(row).queryByText("旧式连接")).not.toBeInTheDocument();
  }
});

it("shows key versions exactly but labels SIWC roles by stable authorization binding", () => {
  const keyAccount = {
    id: "key-account",
    name: "Key account",
    auth_method: "api_key",
    current_credential_version: 4,
  } as ModelAccount;
  const oauthAccount = {
    id: "oauth-account",
    name: "OpenAI subscription",
    auth_method: "openai_siwc",
    current_credential_version: 0,
  } as ModelAccount;
  const keyProfile = {
    id: "key-profile",
    name: "Key profile",
    account_id: keyAccount.id,
  } as Profile;
  const oauthProfile = {
    id: "oauth-profile",
    name: "SIWC profile",
    account_id: oauthAccount.id,
    route_id: "siwc-route",
  } as Profile;
  render(
    <RoleBindings
      profiles={[keyProfile, oauthProfile]}
      accounts={[keyAccount, oauthAccount]}
      settings={{
        default_profile_id: keyProfile.id,
        role_bindings: { worker: oauthProfile.id },
      }}
      disabled={false}
      onChange={vi.fn()}
      onSave={vi.fn()}
    />,
  );

  const main = screen.getByRole("row", { name: /Main/ });
  expect(main).toHaveTextContent("版本 4");
  expect(main).toHaveTextContent("真实调用未开放");
  const worker = screen.getByRole("row", { name: /Worker/ });
  expect(worker).toHaveTextContent("OpenAI SIWC 稳定授权（可用性由后端复核）");
  expect(within(worker).queryByText(/版本 0/)).not.toBeInTheDocument();
  expect(within(worker).queryByText(/未配置/)).not.toBeInTheDocument();
});

it("labels live coding role transport as independently approved without enabling edits", () => {
  const account = {
    id: "live-account",
    name: "Live account",
    auth_method: "api_key",
    current_credential_version: 2,
    billing_mode: "api_credits",
  } as ModelAccount;
  const liveProfile = {
    id: "live-profile",
    name: "Live profile",
    account_id: account.id,
  } as Profile;
  render(
    <RoleBindings
      profiles={[liveProfile]}
      accounts={[account]}
      settings={{
        default_profile_id: liveProfile.id,
        role_bindings: {},
      }}
      liveTaskInference
      disabled
      onChange={vi.fn()}
      onSave={vi.fn()}
    />,
  );

  const main = screen.getByRole("row", { name: /Main/ });
  expect(main).toHaveTextContent("仅受支持的账户编码任务按独立审批");
  expect(main).not.toHaveTextContent("真实调用未开放");
  expect(within(main).getByRole("combobox")).toBeDisabled();
  expect(screen.getByRole("button", { name: "保存角色分配" })).toBeDisabled();
});
