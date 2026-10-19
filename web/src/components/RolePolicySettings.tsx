import { useEffect, useRef, useState } from "react";
import { APIError, api, errorMessage } from "../api";
import { rolePolicyApi } from "../rolePolicyApi";
import {
  rolePolicyCapabilities,
  rolePolicySpendClasses,
  type RoleModelPolicy,
  type RolePolicyCapability,
  type RolePolicyCandidateInput,
  type RolePolicyInput,
  type RolePolicyProposal,
  type RolePolicyReason,
  type RolePolicySpendClass,
  type RolePolicyState,
} from "../rolePolicyTypes";
import {
  roleNames,
  roles,
  type Profile,
  type Role,
  type Settings,
} from "../types";

const capabilityLabels: Record<RolePolicyCapability, string> = {
  structured_output: "结构化输出",
  reasoning: "推理",
  coding: "编码",
  long_context: "长上下文",
  review: "审查",
  concise_explanation: "简洁说明",
};
const spendLabels: Record<RolePolicySpendClass, string> = {
  subscription_only: "仅订阅",
  interactive_package: "交互套餐",
  explicit_api: "明确 API 费用",
  unknown: "未知",
};
const reasonLabels: Record<RolePolicyReason, string> = {
  profile_missing: "连接不存在",
  profile_changed: "连接语义已变化，声明需要重存",
  required_capability_missing: "缺少要求的能力声明",
  spend_class_not_allowed: "费用渠道未获允许",
  account_disabled: "账户已停用",
  route_invalid: "路由结构无效",
  credential_unavailable: "凭据不可用",
  authorization_unavailable: "授权不可用",
  extra_usage_not_off: "额外用量未明确关闭",
  evidence_invalid: "引用的调用记录无效或已不匹配",
  selection_invalid: "整组模型选择无效",
};
const evidenceStatusLabels = {
  completed: "调用返回完成，只证明该次调用已返回",
  failed: "调用失败",
  outcome_unknown: "结果未知",
  interrupted_before_dispatch: "派发前中断",
} as const;
const evidenceEnvironmentLabels = {
  live: "真实环境记录",
  simulation: "本地模拟记录",
  unknown: "历史环境未知",
} as const;
const evidenceErrorLabels = {
  auth_invalid: "认证无效",
  entitlement_denied: "权益拒绝",
  quota_exhausted: "额度耗尽",
  rate_limited: "请求限流",
  request_rejected: "请求被拒绝",
  server_error: "服务端错误",
  transport_unknown: "传输结果未知",
  model_transport_unknown: "模型传输结果未知",
  response_incomplete: "响应不完整",
  live_provider_access_pending_confirmation: "真实提供方访问等待确认",
  dispatch_interrupted: "派发中断",
  stale_dispatch: "派发已过期",
  result_seal_failed: "结果封存失败",
  interrupted_before_dispatch: "派发前中断",
  other_failure: "其他失败",
} as const;

const emptyRole = () => ({
  candidates: [] as RolePolicyCandidateInput[],
  required_capabilities: [] as RolePolicyCapability[],
  allowed_spend_classes: [] as RolePolicySpendClass[],
  preference: "candidate_order" as const,
});

function editablePolicy(policy: RoleModelPolicy): RolePolicyInput {
  return {
    roles: Object.fromEntries(
      roles.map((role) => [
        role,
        {
          candidates: (policy.roles[role]?.candidates ?? []).map(
            (candidate) => ({
              profile_id: candidate.profile_id,
              capabilities: [...candidate.capabilities],
              rationale: candidate.rationale,
              ...(candidate.evidence_invocation_id
                ? { evidence_invocation_id: candidate.evidence_invocation_id }
                : {}),
            }),
          ),
          required_capabilities: [
            ...(policy.roles[role]?.required_capabilities ?? []),
          ],
          allowed_spend_classes: [
            ...(policy.roles[role]?.allowed_spend_classes ?? []),
          ],
          preference: policy.roles[role]?.preference ?? "candidate_order",
        },
      ]),
    ) as RolePolicyInput["roles"],
  };
}

const toggle = <T extends string>(values: T[], value: T) =>
  values.includes(value)
    ? values.filter((item) => item !== value)
    : [...values, value];

export function RolePolicySettings({
  profiles,
  disabled,
  onConfirmed,
}: {
  profiles: Profile[];
  disabled: boolean;
  onConfirmed: (settings: Settings) => void;
}) {
  const [state, setState] = useState<RolePolicyState>();
  const [draft, setDraft] = useState<RolePolicyInput>();
  const [proposal, setProposal] = useState<RolePolicyProposal>();
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"save" | "propose" | "confirm" | "">("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const lock = useRef(false);

  useEffect(() => {
    let disposed = false;
    setLoading(true);
    void rolePolicyApi
      .state()
      .then(({ state: next }) => {
        if (disposed) return;
        setState(next);
        setDraft(editablePolicy(next.policy));
        setProposal(next.latest_proposal);
        setDirty(false);
      })
      .catch((cause) => {
        if (!disposed) setError(errorMessage(cause));
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, []);

  const changeDraft = (
    update: (current: RolePolicyInput) => RolePolicyInput,
  ) => {
    setDraft((current) => (current ? update(current) : current));
    setDirty(true);
    if (proposal) {
      setProposal(undefined);
      setNotice("本地编辑已使旧建议失效。请先保存策略版本，再显式生成新建议。");
    } else {
      setNotice("本地编辑尚未保存。请先保存策略版本，再显式生成新建议。");
    }
    setError("");
  };

  const run = async (
    action: "save" | "propose" | "confirm",
    operation: () => Promise<void>,
  ) => {
    if (lock.current || disabled) return;
    lock.current = true;
    setBusy(action);
    setError("");
    if (action !== "save") setNotice("");
    try {
      await operation();
    } finally {
      lock.current = false;
      setBusy("");
    }
  };

  const save = () => {
    if (!state || !draft) return;
    const invalid = roles.some((role) =>
      draft.roles[role].candidates.some(
        (candidate) => !candidate.profile_id || !candidate.rationale.trim(),
      ),
    );
    if (invalid) {
      setError("每个候选都需要已保存连接和非空声明依据。");
      return;
    }
    void run("save", async () => {
      try {
        const { policy } = await rolePolicyApi.save(
          state.policy.revision,
          state.profiles_digest,
          {
            roles: Object.fromEntries(
              roles.map((role) => [
                role,
                {
                  ...draft.roles[role],
                  candidates: draft.roles[role].candidates.map((candidate) => ({
                    ...candidate,
                    rationale: candidate.rationale.trim(),
                    ...(candidate.evidence_invocation_id?.trim()
                      ? {
                          evidence_invocation_id:
                            candidate.evidence_invocation_id.trim(),
                        }
                      : { evidence_invocation_id: undefined }),
                  })),
                },
              ]),
            ) as RolePolicyInput["roles"],
          },
        );
        setState((current) =>
          current
            ? { ...current, policy, latest_proposal: undefined }
            : current,
        );
        setDraft(editablePolicy(policy));
        setProposal(undefined);
        setDirty(false);
        setNotice(
          `策略版本 ${policy.revision} 已保存。保存不应用角色分配；请另行生成并确认建议。`,
        );
      } catch (cause) {
        setError(
          cause instanceof APIError && cause.code === "role_policy_stale"
            ? "策略或连接列表已变化。请重新读取页面后再保存，当前草稿未提交。"
            : errorMessage(cause),
        );
      }
    });
  };

  const propose = () => {
    if (dirty) return;
    void run("propose", async () => {
      try {
        const { proposal: next } = await rolePolicyApi.propose();
        setProposal(next);
        setState((current) =>
          current ? { ...current, latest_proposal: next } : current,
        );
        setNotice(
          next.confirmable
            ? "建议已生成。请展开核对依据；确认仅影响未来任务。"
            : "建议已生成，但七个角色尚未形成完整可选组合。",
        );
      } catch (cause) {
        setError(errorMessage(cause));
      }
    });
  };

  const confirm = () => {
    if (dirty || !proposal?.confirmable) return;
    void run("confirm", async () => {
      try {
        const result = await rolePolicyApi.confirm(proposal);
        setState((current) =>
          current
            ? {
                ...current,
                active_decision:
                  result.settings.model_policy_decision_id ===
                  result.decision.id
                    ? result.decision
                    : current.active_decision?.id ===
                        result.settings.model_policy_decision_id
                      ? current.active_decision
                      : undefined,
                latest_proposal: proposal,
              }
            : current,
        );
        onConfirmed(result.settings);
        setNotice(
          result.applied
            ? "已确认并应用到未来任务。已有任务的冻结来源不会改变。"
            : result.settings.model_policy_decision_id === result.decision.id
              ? "此建议此前已确认；只对账当前活动设置，没有再次应用角色分配。"
              : "此建议此前已确认，但当前设置没有采用它；原决定只作为历史收据，没有再次应用角色分配。",
        );
      } catch (cause) {
        if (cause instanceof APIError) {
          setError(
            cause.code === "role_policy_stale"
              ? "配置已变化，请重新生成；系统没有自动提交或确认另一组模型。"
              : errorMessage(cause),
          );
          return;
        }
        try {
          const [{ state: reconciled }, profileState] = await Promise.all([
            rolePolicyApi.state(),
            api.profiles(),
          ]);
          const decision = reconciled.active_decision;
          setState({
            ...reconciled,
            active_decision:
              decision?.id ===
              profileState.settings.model_policy_decision_id
                ? decision
                : undefined,
          });
          setDraft(editablePolicy(reconciled.policy));
          setProposal(reconciled.latest_proposal);
          setDirty(false);
          onConfirmed(profileState.settings);
          if (
            decision?.proposal_id === proposal.id &&
            profileState.settings.model_policy_decision_id === decision.id
          ) {
            setNotice(
              "确认响应未知；已通过只读对账确认提交结果。没有重发确认请求。",
            );
          } else {
            setError(
              "确认响应未知；一次只读对账未发现匹配决定。请核对当前状态后再决定，不会自动重试。",
            );
          }
        } catch {
          setError("确认响应未知，且一次只读对账失败。不会自动重发确认请求。");
        }
      }
    });
  };

  return (
    <section
      className="role-policy-section"
      aria-labelledby="role-policy-title"
    >
      <div className="section-heading">
        <div>
          <p className="eyebrow">确定性匹配</p>
          <h2 id="role-policy-title">角色策略与建议</h2>
        </div>
        {state?.active_decision && (
          <span className="policy-active">未来任务已启用</span>
        )}
      </div>
      <p className="muted">
        保存策略、生成建议、确认应用是三个独立动作。建议不是上下文、模型调用、工具、执行或交付授权。
      </p>
      {loading && <p role="status">正在读取角色策略…</p>}
      {error && (
        <p className="form-error" role="alert">
          {error}
        </p>
      )}
      {notice && (
        <p className="notice" role="status">
          {notice}
        </p>
      )}
      {state?.active_decision && (
        <p className="policy-source-line">
          当前活动决定 <code>{state.active_decision.id}</code> · 策略版本{" "}
          {state.active_decision.proposal.policy_revision} ·
          仅影响之后创建的任务
        </p>
      )}
      {state && draft && (
        <fieldset disabled={disabled || !!busy}>
          <legend className="sr-only">七角色模型策略</legend>
          <p className="muted small">
            策略版本 {state.policy.revision} ·
            候选顺序是优先级，不是自动失败回退顺序
          </p>
          <div className="policy-role-list">
            {roles.map((role) => {
              const item = draft.roles[role] ?? emptyRole();
              return (
                <details className="policy-role" key={role}>
                  <summary
                    aria-label={`编辑 ${roleNames[role]} 策略，${item.candidates.length} 个候选`}
                  >
                    <strong>{roleNames[role]}</strong> ·{" "}
                    {item.candidates.length} 个候选
                  </summary>
                  <div className="policy-role-editor">
                    <fieldset>
                      <legend>要求能力（用户声明筛选）</legend>
                      <div className="policy-check-grid">
                        {rolePolicyCapabilities.map((capability) => (
                          <label className="check-label" key={capability}>
                            <input
                              type="checkbox"
                              aria-label={`${roleNames[role]} 要求能力：${capabilityLabels[capability]}`}
                              checked={item.required_capabilities.includes(
                                capability,
                              )}
                              onChange={() =>
                                changeDraft((current) => ({
                                  roles: {
                                    ...current.roles,
                                    [role]: {
                                      ...item,
                                      required_capabilities: toggle(
                                        item.required_capabilities,
                                        capability,
                                      ),
                                    },
                                  },
                                }))
                              }
                            />
                            {capabilityLabels[capability]}
                          </label>
                        ))}
                      </div>
                    </fieldset>
                    <fieldset>
                      <legend>允许费用渠道</legend>
                      <div className="policy-check-grid">
                        {rolePolicySpendClasses.map((spendClass) => (
                          <label className="check-label" key={spendClass}>
                            <input
                              type="checkbox"
                              aria-label={`${roleNames[role]} 允许费用：${spendLabels[spendClass]}`}
                              checked={item.allowed_spend_classes.includes(
                                spendClass,
                              )}
                              onChange={() =>
                                changeDraft((current) => ({
                                  roles: {
                                    ...current.roles,
                                    [role]: {
                                      ...item,
                                      allowed_spend_classes: toggle(
                                        item.allowed_spend_classes,
                                        spendClass,
                                      ),
                                    },
                                  },
                                }))
                              }
                            />
                            {spendLabels[spendClass]}
                          </label>
                        ))}
                      </div>
                    </fieldset>
                    <label htmlFor={`policy-preference-${role}`}>
                      排序偏好
                    </label>
                    <select
                      id={`policy-preference-${role}`}
                      aria-label={`${roleNames[role]} 排序偏好`}
                      value={item.preference}
                      onChange={(event) =>
                        changeDraft((current) => ({
                          roles: {
                            ...current.roles,
                            [role]: {
                              ...item,
                              preference: event.target
                                .value as typeof item.preference,
                            },
                          },
                        }))
                      }
                    >
                      <option value="candidate_order">按候选顺序</option>
                      <option value="subscription_first">
                        允许集合内订阅优先
                      </option>
                    </select>
                    <div className="policy-candidates">
                      {item.candidates.map((candidate, index) => (
                        <CandidateEditor
                          key={`${role}-${index}`}
                          role={role}
                          index={index}
                          candidate={candidate}
                          profiles={profiles}
                          onChange={(next) =>
                            changeDraft((current) => ({
                              roles: {
                                ...current.roles,
                                [role]: {
                                  ...item,
                                  candidates: item.candidates.map(
                                    (value, candidateIndex) =>
                                      candidateIndex === index ? next : value,
                                  ),
                                },
                              },
                            }))
                          }
                          onRemove={() =>
                            changeDraft((current) => ({
                              roles: {
                                ...current.roles,
                                [role]: {
                                  ...item,
                                  candidates: item.candidates.filter(
                                    (_, candidateIndex) =>
                                      candidateIndex !== index,
                                  ),
                                },
                              },
                            }))
                          }
                        />
                      ))}
                    </div>
                    <button
                      type="button"
                      disabled={item.candidates.length >= 8}
                      onClick={() =>
                        changeDraft((current) => ({
                          roles: {
                            ...current.roles,
                            [role]: {
                              ...item,
                              candidates: [
                                ...item.candidates,
                                {
                                  profile_id: "",
                                  capabilities: [],
                                  rationale: "",
                                },
                              ],
                            },
                          },
                        }))
                      }
                    >
                      为 {roleNames[role]} 添加候选
                    </button>
                  </div>
                </details>
              );
            })}
          </div>
          <div className="actions policy-actions">
            <button className="primary" type="button" onClick={save}>
              {busy === "save" ? "正在保存…" : "保存策略版本"}
            </button>
            <button type="button" disabled={dirty} onClick={propose}>
              {busy === "propose"
                ? "正在生成…"
                : error.includes("重新生成")
                  ? "重新生成建议"
                  : "生成建议"}
            </button>
          </div>
        </fieldset>
      )}
      {proposal && (
        <ProposalView
          proposal={proposal}
          profiles={profiles}
          disabled={disabled || !!busy || dirty}
          confirming={busy === "confirm"}
          onConfirm={confirm}
        />
      )}
    </section>
  );
}

function CandidateEditor({
  role,
  index,
  candidate,
  profiles,
  onChange,
  onRemove,
}: {
  role: Role;
  index: number;
  candidate: RolePolicyCandidateInput;
  profiles: Profile[];
  onChange: (candidate: RolePolicyCandidateInput) => void;
  onRemove: () => void;
}) {
  const label = `${roleNames[role]} 候选 ${index + 1}`;
  const missing =
    candidate.profile_id &&
    !profiles.some((profile) => profile.id === candidate.profile_id);
  return (
    <section className="policy-candidate-editor" aria-label={label}>
      <div className="section-heading">
        <strong>{label}</strong>
        <button type="button" onClick={onRemove}>
          移除
        </button>
      </div>
      <label htmlFor={`policy-profile-${role}-${index}`}>连接</label>
      <select
        id={`policy-profile-${role}-${index}`}
        aria-label={`${label} 连接`}
        value={candidate.profile_id}
        onChange={(event) =>
          onChange({ ...candidate, profile_id: event.target.value })
        }
      >
        <option value="">选择已保存连接</option>
        {profiles.map((profile) => (
          <option value={profile.id} key={profile.id}>
            {profile.name} · {profile.model}
          </option>
        ))}
        {missing && (
          <option value={candidate.profile_id}>
            {candidate.profile_id}（当前缺失）
          </option>
        )}
      </select>
      <fieldset>
        <legend>能力声明</legend>
        <div className="policy-check-grid">
          {rolePolicyCapabilities.map((capability) => (
            <label className="check-label" key={capability}>
              <input
                type="checkbox"
                aria-label={`${label} 声明能力：${capabilityLabels[capability]}`}
                checked={candidate.capabilities.includes(capability)}
                onChange={() =>
                  onChange({
                    ...candidate,
                    capabilities: toggle(candidate.capabilities, capability),
                  })
                }
              />
              {capabilityLabels[capability]}
            </label>
          ))}
        </div>
      </fieldset>
      <p className="field-help">能力是用户声明，未独立验证，也不是运行授权。</p>
      <label htmlFor={`policy-rationale-${role}-${index}`}>声明依据</label>
      <textarea
        id={`policy-rationale-${role}-${index}`}
        aria-label={`${label} 声明依据`}
        value={candidate.rationale}
        maxLength={500}
        rows={2}
        required
        onChange={(event) =>
          onChange({ ...candidate, rationale: event.target.value })
        }
      />
      <label htmlFor={`policy-evidence-${role}-${index}`}>
        调用记录 ID（可选）
      </label>
      <input
        id={`policy-evidence-${role}-${index}`}
        aria-label={`${label} 调用记录 ID`}
        value={candidate.evidence_invocation_id ?? ""}
        onChange={(event) =>
          onChange({
            ...candidate,
            ...(event.target.value
              ? { evidence_invocation_id: event.target.value }
              : { evidence_invocation_id: undefined }),
          })
        }
      />
      <p className="field-help">
        调用记录只说明一次调用的终态，不证明质量、未来额度或模型能力。
      </p>
    </section>
  );
}

function ProposalView({
  proposal,
  profiles,
  disabled,
  confirming,
  onConfirm,
}: {
  proposal: RolePolicyProposal;
  profiles: Profile[];
  disabled: boolean;
  confirming: boolean;
  onConfirm: () => void;
}) {
  return (
    <section className="policy-proposal" aria-label="角色模型建议">
      <div className="section-heading">
        <div>
          <h3>建议依据</h3>
          <p className="muted small">
            策略版本 {proposal.policy_revision} · <code>{proposal.id}</code>
          </p>
        </div>
        <strong>
          {proposal.confirmable
            ? "七个角色均有可选建议"
            : "建议不完整，不能确认"}
        </strong>
      </div>
      <div className="policy-result-list">
        {roles.map((role) => {
          const result = proposal.roles[role];
          return (
            <details
              className="policy-result"
              key={role}
              open={!result?.selected_profile_id}
            >
              <summary>
                <strong>{roleNames[role]}</strong> ·{" "}
                {result?.selected_profile_id || "没有可选建议"}
              </summary>
              {!result?.candidates?.length && (
                <p className="muted">没有候选；这不是默认连接。</p>
              )}
              {(result?.candidates ?? []).map((candidate) => {
                const profileName = profiles.find(
                  (item) => item.id === candidate.profile_id,
                )?.name;
                return (
                  <article
                    className="policy-candidate-result"
                    key={candidate.profile_id}
                  >
                    <div className="section-heading">
                      <strong>{profileName ?? "当前缺失的连接"}</strong>
                      <span
                        className={
                          candidate.eligible
                            ? "result-eligible"
                            : "result-ineligible"
                        }
                      >
                        {candidate.eligible ? "符合当前声明约束" : "已排除"}
                      </span>
                    </div>
                    <code>{candidate.profile_id}</code>
                    <p>{candidate.rationale}</p>
                    <p className="field-help">
                      用户声明，未独立验证：
                      {candidate.capabilities
                        .map((value) => capabilityLabels[value])
                        .join("、") || "没有能力声明"}
                    </p>
                    <p className="field-help">
                      费用渠道：{spendLabels[candidate.spend_class]}
                    </p>
                    {!!candidate.reasons.length && (
                      <ul className="policy-reasons">
                        {candidate.reasons.map((reason) => (
                          <li key={reason}>{reasonLabels[reason]}</li>
                        ))}
                      </ul>
                    )}
                    <p className="field-help">
                      {candidate.execution_notice ===
                      "production_live_gate_closed"
                        ? "真实运行门禁关闭；配置有效不等于获准调用"
                        : "凭据与额度未知；不会推断可用或零费用"}
                    </p>
                    {candidate.evidence ? (
                      <div className="policy-evidence">
                        <strong>
                          {evidenceStatusLabels[candidate.evidence.status]}
                          {candidate.evidence.error_code
                            ? ` · ${evidenceErrorLabels[candidate.evidence.error_code]}`
                            : ""}
                        </strong>
                        <span>
                          {
                            evidenceEnvironmentLabels[
                              candidate.evidence.environment
                            ]
                          }{" "}
                          · {candidate.evidence.finished_at}
                        </span>
                        <code>
                          任务 {candidate.evidence.mission_id} · 调用{" "}
                          {candidate.evidence.invocation_id}
                        </code>
                        <small>这是调用收据，不是角色验收或能力验证。</small>
                      </div>
                    ) : (
                      <p className="field-help">
                        无运行记录；不表示失败或成功。
                      </p>
                    )}
                  </article>
                );
              })}
            </details>
          );
        })}
      </div>
      <p className="note">
        确认只改变未来任务的七角色分配；现有任务、审批与预算保持原样。
      </p>
      <button
        className="primary"
        type="button"
        disabled={disabled || !proposal.confirmable}
        onClick={onConfirm}
      >
        {confirming ? "正在确认…" : "确认并用于未来任务"}
      </button>
    </section>
  );
}
