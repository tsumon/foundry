import { useEffect, useRef, useState } from "react";
import {
  accountError,
  modelRouteApi,
  type ModelRouteCreateInput,
} from "../modelAccountApi";
import type {
  ModelAccount,
  ModelAdapterRuntime,
  ModelRoute,
  ModelRuntime,
} from "../types";
import { billingLabels } from "./ModelAccounts";

export interface ModelRouteSnapshot {
  routes: ModelRoute[];
  runtime?: ModelRuntime;
  loaded: boolean;
}

const adapterLabels: Record<string, string> = {
  compatible_api_responses: "兼容 API / Magpie · Responses",
  openai_api_chat: "OpenAI API · Chat Completions",
  openai_api_responses: "OpenAI API · Responses",
  anthropic_api_messages: "Anthropic API · Messages",
  zhipu_api_chat: "智谱标准 API · Chat Completions",
  kimi_api_chat: "Kimi 标准 API · Chat Completions",
  kimi_code_chat: "Kimi Code 套餐 · Chat Completions",
  openai_siwc_responses: "OpenAI SIWC · Responses",
};

const blankRoute: Pick<
  ModelRouteCreateInput,
  "account_id" | "adapter" | "environment" | "extra_usage"
> = {
  account_id: "",
  adapter: "",
  environment: "live",
  extra_usage: "off",
};

const supportsAccount = (adapter: ModelAdapterRuntime, account: ModelAccount) =>
  adapter.provider === account.provider &&
  adapter.auth_method === account.auth_method &&
  (adapter.spend_class === "explicit_api"
    ? account.billing_mode === "api_credits" ||
      account.billing_mode === "metered_api"
    : account.billing_mode === "subscription");

export function ModelRouteSettings({
  accounts,
  onSnapshot,
}: {
  accounts: ModelAccount[];
  onSnapshot: (snapshot: ModelRouteSnapshot) => void;
}) {
  const [routes, setRoutes] = useState<ModelRoute[]>([]);
  const [runtime, setRuntime] = useState<ModelRuntime>();
  const [loaded, setLoaded] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [form, setForm] = useState(blankRoute);
  const mounted = useRef(true);
  const mutationLock = useRef(false);
  const snapshot = useRef(onSnapshot);
  snapshot.current = onSnapshot;

  useEffect(() => {
    mounted.current = true;
    let active = true;
    setLoading(true);
    void modelRouteApi
      .list()
      .then((result) => {
        if (!active) return;
        const next = result.routes ?? [];
        setRoutes(next);
        setRuntime(result.runtime);
        setLoaded(true);
        snapshot.current({
          routes: next,
          runtime: result.runtime,
          loaded: true,
        });
      })
      .catch((cause) => {
        if (active) setError(accountError(cause));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      mounted.current = false;
    };
  }, []);

  const selected = accounts.find((account) => account.id === form.account_id);
  const liveTaskInference = runtime?.live_inference === true;
  const adapterTaskInferenceAvailable = (adapterID: string) =>
    liveTaskInference &&
    runtime?.adapters.some(
      (adapter) =>
        adapter.id === adapterID && adapter.implemented && adapter.live_enabled,
    ) === true;
  const adapters = (runtime?.adapters ?? []).filter(
    (adapter) => selected && supportsAccount(adapter, selected),
  );
  const create = async () => {
    if (!selected || !form.adapter || mutationLock.current) return;
    mutationLock.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const { route } = await modelRouteApi.create({
        ...form,
        expected_account_revision: selected.revision,
      });
      if (!mounted.current) return;
      const next = [route, ...routes.filter((item) => item.id !== route.id)];
      setRoutes(next);
      setNotice("路由已保存。创建配置不代表目录或推理已验证。");
      snapshot.current({ routes: next, runtime, loaded });
    } catch (cause) {
      if (mounted.current) setError(accountError(cause));
    } finally {
      mutationLock.current = false;
      if (mounted.current) setBusy(false);
    }
  };

  return (
    <section className="model-routes" aria-label="受控模型接入">
      <div className="section-heading">
        <div>
          <p className="eyebrow">受控接入</p>
          <h2>模型路由与运行门禁</h2>
        </div>
        <span className="route-lock">
          {liveTaskInference
            ? "受支持账户编码任务逐次门禁"
            : "生产真实接入关闭"}
        </span>
      </div>
      <p className="note">
        {liveTaskInference
          ? "当前进程仅开放受支持账户模型编码任务的真实推理传输，仅受支持的账户编码任务按独立审批；这不代表账户已连接或验证，也不开放登录、目录、刷新或一般账户路由。"
          : "全局账户路由的真实授权、目录发现与推理保持关闭；账户级订阅连接另由独立验收审批处理。单次目录与固定单次推理也各需当前进程明确准入、独立材料批准与执行，不启用全局任务路由。旧式独立连接的既有行为保持不变。"}
        不会自动切换到付费
        API；每条路由都必须明确选择账户、适配器与额外用量声明。
      </p>
      {!loaded && (
        <p className="warning-copy">
          路由与运行状态尚未读取；这不是空列表或健康状态。真实接入仍保持禁用。
        </p>
      )}
      {loading && <p role="status">正在读取路由与运行状态…</p>}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      <dl className="route-runtime-grid">
        <div>
          <dt>真实授权 / 发现 / 推理</dt>
          <dd>
            {runtime
              ? runtime.live_authorization || runtime.live_discovery
                ? "状态冲突，界面仍禁用"
                : liveTaskInference
                  ? "仅受支持的账户编码任务按独立审批"
                  : "关闭"
              : "未读取"}
          </dd>
        </div>
        <div>
          <dt>本地模拟推理</dt>
          <dd>
            {runtime
              ? runtime.simulation_inference
                ? "本进程可用（仅模拟）"
                : "本进程未启用本地模拟推理"
              : "未读取"}
          </dd>
        </div>
        <div>
          <dt>自动付费降级</dt>
          <dd>
            {runtime
              ? runtime.automatic_paid_fallback
                ? "状态冲突，界面不提供操作"
                : "关闭"
              : "未读取"}
          </dd>
        </div>
      </dl>
      <div className="route-live-lock">
        <button type="button" disabled aria-describedby="live-login-reason">
          OpenAI 真实登录（未开放）
        </button>
        <p id="live-login-reason" className="field-help">
          {runtime?.desktop_subscription
            ? "此直接登录按钮保持禁用；桌面编码工作台可在账户卡中显式准备，再独立审批、执行连接与目录。"
            : "此直接登录按钮保持禁用；账户级真实连接仅在操作员显式启动后，由验收材料独立审批与执行。"}
          本界面不会自动打开授权页或轮询。Claude 订阅与智谱 Coding Plan 未开放。
        </p>
      </div>
      <details className="disclosure">
        <summary>查看适配器定义（{runtime?.adapters.length ?? 0}）</summary>
        <div className="adapter-list">
          {(runtime?.adapters ?? []).map((adapter) => (
            <div key={adapter.id}>
              <strong>{adapterLabels[adapter.id] ?? adapter.id}</strong>
              <code>{adapter.id}</code>
              <span>
                {adapter.implemented ? "已实现" : "未实现"} · 真实接入
                {adapterTaskInferenceAvailable(adapter.id)
                  ? "任务调用按独立审批"
                  : adapter.live_enabled
                    ? "状态冲突"
                    : liveTaskInference
                      ? "真实调用未开放"
                      : "待确认"}
              </span>
            </div>
          ))}
        </div>
      </details>
      <div className="route-layout">
        <div>
          <h3>已保存路由</h3>
          {loaded && !routes.length && <p>尚未创建模型路由</p>}
          {!loaded && <p className="muted">路由未读取</p>}
          <div className="route-list">
            {routes.map((route) => (
              <article key={route.id}>
                <strong>{adapterLabels[route.adapter] ?? route.adapter}</strong>
                <code>{route.id}</code>
                <span>
                  {route.environment === "simulation"
                    ? "本地模拟"
                    : adapterTaskInferenceAvailable(route.adapter)
                      ? "真实环境（任务调用按独立审批）"
                      : "真实环境（关闭）"}
                  {" · "}
                  {route.spend_class} · extra usage {route.extra_usage}
                </span>
                <code>{route.base_url}</code>
              </article>
            ))}
          </div>
        </div>
        <form
          className="connection-form route-form"
          onSubmit={(event) => {
            event.preventDefault();
            void create();
          }}
        >
          <h3>创建不可变路由</h3>
          <fieldset disabled={!loaded || busy}>
            <legend className="sr-only">不可变模型路由</legend>
            <label htmlFor="route-account">路由账户</label>
            <select
              id="route-account"
              value={form.account_id}
              required
              onChange={(event) =>
                setForm({
                  ...form,
                  account_id: event.target.value,
                  adapter: "",
                })
              }
            >
              <option value="">选择现有账户</option>
              {accounts.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.name}
                </option>
              ))}
            </select>
            {selected && (
              <div className="route-account-facts">
                <span>{billingLabels[selected.billing_mode]}</span>
                <code>{selected.base_url}</code>
                <small>
                  固定端点与认证方式来自账户；不会根据模型名称推断。
                </small>
              </div>
            )}
            <label htmlFor="route-adapter">适配器</label>
            <select
              id="route-adapter"
              value={form.adapter}
              required
              onChange={(event) =>
                setForm({ ...form, adapter: event.target.value })
              }
            >
              <option value="">选择匹配适配器</option>
              {adapters.map((adapter) => (
                <option key={adapter.id} value={adapter.id}>
                  {adapterLabels[adapter.id] ?? adapter.id}
                </option>
              ))}
            </select>
            <label htmlFor="route-environment">环境</label>
            <select
              id="route-environment"
              value={form.environment}
              onChange={(event) =>
                setForm({
                  ...form,
                  environment: event.target
                    .value as ModelRouteCreateInput["environment"],
                })
              }
            >
              <option value="live">
                {adapterTaskInferenceAvailable(form.adapter)
                  ? "真实环境（任务调用按独立审批）"
                  : "真实环境（当前关闭）"}
              </option>
              <option value="simulation">本地模拟（需进程显式启用）</option>
            </select>
            <label htmlFor="route-extra-usage">额外用量声明</label>
            <select
              id="route-extra-usage"
              value={form.extra_usage}
              onChange={(event) =>
                setForm({
                  ...form,
                  extra_usage: event.target
                    .value as ModelRouteCreateInput["extra_usage"],
                })
              }
            >
              <option value="off">关闭</option>
              <option value="unknown">未知（不可视为可运行）</option>
            </select>
            <button type="submit" disabled={!selected || !form.adapter || busy}>
              {busy ? "正在创建…" : "创建不可变路由"}
            </button>
          </fieldset>
        </form>
      </div>
    </section>
  );
}
