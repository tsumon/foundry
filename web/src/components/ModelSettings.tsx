import { useEffect, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import {
  roles,
  type Profile,
  type ProfileInput,
  type Settings,
  type ModelAccount,
  type ModelRoute,
  type ModelRuntime,
} from "../types";
import { RoleBindings } from "./RoleBindings";
import { ModelAccounts } from "./ModelAccounts";
import { accountError } from "../modelAccountApi";
import {
  ModelRouteSettings,
  type ModelRouteSnapshot,
} from "./ModelRouteSettings";
import { RolePolicySettings } from "./RolePolicySettings";
const blank: ProfileInput = {
  name: "",
  kind: "direct",
  protocol: "responses",
  base_url: "",
  model: "",
};
export function ModelSettings() {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [accounts, setAccounts] = useState<ModelAccount[]>([]);
  const [routes, setRoutes] = useState<ModelRoute[]>([]);
  const [modelRuntime, setModelRuntime] = useState<ModelRuntime>();
  const [routesLoaded, setRoutesLoaded] = useState(false);
  const [settings, setSettings] = useState<Settings>({
    default_profile_id: "",
    role_bindings: {},
  });
  const [form, setForm] = useState<ProfileInput>(blank);
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [models, setModels] = useState<string[]>([]);
  useEffect(() => {
    let disposed = false;
    setLoading(true);
    setError("");
    void api
      .profiles()
      .then((data) => {
        if (!disposed) {
          setProfiles(data.profiles ?? []);
          setSettings({
            ...data.settings,
            role_bindings: data.settings.role_bindings ?? {},
          });
          setLoaded(true);
        }
      })
      .catch((e) => {
        if (!disposed) setError(errorMessage(e));
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [loadAttempt]);
  const edit = (profile?: Profile) => {
    setForm(
      profile
        ? {
            id: profile.id,
            name: profile.name,
            kind: profile.kind,
            protocol: profile.protocol,
            base_url: profile.base_url,
            model: profile.model,
            ...(profile.route_id ? { route_id: profile.route_id } : {}),
            ...(profile.account_id ? { account_id: profile.account_id } : {}),
          }
        : blank,
    );
    setKey("");
    setModels([]);
    setNotice("");
    setError("");
  };
  const field = <K extends keyof ProfileInput>(
    name: K,
    value: ProfileInput[K],
  ) => {
    setForm((old) => ({ ...old, [name]: value }));
    setNotice("");
    setModels([]);
  };
  const run = async (operation: () => Promise<void>) => {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await operation();
    } catch (e) {
      setKey("");
      setError(accountError(e));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  const save = () => {
    if (!loaded || loading) return;
    return run(async () => {
      const { profile } = await api.saveProfile({
        ...form,
        ...(key ? { api_key: key } : {}),
      });
      setKey("");
      setForm({
        id: profile.id,
        name: profile.name,
        kind: profile.kind,
        protocol: profile.protocol,
        base_url: profile.base_url,
        model: profile.model,
        ...(profile.route_id ? { route_id: profile.route_id } : {}),
        ...(profile.account_id ? { account_id: profile.account_id } : {}),
      });
      setProfiles((old) => [
        profile,
        ...old.filter((item) => item.id !== profile.id),
      ]);
      setNotice("连接已保存。保存不代表已验证推理能力。");
    });
  };
  const current = profiles.find((profile) => profile.id === form.id);
  const selectedRoute = routes.find((route) => route.id === form.route_id);
  const routeProbeEnabled =
    !!form.id &&
    selectedRoute?.environment === "simulation" &&
    modelRuntime?.simulation_inference === true;
  const acceptRouteSnapshot = (next: ModelRouteSnapshot) => {
    setRoutes(next.routes);
    setModelRuntime(next.runtime);
    setRoutesLoaded(next.loaded);
  };
  return (
    <div className="page settings-page">
      <header className="page-header">
        <p className="eyebrow">设置</p>
        <h1>模型与连接</h1>
        <p className="muted">
          让每个角色使用合适的模型。凭据留在后端，授权由你掌握。
        </p>
      </header>
      <ModelRouteSettings
        accounts={accounts}
        onSnapshot={acceptRouteSnapshot}
      />
      <ModelAccounts onAccounts={setAccounts} runtime={modelRuntime} />
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {!loaded && !loading && (
        <div className="note warning-note">
          <p>尚未读取现有设置。为避免覆盖已有分配，连接与角色编辑暂时锁定。</p>
          <button
            disabled={busy}
            onClick={() => {
              setLoading(true);
              setLoadAttempt((attempt) => attempt + 1);
            }}
          >
            重新读取模型设置
          </button>
        </div>
      )}
      {notice && (
        <p className="notice" role="status">
          {notice}
        </p>
      )}
      <div className="settings-top">
        <section className="connection-section">
          <div className="section-heading">
            <h2>{form.id ? "编辑连接" : "新建连接"}</h2>
            {form.id && (
              <button
                className="text-button"
                disabled={busy}
                onClick={() => edit()}
              >
                ＋ 新建连接
              </button>
            )}
          </div>
          <p className="muted">配置模型服务，用于新建任务中的角色调用。</p>
          <form
            className="connection-form"
            onSubmit={(event) => {
              event.preventDefault();
              void save();
            }}
          >
            <fieldset disabled={busy || loading || !loaded}>
              <legend className="sr-only">模型连接配置</legend>
              <label htmlFor="profile-route">模型路由</label>
              <select
                id="profile-route"
                value={form.route_id ?? ""}
                disabled={!!current?.route_id || !routesLoaded}
                onChange={(event) => {
                  const route = routes.find(
                    (item) => item.id === event.target.value,
                  );
                  setKey("");
                  setModels([]);
                  setForm((old) => ({
                    ...old,
                    route_id: route?.id,
                    account_id: route?.account_id,
                    base_url: route?.base_url ?? "",
                    protocol:
                      (route?.protocol as Profile["protocol"] | undefined) ??
                      "responses",
                  }));
                }}
              >
                <option value="">不使用路由（旧 schema1）</option>
                {routes.map((route) => (
                  <option key={route.id} value={route.id}>
                    {route.id} · {route.adapter} · {route.environment}
                  </option>
                ))}
                {current?.route_id &&
                  !routes.some((route) => route.id === current.route_id) && (
                    <option value={current.route_id}>{current.route_id}</option>
                  )}
              </select>
              {form.route_id && (
                <p className="note">
                  路由固定账户、端点、协议与计费通道。编辑现有路由连接时不能更换路由或账户；如需变更请新建连接。
                </p>
              )}
              <label htmlFor="profile-account">凭据来源</label>
              <select
                id="profile-account"
                value={form.account_id ?? ""}
                disabled={
                  !!current?.account_id || !!current?.has_key || !!form.route_id
                }
                onChange={(e) => {
                  const selected = accounts.find(
                    (a) => a.id === e.target.value,
                  );
                  setKey("");
                  setModels([]);
                  setForm((old) => ({
                    ...old,
                    route_id: undefined,
                    account_id: selected?.id,
                    base_url: selected?.base_url ?? "",
                  }));
                }}
              >
                <option value="">旧式独立连接</option>
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}（未验证）
                  </option>
                ))}
                {current?.account_id &&
                  !accounts.some((a) => a.id === current.account_id) && (
                    <option value={current.account_id}>
                      {current.account?.name ?? current.account_id}
                    </option>
                  )}
              </select>
              {form.account_id && (
                <p className="note">
                  账户绑定不可更换或解除。凭据在账户版本列表管理；schema1
                  账户连接不提供目录探测。
                </p>
              )}
              <label htmlFor="provider-kind">接入方式</label>
              <select
                id="provider-kind"
                value={form.kind}
                onChange={(e) =>
                  field("kind", e.target.value as Profile["kind"])
                }
              >
                <option value="direct">直接 API</option>
                <option value="magpie">Magpie</option>
                <option value="compatible">兼容网关</option>
              </select>
              <label htmlFor="profile-name">连接名称</label>
              <input
                id="profile-name"
                value={form.name}
                onChange={(e) => field("name", e.target.value)}
                required
                maxLength={200}
                placeholder="为这个连接命名"
              />
              <label htmlFor="profile-protocol">协议</label>
              <select
                id="profile-protocol"
                value={form.protocol}
                disabled={!!form.route_id}
                onChange={(e) =>
                  field("protocol", e.target.value as Profile["protocol"])
                }
              >
                <option value="responses">Responses</option>
                <option value="chat_completions">Chat Completions</option>
                {form.protocol === "messages" && (
                  <option value="messages">Anthropic Messages</option>
                )}
              </select>
              <label htmlFor="profile-url">API 地址</label>
              <input
                id="profile-url"
                type="url"
                required
                value={form.base_url}
                onChange={(e) => field("base_url", e.target.value)}
                placeholder="https://your-provider.example/v1"
                readOnly={
                  !!current?.has_key || !!form.account_id || !!form.route_id
                }
              />
              <p className="field-help">
                {current?.has_key
                  ? "已有凭据的连接不能更换端点，请新建连接。"
                  : "远程地址要求 HTTPS；本机回环地址允许 HTTP。"}
              </p>
              <label htmlFor="profile-model">模型 ID</label>
              <input
                id="profile-model"
                value={form.model}
                onChange={(e) => field("model", e.target.value)}
                required
                placeholder="输入模型 ID"
                list="probed-models"
              />
              <datalist id="probed-models">
                {models.map((model) => (
                  <option key={model} value={model} />
                ))}
              </datalist>
              {!form.account_id && !current?.has_key && (
                <>
                  <label htmlFor="profile-key">API Key（只写）</label>
                  <input
                    id="profile-key"
                    type="password"
                    value={key}
                    onChange={(e) => setKey(e.target.value)}
                    autoComplete="new-password"
                    spellCheck={false}
                    placeholder="可选：无认证的本地网关可留空"
                  />
                  <p className="field-help">
                    只写不读，保存到 macOS
                    Keychain；保存成功后清空输入，不读取其他应用配置。
                  </p>
                </>
              )}
              {current?.has_key && !form.account_id && (
                <p className="note">
                  旧连接已有凭据，不能原地覆盖或更换端点。请创建账户及新模型连接；留空保存保持原凭据。
                </p>
              )}
              <div className="actions">
                <button className="primary" type="submit">
                  {busy ? "处理中…" : "保存连接"}
                </button>
                {!form.account_id && (
                  <button
                    type="button"
                    disabled={!form.id}
                    onClick={() =>
                      void run(async () => {
                        const result = await api.probe(form.id!);
                        setModels(result.models ?? []);
                        setNotice(
                          `模型目录可访问：${result.models?.length ?? 0} 个模型。未执行推理。探测使用已保存的连接。`,
                        );
                      })
                    }
                  >
                    探测模型目录
                  </button>
                )}
                {form.route_id && (
                  <button
                    type="button"
                    disabled={!routeProbeEnabled}
                    onClick={() =>
                      void run(async () => {
                        const result = await api.probe(form.id!);
                        setModels(result.models ?? []);
                        setNotice(
                          `模型目录可访问：${result.models?.length ?? 0} 个模型。未执行推理；目录结果不证明推理可用。`,
                        );
                      })
                    }
                  >
                    {selectedRoute?.environment === "live"
                      ? "探测模型目录（真实环境未开放）"
                      : modelRuntime?.simulation_inference
                        ? "探测本地模拟模型目录"
                        : "探测模型目录（本进程模拟未启用）"}
                  </button>
                )}
              </div>
              {form.kind === "magpie" && !form.account_id && (
                <button
                  type="button"
                  className="text-button"
                  disabled={!form.base_url}
                  onClick={() =>
                    void run(async () => {
                      const result = await api.discover(form.base_url);
                      setModels(result.models ?? []);
                      setNotice(
                        result.available
                          ? `Magpie 目录可访问：${result.models?.length ?? 0} 个模型；未执行推理。`
                          : "该地址的 Magpie 目录不可用；未执行推理。",
                      );
                    })
                  }
                >
                  发现此本地 Magpie 地址
                </button>
              )}
            </fieldset>
          </form>
        </section>
        <aside className="connection-guide">
          <h2>接入方式</h2>
          <div>
            <h3>直接 API</h3>
            <p>连接支持 Responses 或 Chat Completions 的模型 API。</p>
          </div>
          <div>
            <h3>Magpie</h3>
            <p>可选的本地兼容网关，不是必需依赖。上游转发不等于在本机计算。</p>
          </div>
          <div>
            <h3>兼容网关</h3>
            <p>
              OpenAI-compatible endpoint，可填写 CC Switch 代理地址。不支持
              Anthropic / Gemini 原生协议。
            </p>
          </div>
          <div className="note">
            目录连通不代表推理可用。探测只读取模型目录，不发起推理请求；不声称订阅、计费或模型状态。
          </div>
        </aside>
      </div>
      <section className="saved-connections">
        <h2>已保存连接</h2>
        {!loaded ? (
          <p className="muted">
            {loading ? "正在读取连接…" : "连接信息尚未读取"}
          </p>
        ) : (
          !profiles.length && <p className="muted">未配置</p>
        )}
        {profiles.map((profile) => (
          <div className="saved-connection" key={profile.id}>
            <div>
              <strong>{profile.name}</strong>
              <small>
                {profile.protocol} · {profile.model}
              </small>
              <code>{profile.base_url}</code>
              {profile.route_id && <code>路由 {profile.route_id}</code>}
            </div>
            <span className="muted small">
              {profile.account_id
                ? `账户绑定 · ${accounts.find((account) => account.id === profile.account_id)?.name ?? profile.account?.name ?? profile.account_id} · ${modelRuntime?.live_inference ? "仅受支持的账户编码任务按独立审批" : "真实调用未开放"}`
                : profile.has_key
                  ? "旧凭据已配置（不可覆盖）"
                  : "旧式连接 · 未配置凭据"}
            </span>
            <button
              disabled={busy}
              onClick={() => edit(profile)}
              aria-label={`编辑 ${profile.name}`}
            >
              编辑
            </button>
          </div>
        ))}
      </section>
      {loaded && (
        <>
          <RoleBindings
            profiles={profiles}
            accounts={accounts}
            settings={settings}
            liveTaskInference={modelRuntime?.live_inference === true}
            disabled={busy || loading}
            onChange={(next) => {
              if (!loading && !busy) setSettings(next);
            }}
            onSave={() => {
              if (loading) return;
              void run(async () => {
                const bindings = Object.fromEntries(
                  roles.map((role) => [
                    role,
                    settings.role_bindings[role] ?? "",
                  ]),
                );
                const result = await api.saveSettings({
                  default_profile_id: settings.default_profile_id,
                  role_bindings: bindings,
                });
                setSettings({
                  ...result.settings,
                  role_bindings: result.settings.role_bindings ?? {},
                });
                setNotice(
                  settings.model_policy_decision_id
                    ? "手动角色分配已保存，活动策略标记已清除。已有任务保持冻结。"
                    : "默认连接与角色分配已保存。",
                );
              });
            }}
          />
          <RolePolicySettings
            key={settings.model_policy_decision_id ?? "manual"}
            profiles={profiles}
            disabled={busy || loading}
            onConfirmed={(next) =>
              setSettings({
                ...next,
                role_bindings: next.role_bindings ?? {},
              })
            }
          />
        </>
      )}
    </div>
  );
}
