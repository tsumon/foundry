import {
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { api, APIError, errorMessage } from "../api";
import {
  hasC4,
  c4EventLabel,
  c4AdvanceAllowed,
  isTerminalModelBackedDelivery,
} from "../c4";
import { C4Decision } from "./C4Verification";
import {
  activeStates,
  isControlledMCPReadMission,
  isControlledReadMission,
  isSupportedMissionMode,
  type Mission,
  type LocalOperation,
  type PlanRevision,
  type MissionAction,
  type RunEvent,
} from "../types";
import { missionStateName } from "../deliveryStatus";
import { StageRail } from "./StageRail";
import { DecisionPanel } from "./DecisionPanel";
import { ReplanSummary } from "./ReplanSummary";
import { MissionViews, views, type MissionView } from "./MissionViews";
import type { MissionProjectContext } from "../projectContext";
import {
  createPlanDraft,
  planListLines,
  PlanRevisionEditor,
  type PlanDraft,
} from "./PlanRevisionEditor";
import { HookRecords } from "./hooks/HookRecords";
import { InvocationActivity } from "./InvocationActivity";
import { MCPDecisionCard, type MCPUnresolvedDecision } from "./MCPDecisionCard";
import { MCPInvocationActivity } from "./MCPInvocationActivity";
import {
  accountActionBlocked,
  accountActionBlockReason,
  invalidModelSnapshot,
} from "../modelSnapshot";

export interface MissionSession {
  draft: PlanDraft;
  busy: boolean;
  saveError: string;
  inFlight: { current: boolean };
  unresolvedMCPDecision: MCPUnresolvedDecision | null;
}

export const createMissionSession = (mission: Mission): MissionSession => ({
  draft: createPlanDraft(mission),
  busy: false,
  saveError: "",
  inFlight: { current: false },
  unresolvedMCPDecision: null,
});

export function MissionWorkspace({
  mission,
  onUpdate,
  demo = false,
  events = [],
  stream = "未连接",
  initialDecisionOpen = false,
  initialView = "计划链路",
  projectContext,
  onOpenProject,
  onBack,
  backLabel,
  session,
  onSessionChange,
  hooksEnabled = false,
}: {
  mission: Mission;
  onUpdate: (mission: Mission) => void;
  demo?: boolean;
  events?: RunEvent[];
  stream?: string;
  initialDecisionOpen?: boolean;
  initialView?: MissionView;
  projectContext?: MissionProjectContext;
  onOpenProject?: (projectId: string) => void;
  onBack?: () => void;
  backLabel?: string;
  session?: MissionSession;
  onSessionChange?: Dispatch<SetStateAction<MissionSession>>;
  hooksEnabled?: boolean;
}) {
  const [view, setView] = useState<MissionView>(initialView);
  const [c4Availability, setC4Availability] = useState({
    id: "",
    revision: 0,
    available: false,
  });
  const c4Available =
    c4Availability.id === mission.id &&
    c4Availability.revision === mission.revision &&
    c4Availability.available;
  const [c4ExecutionAvailability, setC4ExecutionAvailability] = useState({
    id: "",
    revision: 0,
    available: false,
  });
  const c4ExecutionAvailable =
    mission.spec.c4?.control_version !== 1
      ? c4Available
      : c4ExecutionAvailability.id === mission.id &&
        c4ExecutionAvailability.revision === mission.revision &&
        c4ExecutionAvailability.available;
  const [localSession, setLocalSession] = useState(() =>
    createMissionSession(mission),
  );
  const { draft, busy, saveError, inFlight, unresolvedMCPDecision } =
    session ?? localSession;
  const setSession = onSessionChange ?? setLocalSession;
  const setBusy = (value: boolean) =>
    setSession((current) => ({ ...current, busy: value }));
  const setSaveError = (value: string) =>
    setSession((current) => ({ ...current, saveError: value }));
  const setUnresolvedMCPDecision = (value: MCPUnresolvedDecision | null) =>
    setSession((current) => ({
      ...current,
      unresolvedMCPDecision: value,
    }));
  const setDraft: Dispatch<SetStateAction<PlanDraft>> = (value) =>
    setSession((current) => ({
      ...current,
      draft: typeof value === "function" ? value(current.draft) : value,
    }));
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [drawer, setDrawer] = useState(initialDecisionOpen);
  const [history, setHistory] = useState<PlanRevision[]>([]);
  const [historyLoading, setHistoryLoading] = useState(!demo);
  const [historyError, setHistoryError] = useState("");
  const [operations, setOperations] = useState<LocalOperation[]>([]);
  const [operationsLoading, setOperationsLoading] = useState(!demo);
  const [operationsError, setOperationsError] = useState("");
  const [activityOpen, setActivityOpen] = useState(false);
  const invocationEventSequence = events.reduce(
    (latest, event) =>
      ["invocation.", "mcp.", "research."].some((prefix) =>
        event.kind.startsWith(prefix),
      )
        ? Math.max(latest, event.seq)
        : latest,
    0,
  );
  const drawerTrigger = useRef<HTMLButtonElement>(null);
  const drawerRegion = useRef<HTMLElement>(null);
  const isNarrow = () =>
    window.matchMedia?.("(max-width: 1179px)").matches ?? false;
  useEffect(() => {
    if (drawer && isNarrow()) drawerRegion.current?.focus();
  }, [drawer]);
  useEffect(() => {
    setDraft((current) => {
      const currentPlanRevision = mission.plan?.revision ?? 0;
      if (current.missionID !== mission.id) return createPlanDraft(mission);
      // A save response may advance the draft before its mission prop catches
      // up, or arrive after a newer SSE snapshot. Never roll either backward.
      if (current.expectedMissionRevision > mission.revision) return current;
      if (
        current.expectedMissionRevision !== mission.revision ||
        current.expectedPlanRevision !== currentPlanRevision
      ) {
        if (!current.dirty && !current.stale && !current.reason)
          return createPlanDraft(mission);
        return current.stale ? current : { ...current, stale: true };
      }
      return current;
    });
  }, [
    mission.id,
    mission.plan?.revision,
    mission.revision,
    draft.expectedMissionRevision,
    draft.expectedPlanRevision,
  ]);
  useEffect(() => {
    if (demo) return;
    let disposed = false;
    setHistoryLoading(true);
    setHistoryError("");
    void api
      .planRevisions(mission.id)
      .then(({ plan_revisions }) => {
        if (!disposed) setHistory(plan_revisions ?? []);
      })
      .catch((cause) => {
        if (!disposed) setHistoryError(errorMessage(cause));
      })
      .finally(() => {
        if (!disposed) setHistoryLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [demo, mission.id, mission.revision]);
  useEffect(() => {
    if (demo) return;
    let disposed = false;
    setOperationsLoading(true);
    setOperationsError("");
    void api
      .localOperations(mission.id)
      .then(({ local_operations }) => {
        if (!disposed) setOperations(local_operations ?? []);
      })
      .catch((cause) => {
        if (!disposed) setOperationsError(errorMessage(cause));
      })
      .finally(() => {
        if (!disposed) setOperationsLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [demo, mission.id, mission.revision]);
  const closeDrawer = () => {
    setDrawer(false);
    if (isNarrow()) drawerTrigger.current?.focus();
  };
  const authorized = mission.approvals?.some((a) => a.kind === "context");
  const controlled = isControlledReadMission(mission);
  const mcpControlled = isControlledMCPReadMission(mission);
  const showModelInvocations =
    controlled ||
    (mission.workspace_mode === "generations_account_snapshot_v2" &&
      isSupportedMissionMode(mission) &&
      !invalidModelSnapshot(mission));
  const unsupportedWorkspaceMode =
    !isSupportedMissionMode(mission) || invalidModelSnapshot(mission);
  const terminalModelDelivery = isTerminalModelBackedDelivery(mission);
  const canChat =
    !demo &&
    authorized &&
    mission.state !== "draft" &&
    !activeStates.includes(mission.state) &&
    !terminalModelDelivery &&
    !unsupportedWorkspaceMode &&
    !accountActionBlocked(mission, "chat") &&
    (!hasC4(mission) || (c4Available && c4AdvanceAllowed(mission))) &&
    !busy;
  const act = async (action: MissionAction, controlledConsent?: true) => {
    if (
      demo ||
      unsupportedWorkspaceMode ||
      (action === "chat" && terminalModelDelivery) ||
      accountActionBlocked(mission, action) ||
      (action === "approve" && hasC4(mission) && !c4ExecutionAvailable) ||
      (hasC4(mission) &&
        action !== "pause" &&
        (!c4Available || !c4AdvanceAllowed(mission))) ||
      inFlight.current
    )
      return;
    if (
      action === "chat" &&
      new TextEncoder().encode(message.trim()).byteLength > 8000
    ) {
      setError("消息不能超过 8000 UTF-8 字节，请缩短后发送。");
      return;
    }
    inFlight.current = true;
    setBusy(true);
    setError("");
    try {
      const result =
        action === "plan"
          ? await api.plan(mission.id, mission.revision, controlledConsent)
          : await api.action(
              mission.id,
              action,
              mission.revision,
              action === "chat" ? message.trim() : undefined,
            );
      onUpdate(result.mission);
      if (action === "chat") setMessage("");
    } catch (e) {
      if (e instanceof APIError && e.status === 409) {
        setError(
          "版本已变化或控制器忙碌。已请求刷新；请重新检查后再决定，本次操作未自动重试。",
        );
        try {
          onUpdate((await api.mission(mission.id)).mission);
        } catch (refreshError) {
          setError(
            `冲突后刷新失败：${errorMessage(refreshError)}。请重新连接后检查任务。`,
          );
        }
      } else setError(errorMessage(e));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };
  const savePlanRevision = async () => {
    if (demo || unsupportedWorkspaceMode || inFlight.current || !mission.plan)
      return;
    inFlight.current = true;
    setBusy(true);
    setSaveError("");
    try {
      const result = await api.revisePlan(mission.id, {
        expected_mission_revision: draft.expectedMissionRevision,
        expected_plan_revision: draft.expectedPlanRevision,
        reason: draft.reason.trim(),
        plan: {
          ...(draft.plan.adjustments
            ? { adjustments: draft.plan.adjustments }
            : {}),
          summary: draft.plan.summary,
          steps: draft.plan.steps.map((step) => ({
            id: step.id,
            title: step.title,
            description: step.description,
            depends_on: planListLines(step.depends_on.join("\n")),
            files: planListLines(step.files.join("\n")),
          })),
        },
      });
      setDraft(createPlanDraft(result.mission));
      setHistory((current) => {
        const without = current.filter(
          (item) =>
            item.epoch !== result.plan_revision.epoch ||
            item.plan_revision !== result.plan_revision.plan_revision,
        );
        return [...without, result.plan_revision].sort(
          (a, b) => a.epoch - b.epoch || a.plan_revision - b.plan_revision,
        );
      });
      onUpdate(result.mission);
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 409) {
        setDraft((current) => ({ ...current, stale: true }));
        setSaveError(
          "版本冲突：草稿已保留，且未自动重送。请核对最新任务后丢弃或重新基于当前版本编辑。",
        );
        try {
          onUpdate((await api.mission(mission.id)).mission);
        } catch (refreshError) {
          setSaveError(
            `版本冲突且刷新失败：${errorMessage(refreshError)}。草稿仍保留，请重新连接后检查。`,
          );
        }
      } else setSaveError(errorMessage(cause));
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };
  const discardDraft = () => {
    setSaveError("");
    setDraft(createPlanDraft(mission));
  };
  const rebaseDraft = () => {
    if (!mission.plan) return;
    const current = createPlanDraft(mission);
    setSaveError("");
    setDraft((previous) => ({
      ...previous,
      key: current.key,
      expectedMissionRevision: current.expectedMissionRevision,
      expectedPlanRevision: current.expectedPlanRevision,
      originalPlan: current.originalPlan,
      stale: false,
      dirty: true,
    }));
  };
  return (
    <div
      className="workspace"
      onKeyDown={(event) => {
        if (event.key === "Escape" && drawer && isNarrow()) {
          event.preventDefault();
          closeDrawer();
        }
      }}
    >
      <section className="workspace-center">
        <header className="mission-header">
          {(onBack || projectContext) && (
            <div className="mission-context-navigation">
              {onBack && (
                <button onClick={onBack}>{backLabel ?? "返回"}</button>
              )}
              {projectContext &&
                ("projectId" in projectContext &&
                "projectName" in projectContext &&
                projectContext.projectName &&
                onOpenProject ? (
                  <button
                    className="text-button"
                    onClick={() => onOpenProject(projectContext.projectId!)}
                    aria-label={`打开项目 ${projectContext.projectName}`}
                  >
                    任务项目：{projectContext.label}
                  </button>
                ) : (
                  <span className="project-context">
                    任务项目：{projectContext.label}
                  </span>
                ))}
            </div>
          )}
          <div className="eyebrow">
            {demo ? "设计预览" : "项目"} / 交付任务{" "}
            <span
              className={`status ${mission.state.includes("awaiting") ? "warning" : ""}`}
            >
              {missionStateName(mission)}
            </span>
          </div>
          <h1>{mission.title}</h1>
          <p className="muted mission-goal">{mission.goal}</p>
          <StageRail stage={mission.stage} />
          <button
            ref={drawerTrigger}
            className="drawer-toggle"
            aria-expanded={drawer}
            aria-controls="decision-drawer"
            onClick={() => (drawer ? closeDrawer() : setDrawer(true))}
          >
            决策与 Main Agent {drawer ? "−" : "＋"}
          </button>
        </header>
        <div className="view-tabs" role="tablist" aria-label="任务视角">
          {views.map((name, index) => (
            <button
              key={name}
              id={`view-${index}`}
              role="tab"
              aria-selected={view === name}
              aria-controls="view-content"
              tabIndex={view === name ? 0 : -1}
              onClick={() => setView(name)}
              onKeyDown={(event) => {
                if (
                  ["ArrowRight", "ArrowLeft", "Home", "End"].includes(event.key)
                ) {
                  event.preventDefault();
                  const next =
                    event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? views.length - 1
                        : (index +
                            (event.key === "ArrowRight" ? 1 : -1) +
                            views.length) %
                          views.length;
                  setView(views[next]);
                  document.getElementById(`view-${next}`)?.focus();
                }
              }}
            >
              {name}
            </button>
          ))}
        </div>
        {(error || mission.error) && (
          <div className="workspace-error" role="alert">
            {error || mission.error}
          </div>
        )}
        <section
          id="view-content"
          role="tabpanel"
          aria-labelledby={`view-${views.indexOf(view)}`}
          className="view-content"
          tabIndex={0}
        >
          {view === "计划链路" && <ReplanSummary mission={mission} />}
          <MissionViews
            mission={mission}
            onChanged={onUpdate}
            busy={busy}
            onOpenDecision={() => setDrawer(true)}
            view={view}
            demo={demo}
            operations={operations}
            operationsLoading={operationsLoading}
            operationsError={operationsError}
            planEditor={
              !demo && mission.plan ? (
                <PlanRevisionEditor
                  mission={mission}
                  draft={draft}
                  history={history}
                  historyLoading={historyLoading}
                  historyError={historyError}
                  busy={busy}
                  saveError={saveError}
                  onDraftChange={setDraft}
                  onSave={() => void savePlanRevision()}
                  onDiscard={discardDraft}
                  onRebase={rebaseDraft}
                />
              ) : undefined
            }
            mcpDecision={
              !demo && mcpControlled ? (
                <MCPDecisionCard
                  mission={mission}
                  busy={busy}
                  inFlight={inFlight}
                  unresolved={unresolvedMCPDecision}
                  onUnresolvedChange={setUnresolvedMCPDecision}
                  onBusyChange={setBusy}
                  onUpdate={onUpdate}
                  eventSequence={invocationEventSequence}
                />
              ) : undefined
            }
          />
        </section>
      </section>
      <aside
        ref={drawerRegion}
        id="decision-drawer"
        role="region"
        aria-label="决策与 Main Agent"
        tabIndex={-1}
        className={`decision-column ${drawer ? "drawer-open" : ""}`}
      >
        <DecisionPanel
          mission={mission}
          busy={busy}
          demo={demo}
          onOpenMCPApproval={() => {
            setView("审批");
            if (isNarrow()) closeDrawer();
          }}
          onAction={(action, controlledConsent) =>
            void act(action, controlledConsent)
          }
          planApprovalBlocked={draft.dirty || draft.stale}
          unsupportedWorkspaceMode={unsupportedWorkspaceMode}
          c4Available={c4Available}
          c4ExecutionAvailable={c4ExecutionAvailable}
          c4Decision={
            !demo && hasC4(mission) ? (
              <C4Decision
                mission={mission}
                busy={busy || unsupportedWorkspaceMode}
                onUpdate={onUpdate}
                inFlight={inFlight}
                onBusyChange={setBusy}
                onAvailabilityChange={(available) =>
                  setC4Availability({
                    id: mission.id,
                    revision: mission.revision,
                    available,
                  })
                }
                onExecutionAvailabilityChange={(available) =>
                  setC4ExecutionAvailability({
                    id: mission.id,
                    revision: mission.revision,
                    available,
                  })
                }
              />
            ) : undefined
          }
        />
        <section className="main-agent">
          <div className="section-heading">
            <h2>Main Agent</h2>
            <span className="tag">只读问答</span>
          </div>
          <div className="agent-identity">
            <span className="agent-avatar" aria-hidden="true">
              F
            </span>
            <div>
              <strong>Foundry Agent</strong>
              <small>{mission.profiles?.main?.model ?? "未配置"}</small>
            </div>
          </div>
          <p className="muted">
            解释当前计划、证据与阻塞。你的消息不会自动批准执行或交付。
          </p>
          {mission.messages?.length > 0 && (
            <p className="agent-last-message">
              {mission.messages.at(-1)?.content}
            </p>
          )}
          {!demo && (
            <form
              onSubmit={(event) => {
                event.preventDefault();
                if (canChat && message.trim()) void act("chat");
              }}
            >
              <label htmlFor="main-question">向 Main 提问</label>
              <textarea
                id="main-question"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                disabled={!canChat}
                placeholder={
                  authorized
                    ? "询问计划、范围或验证证据…"
                    : "授权上下文后可开始问答"
                }
                rows={3}
                maxLength={10000}
              />
              <button
                className="full"
                type="submit"
                disabled={!canChat || !message.trim()}
              >
                发送消息
              </button>
            </form>
          )}
          {!demo && accountActionBlocked(mission, "chat") && (
            <p className="warning-copy">
              Main 的账户调用已禁用：
              {accountActionBlockReason(mission, "chat")}
              历史消息保持可读，不会切换其他模型。
            </p>
          )}
          {!demo && terminalModelDelivery && (
            <p className="warning-copy">
              已交付；如需继续执行请新建任务，历史证据保持不变
            </p>
          )}
        </section>
      </aside>
      <details
        className="activity-bar"
        onToggle={(event) => setActivityOpen(event.currentTarget.open)}
      >
        <summary>
          <strong>⌁ 运行活动</strong>
          <span>{demo ? "演示 · 无实际运行" : stream}</span>
          <span className="muted">
            {missionStateName(mission)} · r{mission.revision}
          </span>
        </summary>
        <div className="activity-content">
          {!events.length && (
            <p className="muted">
              {demo
                ? "此视图不产生运行事件。"
                : "尚未收到此任务的事件。事件流仅记录状态变化，不是命令日志。"}
            </p>
          )}
          {events.map((event) => (
            <div className="event-row" key={event.seq}>
              <code>#{event.seq}</code>
              <span>{c4EventLabel(event.kind)}</span>
              <span>r{event.revision}</span>
              <time>{event.created_at}</time>
            </div>
          ))}
          {activityOpen && !demo && mission.spec.c4?.control_version !== 1 && (
            <HookRecords
              missionID={mission.id}
              enabled={hooksEnabled}
              visible={activityOpen}
            />
          )}
          {showModelInvocations && !demo && (
            <InvocationActivity
              mission={mission}
              visible={activityOpen}
              eventSequence={invocationEventSequence}
            />
          )}
          {mcpControlled && !demo && (
            <MCPInvocationActivity
              mission={mission}
              visible={activityOpen}
              eventSequence={invocationEventSequence}
            />
          )}
        </div>
      </details>
    </div>
  );
}
