import {
  roleNames,
  roles,
  type Profile,
  type Role,
  type Settings,
  type ModelAccount,
} from "../types";
import { billingLabels } from "./ModelAccounts";

const duties: Record<Role, string> = {
  main: "解释计划、证据与阻塞；只读问答",
  researcher: "仓库调研，列出来源和未确认问题",
  planner: "形成带范围与依赖的执行计划",
  worker: "在批准范围内生成候选变更",
  debugger: "依据失败证据进行有界修复",
  qa: "检查需求覆盖和未验证项",
  auditor: "独立评估范围、安全与交付证据",
};

export function RoleBindings({
  profiles,
  accounts = [],
  settings,
  liveTaskInference = false,
  disabled,
  onChange,
  onSave,
}: {
  profiles: Profile[];
  accounts?: ModelAccount[];
  settings: Settings;
  liveTaskInference?: boolean;
  disabled: boolean;
  onChange: (settings: Settings) => void;
  onSave: () => void;
}) {
  return (
    <section className="role-section">
      <h2>角色模型分配</h2>
      <p className="muted">用于之后创建的任务。已有任务保持冻结的角色连接。</p>
      {settings.model_policy_decision_id && (
        <p className="policy-source-line">
          当前七角色分配来自已确认策略{" "}
          <code>{settings.model_policy_decision_id}</code>，仅用于未来任务。
        </p>
      )}
      <label className="default-binding" htmlFor="default-profile">
        默认连接
        <select
          id="default-profile"
          disabled={disabled}
          value={settings.default_profile_id}
          onChange={(event) =>
            onChange({ ...settings, default_profile_id: event.target.value })
          }
        >
          <option value="">未配置</option>
          {profiles.map((profile) => (
            <option key={profile.id} value={profile.id}>
              {profile.name}
            </option>
          ))}
        </select>
      </label>
      <div className="table-scroll">
        <table className="role-table">
          <thead>
            <tr>
              <th>角色</th>
              <th>职责说明</th>
              <th>模型连接</th>
              <th>账户 / 凭据版本</th>
              <th>声明计费 / 可用性</th>
            </tr>
          </thead>
          <tbody>
            {roles.map((role) => {
              const profile = profiles.find(
                (p) =>
                  p.id ===
                  (settings.role_bindings[role] || settings.default_profile_id),
              );
              const account =
                accounts.find((a) => a.id === profile?.account_id) ??
                profile?.account;
              return (
                <tr key={role}>
                  <th scope="row">{roleNames[role]}</th>
                  <td>{duties[role]}</td>
                  <td>
                    <select
                      aria-label={`${roleNames[role]} 连接`}
                      disabled={disabled}
                      value={settings.role_bindings[role] ?? ""}
                      onChange={(event) =>
                        onChange({
                          ...settings,
                          role_bindings: {
                            ...settings.role_bindings,
                            [role]: event.target.value,
                          },
                        })
                      }
                    >
                      <option value="">使用默认连接</option>
                      {profiles.map((profile) => (
                        <option key={profile.id} value={profile.id}>
                          {profile.name}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>
                    {profile?.account_id ? (
                      <>
                        {account?.name ?? profile.account_id}
                        <br />
                        <code>{profile.account_id}</code>
                        <br />
                        {account?.auth_method === "openai_siwc" ? (
                          "OpenAI SIWC 稳定授权（可用性由后端复核）"
                        ) : (
                          <>
                            版本 {account?.current_credential_version ?? "未知"}
                            {account?.current_credential_version === 0
                              ? "（未配置）"
                              : ""}
                          </>
                        )}
                      </>
                    ) : profile ? (
                      "旧式连接"
                    ) : (
                      "未配置"
                    )}
                  </td>
                  <td>
                    {account ? (
                      <>
                        {billingLabels[account.billing_mode]}
                        <br />
                        身份未验证 · 额度未知 ·{" "}
                        {liveTaskInference
                          ? "仅受支持的账户编码任务按独立审批"
                          : "真实调用未开放"}
                      </>
                    ) : (
                      "账户 / 额度未知"
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {settings.model_policy_decision_id && (
        <p className="note warning-note">
          保存手动分配会退出活动策略，但不会删除历史决定，也不会改写已有任务。
        </p>
      )}
      <button className="primary" disabled={disabled} onClick={onSave}>
        保存角色分配
      </button>
    </section>
  );
}
