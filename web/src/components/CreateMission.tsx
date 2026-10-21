import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, errorMessage } from "../api";
import { capabilityApi } from "../capabilityApi";
import { mcpTaskApi } from "../mcpTaskApi";
import type { CapabilitySnapshot } from "../capabilityTypes";
import {
  roles,
  roleNames,
  type LocalMCPTest,
  type Mission,
  type Profile,
  type Role,
  type Settings,
  type RoleModelSelection,
} from "../types";
import { ModelSnapshot } from "./ModelSnapshot";
import { rolePolicyApi } from "../rolePolicyApi";
import type { RolePolicyState } from "../rolePolicyTypes";
import { ImportHistory, RepositoryImport } from "./RepositoryImport";
import type { RepositoryInspection } from "../repositoryImportTypes";
import type { GitRepository } from "../capabilityTypes";
const lines = (text: string) =>
  text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
export function CreateMission({
  onCancel,
  onCreated,
  initialRepository,
}: {
  onCancel: () => void;
  onCreated: (mission: Mission) => void;
  initialRepository?: GitRepository;
}) {
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [profileID, setProfileID] = useState("");
  const [modelSettings, setModelSettings] = useState<Settings>({
    default_profile_id: "",
    role_bindings: {},
  });
  const [rolePolicyState, setRolePolicyState] = useState<RolePolicyState>();
  const [rolePolicyLoading, setRolePolicyLoading] = useState(true);
  const [rolePolicyError, setRolePolicyError] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [healthLoading, setHealthLoading] = useState(true);
  const [healthError, setHealthError] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [controlledAvailable, setControlledAvailable] = useState(false);
  const [taskLocalMCPEnabled, setTaskLocalMCPEnabled] = useState(false);
  const [localMCP, setLocalMCP] = useState<LocalMCPTest | null>(null);
  const [localMCPLoading, setLocalMCPLoading] = useState(false);
  const [localMCPError, setLocalMCPError] = useState("");
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const [title, setTitle] = useState("");
  const [goal, setGoal] = useState("");
  const [repo, setRepo] = useState(initialRepository?.path ?? "");
  const [importRepository, setImportRepository] = useState<
    GitRepository | undefined
  >(initialRepository);
  const [repositories, setRepositories] = useState<GitRepository[]>([]);
  const [registryError, setRegistryError] = useState("");
  const [scope, setScope] = useState("");
  const [protectedPaths, setProtectedPaths] = useState("");
  const [commands, setCommands] = useState("");
  const [image, setImage] = useState("");
  const [budget, setBudget] = useState(16);
  const [research, setResearch] = useState(false);
  const [recoverable, setRecoverable] = useState(true);
  const [controlled, setControlled] = useState(false);
  const [mcpControlled, setMCPControlled] = useState(false);
  const controlledMode = controlled || mcpControlled;
  const importing = recoverable && !controlledMode;
  const [importAttempt, setImportAttempt] = useState(0);
  const importKey = JSON.stringify([
    importAttempt,
    importing,
    repo,
    scope,
    protectedPaths,
    commands,
    image,
    importRepository,
  ]);
  const importEpoch = useRef({ key: importKey, epoch: 0 });
  if (importEpoch.current.key !== importKey)
    importEpoch.current = {
      key: importKey,
      epoch: importEpoch.current.epoch + 1,
    };
  const epoch = importEpoch.current.epoch;
  const [inspection, setInspection] = useState<{
    epoch: number;
    receipt: RepositoryInspection;
  } | null>(null);
  const selectedInspection =
    importing && inspection?.epoch === epoch ? inspection.receipt : null;
  useEffect(() => {
    let alive = true;
    void capabilityApi
      .snapshot()
      .then((data) => {
        if (alive) setRepositories(data.repositories ?? []);
      })
      .catch((cause) => {
        if (alive) setRegistryError(errorMessage(cause));
      });
    return () => {
      alive = false;
    };
  }, []);
  const [capabilities, setCapabilities] = useState<CapabilitySnapshot | null>(
    null,
  );
  const [capabilityLoading, setCapabilityLoading] = useState(false);
  const [capabilityError, setCapabilityError] = useState("");
  const [sourceKey, setSourceKey] = useState("");
  const [skillRows, setSkillRows] = useState<
    Array<{ key: number; skillKey: string; role: Role }>
  >([]);
  const nextSkillKey = useRef(1);
  const capabilityEpoch = useRef(0);
  useEffect(() => {
    let disposed = false;
    void api
      .profiles()
      .then((data) => {
        if (!disposed) {
          setProfiles(data.profiles ?? []);
          setProfileID(data.settings.default_profile_id);
          setModelSettings({
            ...data.settings,
            role_bindings: data.settings.role_bindings ?? {},
          });
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
  }, []);
  useEffect(() => {
    let disposed = false;
    void rolePolicyApi
      .state()
      .then(({ state }) => {
        if (!disposed) setRolePolicyState(state);
      })
      .catch((cause) => {
        if (!disposed) setRolePolicyError(errorMessage(cause));
      })
      .finally(() => {
        if (!disposed) setRolePolicyLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, []);
  const readHealth = useCallback(async () => {
    setHealthLoading(true);
    setHealthError("");
    try {
      const data = await api.health();
      setReadOnly(data.read_only === true);
      setControlledAvailable(
        data.capabilities.controlled_read_creation === true,
      );
      setTaskLocalMCPEnabled(data.capabilities.task_local_mcp === true);
    } catch (cause) {
      setControlledAvailable(false);
      setTaskLocalMCPEnabled(false);
      setHealthError(errorMessage(cause));
    } finally {
      setHealthLoading(false);
    }
  }, []);
  useEffect(() => {
    void readHealth();
  }, [readHealth]);
  const readLocalMCP = useCallback(async () => {
    setLocalMCPLoading(true);
    setLocalMCPError("");
    try {
      setLocalMCP(await mcpTaskApi.localTest());
    } catch (cause) {
      setLocalMCP(null);
      setLocalMCPError(errorMessage(cause));
    } finally {
      setLocalMCPLoading(false);
    }
  }, []);
  useEffect(() => {
    if (taskLocalMCPEnabled) void readLocalMCP();
    else setLocalMCP(null);
  }, [readLocalMCP, taskLocalMCPEnabled]);
  const readCapabilities = useCallback(async () => {
    const epoch = ++capabilityEpoch.current;
    setCapabilityLoading(true);
    setCapabilityError("");
    try {
      const next = await capabilityApi.snapshot();
      if (capabilityEpoch.current !== epoch) return;
      setCapabilities(next);
      const availableSourceKeys = new Set(
        next.git_observations.flatMap((observation) =>
          next.repositories
            .filter(
              (repository) =>
                repository.id === observation.repository_id &&
                repository.revision === observation.repository_revision &&
                observation.status === "succeeded" &&
                !observation.unborn &&
                !!observation.root &&
                !!observation.head,
            )
            .map(
              (repository) =>
                `${repository.id}:${repository.revision}:${observation.id}`,
            ),
        ),
      );
      setSourceKey((current) =>
        availableSourceKeys.has(current) ? current : "",
      );
      const availableSkills = new Set(
        next.skills.map((skill) => `${skill.id}:${skill.revision}`),
      );
      setSkillRows((current) =>
        current.filter(
          (row) => !row.skillKey || availableSkills.has(row.skillKey),
        ),
      );
    } catch (cause) {
      if (capabilityEpoch.current === epoch)
        setCapabilityError(errorMessage(cause));
    } finally {
      if (capabilityEpoch.current === epoch) setCapabilityLoading(false);
    }
  }, []);
  useEffect(() => {
    if (
      controlledMode &&
      !capabilities &&
      !capabilityLoading &&
      !capabilityError
    )
      void readCapabilities();
  }, [
    capabilities,
    capabilityError,
    capabilityLoading,
    controlledMode,
    readCapabilities,
  ]);
  const sources = useMemo(
    () =>
      (capabilities?.git_observations ?? []).flatMap((observation) =>
        (capabilities?.repositories ?? [])
          .filter(
            (repository) =>
              repository.id === observation.repository_id &&
              repository.revision === observation.repository_revision &&
              observation.status === "succeeded" &&
              !observation.unborn &&
              !!observation.root &&
              !!observation.head,
          )
          .map((repository) => ({
            key: `${repository.id}:${repository.revision}:${observation.id}`,
            repository,
            observation,
          })),
      ),
    [capabilities],
  );
  const selectedSource = sources.find((source) => source.key === sourceKey);
  const submit = async () => {
    if (lock.current || readOnly) return;
    if (importing && !selectedInspection) {
      setError("请先检查可导入性并确认当前回执。");
      return;
    }
    const selectedSkills = skillRows.map((row) => ({
      row,
      skill: capabilities?.skills.find(
        (item) => `${item.id}:${item.revision}` === row.skillKey,
      ),
    }));
    if (controlledMode && (!controlledAvailable || !selectedSource)) {
      setError(
        "请选择仍可用的成功登记来源；创建开关或能力目录不可用时不会猜测。",
      );
      return;
    }
    if (controlledMode && selectedSkills.some((item) => !item.skill)) {
      setError("请选择每个 Skill 的精确版本。");
      return;
    }
    const duplicate = new Set<string>();
    if (
      controlledMode &&
      selectedSkills.some(({ row, skill }) => {
        const key = `${skill!.id}\u0000${row.role}`;
        if (duplicate.has(key)) return true;
        duplicate.add(key);
        return false;
      })
    ) {
      setError("同一 Skill 与角色组合不能重复绑定。");
      return;
    }
    if (mcpControlled && (!localMCP?.eligible || !localMCP.selection)) {
      setError(
        "本地测试服务当前不具备任务创建资格；不会提交旧选择或自行构造绑定。",
      );
      return;
    }
    const requestRepo = controlledMode
      ? selectedSource!.observation.root
      : (selectedInspection?.root ?? repo.trim());
    if (!requestRepo.startsWith("/")) {
      setError("请输入 Git 仓库的绝对路径，例如 /Users/you/project");
      return;
    }
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const { mission } = await api.create({
        title: title.trim(),
        goal: goal.trim(),
        repo_path: requestRepo,
        ...(importing && selectedInspection
          ? {
              import_version: 1,
              import_selection: {
                inspection_id: selectedInspection.id,
                digest: selectedInspection.digest,
              },
            }
          : {}),
        spec: {
          scope: lines(scope),
          protected: lines(protectedPaths),
          acceptance: lines(commands),
          image: image.trim(),
          image_id: "",
          profile_id: profileID,
          research: controlledMode ? true : research,
          max_calls: budget,
        },
        ...(mcpControlled
          ? {
              workspace_mode: "generations_controlled_mcp_read_v2" as const,
              tool_mode: "controlled_mcp_read_v2" as const,
              capability_selection: {
                repository_id: selectedSource!.repository.id,
                repository_revision: selectedSource!.repository.revision,
                observation_id: selectedSource!.observation.id,
                skills: selectedSkills.map(({ row, skill }) => ({
                  id: skill!.id,
                  revision: skill!.revision,
                  digest: skill!.digest,
                  name: skill!.name,
                  role: row.role,
                })),
              },
              mcp_selection: localMCP!.selection!,
            }
          : controlled
            ? {
                workspace_mode: "generations_controlled_read_v1" as const,
                tool_mode: "controlled_read_v1" as const,
                capability_selection: {
                  repository_id: selectedSource!.repository.id,
                  repository_revision: selectedSource!.repository.revision,
                  observation_id: selectedSource!.observation.id,
                  skills: selectedSkills.map(({ row, skill }) => ({
                    id: skill!.id,
                    revision: skill!.revision,
                    digest: skill!.digest,
                    name: skill!.name,
                    role: row.role,
                  })),
                },
              }
            : recoverable
              ? { workspace_mode: "generations_v1" as const, coding_version: 1 }
              : {}),
      });
      onCreated(mission);
    } catch (e) {
      setError(errorMessage(e));
      if (importing) {
        setInspection(null);
        setImportAttempt((value) => value + 1);
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  return (
    <div className="page create-page">
      <header className="page-header">
        <p className="eyebrow">项目 / 新建任务</p>
        <h1>定义这一次交付。</h1>
        <p className="muted">
          创建只会读取已提交的 HEAD
          快照，不调用模型。下一步检查文件清单并授权上下文。
        </p>
      </header>
      <form
        className="create-form"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <fieldset disabled={busy || loading}>
          <legend>目标与仓库</legend>
          <label htmlFor="mission-title">任务标题</label>
          <input
            id="mission-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            required
            maxLength={200}
            placeholder="例如：完善导入错误提示"
          />
          <label htmlFor="mission-goal">交付目标</label>
          <textarea
            id="mission-goal"
            value={goal}
            onChange={(e) => setGoal(e.target.value)}
            required
            rows={3}
            placeholder="希望改变什么？如何判断完成？"
          />
          <label htmlFor="mission-repo">Git 仓库绝对路径</label>
          <input
            id="mission-repo"
            value={repo}
            onChange={(e) => {
              setRepo(e.target.value);
              setImportRepository(undefined);
            }}
            required={!controlledMode}
            disabled={controlledMode}
            placeholder="/Users/you/project"
          />
          {importing && (
            <>
              <label htmlFor="import-repository">
                已登记仓库（元数据，不代表许可）
              </label>
              <select
                id="import-repository"
                value={importRepository?.id ?? ""}
                onChange={(event) => {
                  const next = repositories.find(
                    (r) => r.id === event.target.value,
                  );
                  setImportRepository(next);
                  if (next) setRepo(next.path);
                }}
              >
                <option value="">使用显式绝对路径</option>
                {repositories.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name} · 登记 v{r.revision}
                  </option>
                ))}
              </select>
              {registryError && (
                <p role="status">
                  登记目录读取失败：{registryError}。可显式输入路径后检查。
                </p>
              )}
            </>
          )}
          <p className="field-help">
            {controlledMode
              ? "受控模式使用下方成功登记观察的 canonical root；此旧模式草稿会在切换回来时保留。"
              : "只读取已提交文件，不包含未提交修改；原仓库保持只读。"}
          </p>
          <div className="form-columns">
            <div>
              <label htmlFor="mission-scope">允许修改范围（每行一项）</label>
              <textarea
                id="mission-scope"
                value={scope}
                onChange={(e) => setScope(e.target.value)}
                required
                rows={4}
                placeholder="src/"
              />
            </div>
            <div>
              <label htmlFor="mission-protected">受保护文件（每行一项）</label>
              <textarea
                id="mission-protected"
                value={protectedPaths}
                onChange={(e) => setProtectedPaths(e.target.value)}
                rows={4}
                placeholder="tests/"
              />
            </div>
          </div>
          <p className="field-help">
            使用仓库相对路径或目录前缀，不是
            glob。验收、依赖与运行配置另有默认保护。
          </p>
        </fieldset>
        <fieldset disabled={busy || loading}>
          <legend>验收与调用边界</legend>
          <label htmlFor="mission-commands">验收命令（每行一条）</label>
          <textarea
            id="mission-commands"
            value={commands}
            onChange={(e) => setCommands(e.target.value)}
            required
            rows={3}
            placeholder="/opt/foundry/verify"
          />
          <p className="field-help">
            V1 仅支持受限 grep / test、使用相同受限语法的已提交 sh
            脚本，或可信镜像内 /opt/foundry/ 下的验收器（也可用 sh
            调用）。只允许字面参数，禁止 shell 组合与替换，不直接支持 npm /
            python / make。镜像验收器必须自带验收逻辑，
            不得把判定逻辑委托给可编辑的仓库脚本。不支持的语法会在模型调用前拒绝。
          </p>
          <label htmlFor="mission-image">本地 Docker 镜像</label>
          <input
            id="mission-image"
            value={image}
            onChange={(e) => setImage(e.target.value)}
            required
            placeholder="输入已有的可信本地镜像名称或 ID"
          />
          <p className="field-help">
            仅使用本机已有镜像；不自动拉取、不联网、不回退到宿主机执行。命令需兼容只读
            /workspace 与可写 /tmp。
          </p>
          <div className="form-columns">
            <div>
              <label htmlFor="mission-profile">任务默认模型连接</label>
              <select
                id="mission-profile"
                value={profileID}
                onChange={(e) => setProfileID(e.target.value)}
              >
                <option value="">使用已保存的默认连接</option>
                {profiles.map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name} · {profile.model}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label htmlFor="mission-budget">模型调用上限</label>
              <input
                id="mission-budget"
                type="number"
                min={1}
                max={16}
                value={budget}
                onChange={(e) => setBudget(Number(e.target.value))}
                required
              />
            </div>
          </div>
          <label className="check-label">
            <input
              type="checkbox"
              checked={controlledMode || research}
              disabled={controlledMode}
              onChange={(e) => setResearch(e.target.checked)}
            />
            启用 Researcher 仓库调研（只读，不搜索网页）
            {controlledMode && "；受控模式强制开启"}
          </label>
          <label className="check-label">
            <input
              type="checkbox"
              checked={controlled}
              disabled={
                healthLoading ||
                !!healthError ||
                !controlledAvailable ||
                mcpControlled
              }
              onChange={(e) => {
                setControlled(e.target.checked);
                if (e.target.checked) setMCPControlled(false);
              }}
            />
            启用受控 Researcher 本地读取
          </label>
          {healthLoading && (
            <p role="status" className="field-help">
              正在检查受控任务创建开关…
            </p>
          )}
          {healthError && (
            <div className="note warning-note">
              <p>创建开关读取失败；旧模式仍可创建，受控模式保持关闭。</p>
              <button type="button" onClick={() => void readHealth()}>
                重试创建开关
              </button>
            </div>
          )}
          {!healthLoading && !healthError && !controlledAvailable && (
            <p className="note warning-note">
              本机已关闭新的受控任务创建；历史任务仍可读取。
            </p>
          )}
          <label className="check-label">
            <input
              type="checkbox"
              checked={mcpControlled}
              disabled={
                healthLoading ||
                localMCPLoading ||
                !!healthError ||
                !!localMCPError ||
                !taskLocalMCPEnabled ||
                !localMCP?.eligible
              }
              onChange={(e) => {
                setMCPControlled(e.target.checked);
                if (e.target.checked) setControlled(false);
              }}
            />
            启用任务内本地 MCP 调研（测试服务）
          </label>
          {localMCPLoading && (
            <p className="field-help" role="status">
              正在核对本地测试服务选择…
            </p>
          )}
          {localMCPError && (
            <div className="note warning-note">
              <p>本地测试服务状态读取失败：{localMCPError}</p>
              <button type="button" onClick={() => void readLocalMCP()}>
                重试本地测试服务状态
              </button>
            </div>
          )}
          {!healthLoading &&
            !localMCPLoading &&
            !localMCPError &&
            !localMCP?.eligible && (
              <p className="note warning-note">
                任务内本地 MCP 当前不可创建：
                {localMCP?.reason ??
                  "controller-owned local test service disabled"}
                。历史任务仍可读取。
              </p>
            )}
          {localMCP?.eligible && (
            <p className="field-help">
              仅可选择 controller
              拥有的合成本地测试服务；每次实际调用仍需在任务内单独审批。
            </p>
          )}
          <label className="check-label">
            <input
              type="checkbox"
              checked={!controlledMode && recoverable}
              disabled={controlledMode}
              onChange={(e) => {
                setRecoverable(e.target.checked);
                if (e.target.checked) setControlled(false);
              }}
            />
            启用可恢复候选（新任务）
          </label>
          <p className="field-help">
            使用不可变候选 generation
            和本地操作账本。默认用于随后创建的新普通任务；不会迁移旧任务，也不会自动恢复执行或回滚。
            {controlledMode &&
              " 普通任务的可恢复候选选择与受控模式互斥，当前显示关闭；切回普通模式时恢复草稿选择。"}
          </p>
          {controlledMode && (
            <section className="controlled-create" aria-label="受控读取绑定">
              <h3>
                冻结登记来源与 Skill
                {mcpControlled ? " · 本地 MCP 测试选择" : ""}
              </h3>
              {capabilityLoading && <p role="status">正在读取能力目录…</p>}
              {capabilityError && (
                <div className="note warning-note">
                  <p role="alert">
                    能力目录读取失败：{capabilityError}
                    。失败不表示目录为空，表单与已有选择已保留。
                  </p>
                  <button type="button" onClick={() => void readCapabilities()}>
                    重试能力目录
                  </button>
                </div>
              )}
              {capabilities && (
                <>
                  <label htmlFor="controlled-source">冻结的登记来源</label>
                  <select
                    id="controlled-source"
                    value={sourceKey}
                    onChange={(e) => setSourceKey(e.target.value)}
                    required
                  >
                    <option value="">请选择成功的精确观察</option>
                    {sources.map(({ key, repository, observation }) => (
                      <option key={key} value={key}>
                        {repository.name} · 登记 v{repository.revision} ·{" "}
                        {observation.root} · {observation.head}
                      </option>
                    ))}
                  </select>
                  {selectedSource && (
                    <div className="source-facts">
                      <code>{selectedSource.observation.root}</code>
                      <span>
                        HEAD {selectedSource.observation.head} ·{" "}
                        {selectedSource.observation.detached
                          ? "detached"
                          : selectedSource.observation.branch || "分支未知"}
                      </span>
                      <span>
                        检查于 {selectedSource.observation.checked_at}
                      </span>
                      <strong>
                        working_tree_status=
                        {selectedSource.observation.working_tree_status}
                        ；not_checked 不表示工作区干净。
                      </strong>
                    </div>
                  )}
                  <div className="controlled-skill-editor">
                    {skillRows.map((row, index) => (
                      <div className="skill-binding-row" key={row.key}>
                        <label htmlFor={`controlled-skill-${row.key}`}>
                          Skill 版本 {index + 1}
                        </label>
                        <select
                          id={`controlled-skill-${row.key}`}
                          value={row.skillKey}
                          onChange={(e) =>
                            setSkillRows((current) =>
                              current.map((item) =>
                                item.key === row.key
                                  ? { ...item, skillKey: e.target.value }
                                  : item,
                              ),
                            )
                          }
                        >
                          <option value="">请选择精确版本</option>
                          {(capabilities.skills ?? []).map((skill) => (
                            <option
                              key={`${skill.id}:${skill.revision}`}
                              value={`${skill.id}:${skill.revision}`}
                            >
                              {skill.name} · v{skill.revision}
                            </option>
                          ))}
                        </select>
                        <label htmlFor={`controlled-role-${row.key}`}>
                          Skill 角色 {index + 1}
                        </label>
                        <select
                          id={`controlled-role-${row.key}`}
                          value={row.role}
                          onChange={(e) =>
                            setSkillRows((current) =>
                              current.map((item) =>
                                item.key === row.key
                                  ? { ...item, role: e.target.value as Role }
                                  : item,
                              ),
                            )
                          }
                        >
                          {roles.map((role) => (
                            <option key={role} value={role}>
                              {roleNames[role]}
                            </option>
                          ))}
                        </select>
                        <button
                          type="button"
                          onClick={() =>
                            setSkillRows((current) =>
                              current.filter((item) => item.key !== row.key),
                            )
                          }
                        >
                          移除 Skill {index + 1}
                        </button>
                      </div>
                    ))}
                    <button
                      type="button"
                      disabled={skillRows.length >= 3}
                      onClick={() =>
                        setSkillRows((current) => [
                          ...current,
                          {
                            key: nextSkillKey.current++,
                            skillKey: "",
                            role: "researcher",
                          },
                        ])
                      }
                    >
                      添加 Skill 绑定
                    </button>
                  </div>
                  <p className="field-help">
                    最多三个精确版本；重复的 Skill/角色组合会被拒绝。Skill
                    只作为冻结上下文，不执行脚本。
                  </p>
                  {mcpControlled && localMCP?.selection && (
                    <div
                      className="source-facts"
                      aria-label="本地 MCP 冻结选择"
                    >
                      <strong>合成本地测试服务（仅测试）</strong>
                      <code>{localMCP.endpoint}</code>
                      <span>
                        连接 {localMCP.selection.connection_id} · 配置 v
                        {localMCP.selection.connection_revision} · 快照{" "}
                        {localMCP.selection.snapshot_id}
                      </span>
                      {localMCP.selection.tools.map((tool) => (
                        <span key={tool.name}>
                          工具 <code>{tool.name}</code> · 定义{" "}
                          <code>{tool.definition_digest}</code> · 策略{" "}
                          <code>{tool.policy_digest}</code>
                        </span>
                      ))}
                      <span>
                        MCP 与仓库共用最多 {localMCP.budget.max_calls} 次调用、
                        {localMCP.budget.max_total_result_bytes} bytes
                        结果预算； 单次调用 {localMCP.budget.deadline_seconds}
                        s。
                      </span>
                    </div>
                  )}
                  <p className="field-help">
                    {mcpControlled
                      ? "固定读取预算：最多 4 个 Researcher 模型回合；仓库读取与 MCP 合计使用上述 3 次调用和 192 KiB 总结果预算，不是两套调用池。仓库读取参数 8 KiB、单次结果 64 KiB、本地工具期限 5 秒。"
                      : "固定读取预算：最多 4 个 Researcher 模型回合、3 次仓库调用；参数 8 KiB、单次结果 64 KiB、总结果预留 192 KiB、本地工具期限 5 秒。"}
                  </p>
                </>
              )}
            </section>
          )}
          <p className="field-help">
            七个角色的连接会在创建时冻结，角色专用绑定优先于任务默认连接。
          </p>
          {rolePolicyLoading && (
            <p className="field-help" role="status">
              正在读取活动角色策略来源…
            </p>
          )}
          {rolePolicyError && (
            <p className="note warning-note" role="status">
              活动角色策略读取失败：{rolePolicyError}
              。模型连接表单仍可使用，但这里不会猜测策略来源。
            </p>
          )}
          {rolePolicyState?.active_decision &&
            modelSettings.model_policy_decision_id ===
              rolePolicyState.active_decision.id && (
              <section
                className="active-policy-preview"
                aria-label="创建任务的活动策略来源"
              >
                <div className="section-heading">
                  <h3>已确认的未来任务策略</h3>
                  <span>
                    策略版本{" "}
                    {rolePolicyState.active_decision.proposal.policy_revision}
                  </span>
                </div>
                <p className="muted small">
                  创建时由服务端再次核对并冻结；任务默认连接选择不会覆盖已确认策略的七个角色。这里只预览当前来源，不授予调用、工具、执行或交付权限。
                </p>
                <div className="active-policy-roles">
                  {roles.map((role) => (
                    <span key={role}>
                      <strong>{roleNames[role]}</strong>
                      <code>
                        {rolePolicyState.active_decision!.proposal.roles[role]
                          ?.selected_profile_id || "无可选建议"}
                      </code>
                    </span>
                  ))}
                </div>
                <small>
                  决定 <code>{rolePolicyState.active_decision.id}</code> ·
                  确认于 {rolePolicyState.active_decision.confirmed_at}
                </small>
              </section>
            )}
          {!loading && (
            <ModelSnapshot
              preview={Object.fromEntries(
                roles.flatMap((role) => {
                  const profile = profiles.find(
                    (p) =>
                      p.id ===
                      (modelSettings.role_bindings[role] ||
                        profileID ||
                        modelSettings.default_profile_id),
                  );
                  if (!profile) return [];
                  const selection: RoleModelSelection = {
                    profile: {
                      ...profile,
                      ...(profile.account
                        ? {
                            account_revision: profile.account.revision,
                            credential_version:
                              profile.account.current_credential_version,
                          }
                        : {}),
                    },
                    ...(profile.account ? { account: profile.account } : {}),
                  };
                  return [[role, selection]];
                }),
              )}
            />
          )}
        </fieldset>
        {importing ? (
          <RepositoryImport
            key={epoch}
            disabled={busy || loading || readOnly}
            request={{
              ...(importRepository
                ? {
                    repository_id: importRepository.id,
                    repository_revision: importRepository.revision,
                  }
                : { repo_path: repo.trim() }),
              spec: {
                scope: lines(scope),
                protected: lines(protectedPaths),
                acceptance: lines(commands),
                image: image.trim(),
              },
              consent: true,
            }}
            onInspected={(receipt) => {
              if (importEpoch.current.epoch === epoch)
                setInspection(receipt ? { epoch, receipt } : null);
            }}
          />
        ) : (
          <p className="note">
            旧式 / 专用模式：未绑定 C3 导入回执；不会复用普通任务检查。
          </p>
        )}
        <ImportHistory key={`history-${importAttempt}`} />
        {!loading && !profiles.length && (
          <p className="note warning-note">
            尚未配置模型连接。请先进入“设置”保存连接与默认分配。
          </p>
        )}
        {error && (
          <p role="alert" className="form-error">
            {error}
          </p>
        )}
        <div className="actions form-actions">
          <button
            className="primary"
            type="submit"
            disabled={
              busy ||
              loading ||
              readOnly ||
              !profiles.length ||
              (importing && !selectedInspection)
            }
          >
            {busy ? "正在读取快照…" : "创建任务并检查上下文"}
          </button>
          <button type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
        </div>
      </form>
    </div>
  );
}
