import { useCallback, useEffect, useRef, useState } from "react";
import { APIError } from "../api";
import {
  accountError,
  modelAccountApi,
  type AccountCreateInput,
} from "../modelAccountApi";
import type { ModelAccount, ModelCredential, ModelRuntime } from "../types";
import { ModelAccountCredentials } from "./ModelAccountCredentials";
import { ModelAuthorizationCard } from "./ModelAuthorizationCard";
import { ModelAcceptanceCard } from "./ModelAcceptanceCard";
import { ModelAcceptanceOperations } from "./ModelAcceptanceOperations";
import { useCapabilityRequest } from "./capabilities/useCapabilityRequest";

export const billingLabels = {
  subscription: "订阅（用户声明）",
  api_credits: "API 额度（用户声明）",
  metered_api: "按量 API（用户声明）",
  unknown: "计费渠道未知",
};
const blank: AccountCreateInput = {
  name: "",
  provider: "custom",
  base_url: "",
  auth_method: "api_key",
  billing_mode: "unknown",
};
export function ModelAccounts({
  onAccounts,
  runtime,
}: {
  onAccounts: (accounts: ModelAccount[]) => void;
  runtime?: ModelRuntime;
}) {
  const [accounts, setAccounts] = useState<ModelAccount[]>([]);
  const [read, setRead] = useState(false);
  const [form, setForm] = useState(blank);
  const [selected, setSelected] = useState<ModelAccount>();
  const [credentials, setCredentials] = useState<ModelCredential[]>([]);
  const [name, setName] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [readError, setReadError] = useState("");
  const selectionEpoch = useRef(0);
  const { busy, error, setError, run } = useCapabilityRequest(true);
  const merge = (account: ModelAccount) =>
    setAccounts((old) => [account, ...old.filter((a) => a.id !== account.id)]);
  useEffect(() => {
    onAccounts(accounts);
  }, [accounts, onAccounts]);
  useEffect(() => {
    let active = true;
    void modelAccountApi
      .list()
      .then((data) => {
        if (active) {
          setAccounts(data.accounts ?? []);
          setRead(true);
        }
      })
      .catch((cause) => {
        if (active) setReadError(accountError(cause));
      });
    return () => {
      active = false;
      selectionEpoch.current++;
    };
  }, []);
  const refresh = useCallback(
    async (resetDraft = false) => {
      if (!selected) return;
      const epoch = selectionEpoch.current;
      const data = await modelAccountApi.detail(selected.id);
      if (epoch !== selectionEpoch.current) return;
      setSelected(data.account);
      setCredentials(data.credentials);
      merge(data.account);
      if (resetDraft) {
        setName(data.account.name);
        setEnabled(data.account.enabled);
      }
    },
    [selected?.id],
  );
  const open = (id: string) =>
    void run(async (valid) => {
      const epoch = ++selectionEpoch.current;
      try {
        const data = await modelAccountApi.detail(id);
        if (valid() && epoch === selectionEpoch.current) {
          setSelected(data.account);
          setCredentials(data.credentials);
          setName(data.account.name);
          setEnabled(data.account.enabled);
          merge(data.account);
        }
      } catch (cause) {
        if (valid()) setError(accountError(cause));
      }
    });
  return (
    <section className="model-accounts" aria-label="模型账户">
      <h2>模型账户</h2>
      <p className="note">
        账户只是本地配置记录：身份未验证、额度未知、真实账户调用尚未开放。厂商与计费渠道均为用户声明；不登录、不探测、不自动付费降级。
      </p>
      {(error || readError) && (
        <p role="alert" className="form-error">
          {error || readError}
        </p>
      )}
      {!read && <p>账户尚未读取，不能视为空列表。</p>}
      <button
        type="button"
        disabled={busy}
        onClick={() =>
          void run(async (valid) => {
            try {
              const data = await modelAccountApi.list();
              if (valid()) {
                setAccounts(data.accounts);
                setRead(true);
                setReadError("");
              }
            } catch (cause) {
              if (valid()) setError(accountError(cause));
            }
          })
        }
      >
        重新读取账户
      </button>
      {read && !accounts.length && <p>尚无模型账户</p>}
      <ul className="account-list">
        {accounts.map((a) => (
          <li key={a.id}>
            <div>
              <strong>{a.name}</strong>
              <code>{a.id}</code>
              <span>
                {a.provider} · {a.auth_method} · {billingLabels[a.billing_mode]}{" "}
                · {a.enabled ? "已启用（非可用性验证）" : "已停用"}
              </span>
              <span>
                {a.auth_method === "api_key" ? (
                  <>
                    当前凭据：{a.current_credential_version}
                    {a.current_credential_version === 0 ? "（未配置）" : ""}
                  </>
                ) : (
                  "授权状态未读取"
                )}{" "}
                · 身份未验证 · 额度未知
              </span>
            </div>
            <button
              type="button"
              disabled={busy}
              aria-label={`管理账户 ${a.name}`}
              onClick={() => open(a.id)}
            >
              管理
            </button>
          </li>
        ))}
      </ul>
      <details className="disclosure" open={!accounts.length}>
        <summary>新建账户</summary>
        <form
          className="connection-form"
          onSubmit={(e) => {
            e.preventDefault();
            void run(async (valid) => {
              try {
                const { account } = await modelAccountApi.create(form);
                if (valid()) {
                  merge(account);
                  setForm(blank);
                }
              } catch (cause) {
                if (valid()) setError(accountError(cause));
              }
            });
          }}
        >
          <fieldset disabled={busy || !read}>
            <legend className="sr-only">账户声明</legend>
            <label htmlFor="account-name">账户名称</label>
            <input
              id="account-name"
              required
              maxLength={200}
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
            <label htmlFor="account-provider">声明厂商</label>
            <select
              id="account-provider"
              value={form.provider}
              onChange={(e) => {
                const provider = e.target.value;
                setForm({
                  ...form,
                  provider,
                  ...(provider === "openai"
                    ? {}
                    : { auth_method: "api_key" as const }),
                });
              }}
            >
              {["openai", "anthropic", "zhipu", "kimi", "custom"].map((p) => (
                <option key={p} value={p}>
                  {
                    {
                      openai: "OpenAI",
                      anthropic: "Anthropic",
                      zhipu: "智谱 Zhipu",
                      kimi: "Kimi",
                      custom: "自定义",
                    }[p]
                  }
                </option>
              ))}
            </select>
            <label htmlFor="account-auth">认证方式</label>
            <select
              id="account-auth"
              value={form.auth_method}
              onChange={(event) => {
                const auth_method = event.target
                  .value as AccountCreateInput["auth_method"];
                setForm({
                  ...form,
                  auth_method,
                  ...(auth_method === "openai_siwc"
                    ? { provider: "openai", billing_mode: "subscription" }
                    : {}),
                });
              }}
            >
              <option value="api_key">API Key</option>
              {form.provider === "openai" && (
                <option value="openai_siwc">OpenAI SIWC（仅元数据）</option>
              )}
            </select>
            <label htmlFor="account-url">账户固定端点</label>
            <input
              id="account-url"
              type="url"
              required
              value={form.base_url}
              onChange={(e) => setForm({ ...form, base_url: e.target.value })}
            />
            <label htmlFor="account-billing">声明计费渠道</label>
            <select
              id="account-billing"
              value={form.billing_mode}
              disabled={form.auth_method === "openai_siwc"}
              onChange={(e) =>
                setForm({
                  ...form,
                  billing_mode: e.target
                    .value as AccountCreateInput["billing_mode"],
                })
              }
            >
              {Object.entries(billingLabels).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
            <p className="field-help">
              {form.auth_method === "openai_siwc"
                ? "本次仅保存 OpenAI SIWC 账户元数据；真实登录仍关闭，不会打开授权页。"
                : "API Key 仅在账户创建后通过追加式版本管理。"}
              厂商、端点、认证方式、计费声明创建后不可改写；需要变更请新建账户。
            </p>
            <button type="submit">创建账户</button>
          </fieldset>
        </form>
      </details>
      {selected && (
        <section
          className="account-editor"
          aria-label={`管理 ${selected.name}`}
        >
          <h3>管理 {selected.name}</h3>
          <code>
            {selected.id} · r{selected.revision}
          </code>
          <form
            className="connection-form"
            onSubmit={(e) => {
              e.preventDefault();
              void run(async (valid) => {
                try {
                  const { account } = await modelAccountApi.update(
                    selected.id,
                    { name, enabled, expected_revision: selected.revision },
                  );
                  if (valid()) {
                    setSelected(account);
                    merge(account);
                  }
                } catch (cause) {
                  if (valid()) {
                    setError(accountError(cause));
                    if (cause instanceof APIError && cause.status === 409)
                      await refresh(true);
                  }
                }
              });
            }}
          >
            <label htmlFor="account-edit-name">账户新名称</label>
            <input
              id="account-edit-name"
              value={name}
              disabled={busy}
              required
              onChange={(e) => setName(e.target.value)}
            />
            <label className="check-label">
              <input
                type="checkbox"
                checked={enabled}
                disabled={busy}
                onChange={(e) => setEnabled(e.target.checked)}
              />
              启用账户（只控制后续调用，不代表验证通过）
            </label>
            <p className="field-help">
              停用不会删除历史，也不会追溯取消已经取得执行权的调用。
            </p>
            <button type="submit" disabled={busy}>
              保存账户变更
            </button>
          </form>
          {selected.auth_method === "api_key" ? (
            <ModelAccountCredentials
              key={selected.id}
              account={selected}
              credentials={credentials}
              refresh={refresh}
            />
          ) : (
            <ModelAuthorizationCard account={selected} runtime={runtime} />
          )}
          <ModelAcceptanceCard
            accountId={selected.id}
            accountRevision={selected.revision}
          />
          <ModelAcceptanceOperations
            account={selected}
            refreshAccount={refresh}
          />
        </section>
      )}
    </section>
  );
}
