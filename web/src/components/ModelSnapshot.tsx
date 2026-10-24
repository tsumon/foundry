import {
  roles,
  roleNames,
  isAccountWorkspaceMode,
  type Mission,
  type Role,
  type RoleModelSelection,
} from "../types";
import {
  invalidModelSnapshot,
  invalidRolePolicySnapshot,
} from "../modelSnapshot";
import { billingLabels } from "./ModelAccounts";
import { RolePolicySnapshot } from "./RolePolicySnapshot";
import { isModelBackedC4 } from "../c4";

export function ModelSnapshot({
  mission,
  preview,
  codingModePreview = false,
  compact = false,
  showPolicy = true,
}: {
  mission?: Mission;
  preview?: Partial<Record<Role, RoleModelSelection>>;
  codingModePreview?: boolean;
  compact?: boolean;
  showPolicy?: boolean;
}) {
  if (mission && invalidRolePolicySnapshot(mission))
    return (
      <section className="model-snapshot">
        <h3>任务模型与策略来源</h3>
        <p className="warning-copy">
          角色策略来源损坏或版本不受支持。历史仍可查看，但不会信任部分字段；规划、修改与调用已禁用。
        </p>
      </section>
    );
  if (mission && invalidModelSnapshot(mission))
    return (
      <section className="model-snapshot">
        <h3>任务模型快照</h3>
        <p className="warning-copy">
          模型快照缺失、损坏或版本不受支持。历史仍可查看，不能将部分字段视为完整有效快照；所有修改与调用已禁用。
        </p>
      </section>
    );
  const bound = mission
    ? isAccountWorkspaceMode(mission.workspace_mode)
    : roles.some((role) => !!preview?.[role]?.profile.account_id);
  const modelBacked = !!mission && isModelBackedC4(mission);
  if (compact && mission) {
    const accountSelections = roles
      .map((role) => mission.model_snapshot?.roles[role])
      .filter((selection) => !!selection?.profile.account_id);
    const versions = [
      ...new Set(
        accountSelections
          .filter((selection) => !selection?.authorization)
          .map((selection) => selection?.profile.credential_version ?? 0),
      ),
    ];
    const authorizations = [
      ...new Set(
        accountSelections
          .map((selection) => selection?.authorization?.id)
          .filter((id): id is string => !!id),
      ),
    ];
    return (
      <>
        <section
          className="model-snapshot model-snapshot-compact"
          aria-label="冻结模型摘要"
        >
          <strong>7 个角色已冻结</strong>
          <p className="muted small">
            账户绑定 {accountSelections.length} 个
            {!!versions.length && (
              <>
                {" "}
                · 凭据版本{" "}
                {versions
                  .map((version) => (version === 0 ? "0（未配置）" : version))
                  .join("、")}
              </>
            )}
            。历史快照，非实时状态。
          </p>
          {!!authorizations.length && (
            <p className="muted small">
              稳定授权 {authorizations.join("、")}
              ；目录绑定连接的授权代次与到期见冻结明细。
            </p>
          )}
          {bound && (
            <p className="muted small">
              {modelBacked
                ? "身份与额度须在每次调用前由后端复核；配置不等于当前调用授权。"
                : "身份未验证，额度未知；真实账户调用未开放。"}
            </p>
          )}
          <details className="disclosure">
            <summary>查看冻结模型明细（7 个角色）</summary>
            <ModelSnapshot mission={mission} showPolicy={false} />
          </details>
        </section>
        {showPolicy &&
          mission.model_policy_snapshot_status === "valid" &&
          mission.model_policy_snapshot && (
            <RolePolicySnapshot
              snapshot={mission.model_policy_snapshot}
              compact
            />
          )}
      </>
    );
  }
  return (
    <section
      className="model-snapshot"
      aria-label={preview ? "创建前角色预览" : "任务模型快照"}
    >
      <h3>{preview ? "创建前角色预览" : "冻结的七角色模型"}</h3>
      <p className="muted small">
        {preview
          ? "当前角色专属分配优先于所选默认连接；此处仅为预览，以创建响应中的服务端冻结快照为准。"
          : "来自任务创建时的历史快照，不查询或重新解释当前账户；更名、轮换和停用不会改写这里。"}
      </p>
      {bound && (
        <p className="note">
          {modelBacked
            ? "已冻结账户模型配置；配置不等于当前调用授权。上下文与执行审批保持独立，后端会在每次调用前复核路由、凭据或授权、预算与材料。"
            : preview && codingModePreview
              ? "拟冻结账户模型配置；配置不等于当前调用授权。账户模型调用采用单独门禁，readiness、baseline、context、execute 与 delivery 仍分别审批；创建时及每次调用前由后端复核。不会自动付费降级。"
              : preview
                ? "拟冻结配置；真实账户调用尚未开放。身份未验证，额度未知；不会自动付费降级。"
                : "已冻结配置；真实账户调用尚未开放。身份未验证，额度未知；不会自动付费降级。"}
        </p>
      )}
      <div className="snapshot-roles">
        {roles.map((role) => {
          const selection =
            preview?.[role] ?? mission?.model_snapshot?.roles?.[role];
          const profile = selection?.profile ?? mission?.profiles?.[role];
          const account = selection?.account;
          const route = selection?.route;
          const authorization = selection?.authorization;
          return (
            <div className="snapshot-role" key={role}>
              <strong>
                {roleNames[role]}
                {role === "researcher" && mission && !mission.spec.research
                  ? "（本任务未启用）"
                  : ""}
              </strong>
              <span>
                {profile
                  ? `${profile.name} · ${profile.model} · ${profile.protocol}`
                  : "未配置连接"}
              </span>
              {profile && <code>连接 {profile.id}</code>}
              {profile?.account_id ? (
                <>
                  <span>{account?.name ?? "账户元数据未提供"}</span>
                  <code>
                    账户 {profile.account_id} · r
                    {profile.account_revision ?? account?.revision ?? "未知"}
                  </code>
                  {route?.adapter === "openai_siwc_responses" ? (
                    <>
                      <span>
                        稳定授权 {authorization?.id ?? "缺失"} ·{" "}
                        {authorization?.spend_class ?? route.spend_class}
                      </span>
                      <code>
                        scopes {authorization?.scopes?.join(" · ") || "缺失"}
                      </code>
                      <small>
                        {selection?.catalog
                          ? `目录 ${selection.catalog.operation_id} · 授权代次 ${selection.catalog.authorization_generation} · 到期 ${selection.catalog.expires_at}。目录不授权推理，当前可用性由后端复核。`
                          : "当前 token 代数不是冻结快照；当前授权可用性须由后端复核。"}
                      </small>
                    </>
                  ) : preview &&
                    account &&
                    "auth_method" in account &&
                    account.auth_method === "openai_siwc" ? (
                    <small>
                      SIWC 授权将在创建时由后端复核；此预览不代表当前可调用。
                    </small>
                  ) : (
                    <span>
                      凭据版本：{profile.credential_version ?? 0}
                      {!profile.credential_version && "（未配置）"}
                    </span>
                  )}
                  {route && (
                    <>
                      <code>
                        路由 {route.id} · {route.adapter}
                      </code>
                      <span>
                        {route.environment === "simulation"
                          ? "本地模拟"
                          : modelBacked
                            ? "真实环境（逐次门禁）"
                            : "真实环境（门禁关闭）"}{" "}
                        · {route.spend_class} · 额外用量
                        {route.extra_usage === "off" ? "关闭" : "未知"}
                      </span>
                      <code>{route.base_url}</code>
                    </>
                  )}
                  <span>
                    {account
                      ? (billingLabels[account.billing_mode] ?? "计费渠道未知")
                      : "计费渠道未知"}{" "}
                    · 身份未验证 · 额度未知
                  </span>
                  <small>
                    {preview ? "当前记录" : "历史记录"}：
                    {account?.enabled === false
                      ? "已停用"
                      : account?.enabled === true
                        ? "已启用（不代表可调用）"
                        : "启用状态未知"}
                    ；当前可用性未知。
                  </small>
                </>
              ) : profile ? (
                <>
                  <span>旧式连接 · 不推断账户或计费渠道</span>
                  {"base_url" in profile &&
                    typeof profile.base_url === "string" && (
                      <code>{profile.base_url}</code>
                    )}
                </>
              ) : null}
            </div>
          );
        })}
      </div>
      {mission?.model_snapshot && (
        <small className="snapshot-digest">
          快照 v{mission.model_snapshot.schema_version} ·{" "}
          <code>{mission.model_snapshot.digest}</code>
        </small>
      )}
      {showPolicy &&
        mission?.model_policy_snapshot_status === "valid" &&
        mission.model_policy_snapshot && (
          <RolePolicySnapshot snapshot={mission.model_policy_snapshot} />
        )}
    </section>
  );
}
