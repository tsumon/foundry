import { useRef, useState } from "react";
import { api } from "../api";
import { accountError, modelRouteApi } from "../modelAccountApi";
import type { ModelAccount, ModelRoute, ModelRuntime, Profile } from "../types";

function profileName(value: string): string {
  const encoder = new TextEncoder();
  let result = "";
  let bytes = 0;
  for (const character of value) {
    const size = encoder.encode(character).length;
    if (bytes + size > 200) break;
    result += character;
    bytes += size;
  }
  return result;
}

export function OpenAIAPISetup({
  accounts,
  routes,
  profiles,
  runtime,
  loaded,
  onRoutes,
  onProfile,
}: {
  accounts: ModelAccount[];
  routes: ModelRoute[];
  profiles: Profile[];
  runtime?: ModelRuntime;
  loaded: boolean;
  onRoutes: (routes: ModelRoute[]) => void;
  onProfile: (profile: Profile) => void;
}) {
  const [accountID, setAccountID] = useState("");
  const [model, setModel] = useState("");
  const [consent, setConsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const lock = useRef(false);
  const eligible = accounts.filter(
    (a) =>
      ((a.provider === "openai" &&
        a.base_url === "https://api.openai.com/v1") ||
        a.provider === "custom") &&
      a.auth_method === "api_key" &&
      (a.billing_mode === "api_credits" || a.billing_mode === "metered_api") &&
      a.enabled &&
      a.current_credential_version > 0,
  );
  const account = eligible.find((a) => a.id === accountID);
  const adapter =
    account?.provider === "custom"
      ? "compatible_api_responses"
      : "openai_api_responses";
  const matchRoute = (r: ModelRoute) =>
    r.account_id === accountID &&
    r.adapter === adapter &&
    r.environment === "live" &&
    r.base_url === account?.base_url &&
    r.protocol === "responses" &&
    r.spend_class === "explicit_api" &&
    r.extra_usage === "off";
  const matchProfile = (p: Profile, r: ModelRoute) =>
    p.account_id === accountID &&
    p.route_id === r.id &&
    p.model === model.trim() &&
    p.protocol === "responses" &&
    p.base_url === r.base_url &&
    !p.has_key;

  const save = async () => {
    if (
      lock.current ||
      uncertain ||
      !loaded ||
      !account ||
      !consent ||
      !model.trim()
    )
      return;
    lock.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      let route = routes.find(matchRoute);
      if (!route) {
        route = (
          await modelRouteApi.create({
            account_id: account.id,
            expected_account_revision: account.revision,
            adapter,
            environment: "live",
            extra_usage: "off",
          })
        ).route;
        onRoutes([route, ...routes]);
      }
      const frozenRoute = route;
      const existing = profiles.find((p) => matchProfile(p, frozenRoute));
      const profile =
        existing ??
        (
          await api.saveProfile({
            name: profileName(`${account.name} · ${model.trim()}`),
            kind: account.provider === "custom" ? "compatible" : "direct",
            protocol: "responses",
            base_url: route.base_url,
            account_id: account.id,
            route_id: route.id,
            model: model.trim(),
          })
        ).profile;
      onProfile(profile);
      setNotice(
        "连接已保存；尚未调用或验证模型。请在角色分配中选择它并保存，仅用于新任务。",
      );
    } catch (cause) {
      // An accepted write may have lost its response. No replay, including
      // client errors: the user can reconcile nonsecret metadata first.
      setUncertain(true);
      setError(accountError(cause));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  const reconcile = async () => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const [savedRoutes, savedProfiles] = await Promise.all([
        modelRouteApi.list(),
        api.profiles(),
      ]);
      onRoutes(savedRoutes.routes);
      const route = savedRoutes.routes.find(matchRoute);
      const profile =
        route && savedProfiles.profiles?.find((p) => matchProfile(p, route));
      if (profile) onProfile(profile);
      setNotice(
        profile
          ? "已找到保存的连接；没有重送。请在角色分配中选择它，仅用于新任务。"
          : route
            ? "路由已保存，尚未找到模型连接；没有重送。可手动继续保存连接。"
            : "未找到匹配的路由或连接；没有重送。请核对后手动保存。",
      );
      setUncertain(false);
    } catch (cause) {
      setError(accountError(cause));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  const available =
    runtime?.live_inference === true &&
    runtime.adapters.some(
      (a) => a.id === adapter && a.implemented && a.live_enabled,
    );
  return (
    <section
      className="model-accounts api-quick-setup"
      aria-label="API 快速配置"
    >
      <div className="section-heading">
        <h2>API 快速配置</h2>
        <span className="route-lock">推荐接入方式</span>
      </div>
      <p className="note">
        使用自己的 API Key，无需 ChatGPT 登录。支持 OpenAI 官方及兼容 Responses
        网关（如 Magpie）。
        先在下方创建账户并保存凭据，再选择模型。用量由所选服务计费；保存只写本地配置，不查询目录或发送模型请求。
      </p>
      {!available && (
        <p className="field-help">
          可保存配置。真实编码需在桌面菜单开启 Controlled Coding
          Workbench，之后仍按任务审批。
        </p>
      )}
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      {notice && (
        <p role="status">
          {notice} <a href="#model-role-settings">前往角色分配</a>
        </p>
      )}
      {uncertain && (
        <button type="button" disabled={busy} onClick={() => void reconcile()}>
          仅重新读取保存结果
        </button>
      )}
      <form
        className="connection-form"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <fieldset disabled={!loaded || busy || uncertain}>
          <legend className="sr-only">API 模型配置</legend>
          <label htmlFor="quick-api-account">API 账户</label>
          <select
            id="quick-api-account"
            value={accountID}
            required
            onChange={(e) => {
              setAccountID(e.target.value);
              setConsent(false);
              setNotice("");
              setError("");
            }}
          >
            <option value="">选择已保存 Key 的 API 账户</option>
            {eligible.map((a) => (
              <option value={a.id} key={a.id}>
                {a.name} · 凭据版本 {a.current_credential_version}
              </option>
            ))}
          </select>
          {account && (
            <p className="note">
              固定接收端：<code>{account.base_url}</code> · Responses。
              {account.provider === "custom" &&
                "这是自定义网关，模型身份及计费以上游服务为准；Foundry 不更换路由。"}
            </p>
          )}
          {!eligible.length && (
            <p className="field-help">
              尚无可配置的 API 账户。
              <a href="#model-account-settings">创建账户并保存 Key</a>
              ；已有订阅账户不会转为收费 API。
            </p>
          )}
          <label htmlFor="quick-api-model">API 模型 ID</label>
          <input
            id="quick-api-model"
            value={model}
            required
            maxLength={200}
            placeholder="填写 API 项目可用的模型 ID"
            onChange={(e) => {
              setModel(e.target.value);
              setConsent(false);
              setNotice("");
            }}
          />
          <p className="field-help">
            以 API 项目权限为准；订阅目录中的模型不一定可通过 API 使用。
          </p>
          <label className="check-label">
            <input
              type="checkbox"
              checked={consent}
              onChange={(e) => setConsent(e.target.checked)}
            />
            我选择按 API 计费；任务调用另行审批，不自动切换账户或模型。
          </label>
          <button
            type="submit"
            className="primary"
            disabled={!account || !model.trim() || !consent}
          >
            保存 API 模型连接
          </button>
        </fieldset>
      </form>
    </section>
  );
}
