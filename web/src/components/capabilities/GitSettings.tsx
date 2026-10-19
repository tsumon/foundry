import { useEffect, useRef, useState } from "react";
import { capabilityApi } from "../../capabilityApi";
import type {
  CapabilityPanelProps,
  GitInput,
  GitRepository,
  ProjectChoices,
} from "../../capabilityTypes";
import {
  compareCapabilityTime,
  useCapabilityRequest,
} from "./useCapabilityRequest";

const blank = (): GitInput => ({
  expected_revision: 0,
  name: "",
  path: "",
  project_id: "",
});
const inputFor = (item: GitRepository): GitInput => ({
  id: item.id,
  expected_revision: item.revision,
  name: item.name,
  path: item.path,
  project_id: item.project_id,
});

export function GitSettings({
  enabled,
  snapshot,
  update,
  catalog,
}: CapabilityPanelProps & { catalog: ProjectChoices }) {
  const [form, setForm] = useState<GitInput>(blank);
  const [confirmation, setConfirmation] = useState<GitRepository | null>(null);
  const confirmationHeading = useRef<HTMLHeadingElement>(null);
  const confirmationTrigger = useRef<HTMLButtonElement | null>(null);
  const [notice, setNotice] = useState("");
  const request = useCapabilityRequest(enabled);
  const editor = useRef(0);
  useEffect(() => {
    if (confirmation) confirmationHeading.current?.focus();
  }, [confirmation]);
  const projectReady =
    catalog.ready && !catalog.loading && !catalog.error && !!catalog.snapshot;
  const projects = catalog.snapshot?.projects ?? [];
  const edit = (item?: GitRepository) => {
    if (request.locked()) return;
    editor.current++;
    setForm(item ? inputFor(item) : blank());
    setNotice("");
    request.setError("");
  };
  const field = (change: Partial<GitInput>) => {
    if (request.locked()) return;
    editor.current++;
    setForm((old) => ({ ...old, ...change }));
    setNotice("");
  };
  const save = () =>
    void request.run(async (valid) => {
      const generation = editor.current;
      const { repository } = await capabilityApi.saveGit(form);
      if (!valid()) return;
      update((old) =>
        old.repositories.some(
          (item) =>
            item.id === repository.id && item.revision > repository.revision,
        )
          ? old
          : {
              ...old,
              repositories: [
                repository,
                ...old.repositories.filter((item) => item.id !== repository.id),
              ],
            },
      );
      if (generation === editor.current) {
        setForm(inputFor(repository));
        setNotice("仓库登记已保存；尚未因此执行 Git 检查。");
      }
    });
  const inspect = () => {
    if (!confirmation) return;
    const selected = confirmation;
    void request.run(async (valid) => {
      const { observation } = await capabilityApi.inspectGit(
        selected.id,
        selected.revision,
      );
      if (!valid()) return;
      update((old) =>
        old.git_observations.some(
          (item) =>
            item.repository_id === observation.repository_id &&
            (item.repository_revision > observation.repository_revision ||
              (item.repository_revision === observation.repository_revision &&
                compareCapabilityTime(item.checked_at, observation.checked_at) >
                  0)),
        )
          ? old
          : {
              ...old,
              git_observations: [
                observation,
                ...old.git_observations.filter(
                  (item) => item.repository_id !== selected.id,
                ),
              ],
            },
      );
      setConfirmation(null);
      setNotice("检查记录已返回，请查看实际状态和时间；工作区改动未检查。");
    });
  };
  return (
    <>
      <div className="capability-layout">
        <section className="connection-section">
          <div className="section-heading">
            <h2>{form.id ? "编辑 Git 登记" : "登记本地 Git 仓库"}</h2>
            {form.id && (
              <button
                disabled={!enabled || request.busy}
                onClick={() => edit()}
              >
                新建 Git 登记
              </button>
            )}
          </div>
          {request.error && (
            <p className="form-error" role="alert">
              {request.error}
            </p>
          )}
          {notice && (
            <p className="notice" role="status">
              {notice}
            </p>
          )}
          <form
            className="connection-form"
            onSubmit={(e) => {
              e.preventDefault();
              save();
            }}
          >
            <fieldset disabled={!enabled || request.busy}>
              <legend className="sr-only">Git 仓库登记</legend>
              {form.id && (
                <p>
                  编辑基于配置 v{form.expected_revision}；观察不会自动更新。
                </p>
              )}
              <label htmlFor="git-name">Git 仓库名称</label>
              <input
                id="git-name"
                required
                maxLength={200}
                value={form.name}
                onChange={(e) => field({ name: e.target.value })}
              />
              <label htmlFor="git-path">Git 仓库路径</label>
              <input
                id="git-path"
                required
                value={form.path}
                onChange={(e) => field({ path: e.target.value })}
                placeholder="/absolute/repository/root"
              />
              <label htmlFor="git-project">关联项目（可选）</label>
              <select
                id="git-project"
                value={form.project_id}
                disabled={!projectReady}
                onChange={(e) => field({ project_id: e.target.value })}
              >
                <option value="">不关联项目</option>
                {form.project_id &&
                  !projects.some((item) => item.id === form.project_id) && (
                    <option value={form.project_id}>
                      已登记项目 {form.project_id}（名称暂不可用）
                    </option>
                  )}
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
              <p className="field-help">
                {catalog.error
                  ? "项目列表读取失败，关联选择暂不可用；已有关联保留。"
                  : catalog.loading
                    ? "项目列表正在读取，暂不更改关联。"
                    : !projectReady
                      ? "项目列表尚不可用，可先不关联项目；不会视作空列表。"
                      : projects.length
                        ? "复用现有项目台账；登记不会移动或修改仓库。"
                        : "当前项目台账暂无项目，可不关联。"}
              </p>
              <div className="actions">
                <button type="submit" className="primary">
                  {form.id ? "保存 Git 登记" : "登记 Git 仓库"}
                </button>
              </div>
            </fieldset>
          </form>
        </section>
        <aside className="connection-guide">
          <h2>只读仓库身份</h2>
          <p>
            登记不读取文件。明确确认后，只检查 canonical root、分支、HEAD 及
            detached / unborn 状态。
          </p>
          <p>
            不执行 status 或
            diff，不读取远端，不读取仓库正文，不计算工作区改动数；不会提交、推送或修改仓库。
          </p>
          <div className="note">
            仅支持普通物理 .git 目录。bare、linked worktree、metadata
            symlink、include 等布局不支持；失败会保留安全错误类别。
          </div>
          <div className="note">
            成功的精确观察可在新受控任务创建时选择，并由 controller
            重新核对后冻结。working_tree_status=not_checked 不是工作区干净声明。
          </div>
        </aside>
      </div>
      {confirmation && (
        <section
          className="note capability-confirm"
          role="region"
          aria-label="确认 Git 只读检查"
        >
          <h3 ref={confirmationHeading} tabIndex={-1}>
            确认 Git 只读检查
          </h3>
          <p>
            {confirmation.name} · 已登记配置 v{confirmation.revision}
          </p>
          <p>
            <code>{confirmation.path}</code>
          </p>
          <p>仅检查仓库身份，不检查工作区改动，不执行 Git 写入或网络操作。</p>
          <div className="actions">
            <button
              className="primary"
              disabled={!enabled || request.busy}
              onClick={inspect}
            >
              确认只读检查
            </button>
            <button
              disabled={request.busy}
              onClick={() => {
                setConfirmation(null);
                confirmationTrigger.current?.focus();
              }}
            >
              取消检查
            </button>
          </div>
        </section>
      )}
      <section className="saved-connections">
        <h2>已登记 Git 仓库</h2>
        {!snapshot.repositories.length && (
          <p className="muted">尚未登记 Git 仓库</p>
        )}
        {snapshot.repositories.map((item) => {
          const observed = snapshot.git_observations
            .filter((entry) => entry.repository_id === item.id)
            .sort((a, b) =>
              compareCapabilityTime(b.checked_at, a.checked_at),
            )[0];
          const project = projects.find(
            (entry) => entry.id === item.project_id,
          );
          return (
            <article className="capability-record" key={item.id}>
              <div className="capability-record-header">
                <div>
                  <h3>{item.name}</h3>
                  <p>
                    当前配置 v{item.revision} · {item.updated_at}
                  </p>
                  <code>{item.path}</code>
                  <p>
                    项目：
                    {item.project_id
                      ? `${project?.name ?? item.project_id}${projectReady ? "" : "（上次信息 / 待核实）"}`
                      : "未关联"}
                  </p>
                </div>
                <div className="actions">
                  <button
                    disabled={!enabled || request.busy}
                    aria-label={`编辑 Git ${item.name}`}
                    onClick={() => edit(item)}
                  >
                    编辑
                  </button>
                  <button
                    disabled={!enabled || request.busy}
                    aria-label={`检查仓库身份 ${item.name}`}
                    onClick={(event) => {
                      confirmationTrigger.current = event.currentTarget;
                      setConfirmation(item);
                    }}
                  >
                    检查仓库身份
                  </button>
                </div>
              </div>
              {observed ? (
                <section
                  className="capability-observation"
                  aria-label="最近 Git 身份观察"
                >
                  <h4>最近身份观察 · 配置 v{observed.repository_revision}</h4>
                  {observed.repository_revision !== item.revision && (
                    <p className="warning-text">
                      旧配置观察，不能代表当前登记路径。
                    </p>
                  )}
                  <p>检查时间：{observed.checked_at}</p>
                  {observed.status === "succeeded" ? (
                    <>
                      <p>身份读取成功（仅观察时刻）</p>
                      <dl>
                        <dt>Canonical root</dt>
                        <dd>
                          <code>{observed.root}</code>
                        </dd>
                        <dt>分支</dt>
                        <dd>
                          {observed.detached
                            ? "分离 HEAD（detached）"
                            : observed.branch || "未知"}
                        </dd>
                        <dt>HEAD</dt>
                        <dd>
                          <code>
                            {observed.unborn
                              ? "尚无提交（unborn）"
                              : observed.head || "未知"}
                          </code>
                        </dd>
                      </dl>
                    </>
                  ) : (
                    <p className="form-error">
                      检查失败 · {observed.error_code || "未知错误类别"}
                    </p>
                  )}
                </section>
              ) : (
                <p className="muted">尚未检查仓库身份。</p>
              )}
              <p className="note">
                工作区改动未检查（not_checked），不能判断干净或有改动。
              </p>
            </article>
          );
        })}
      </section>
    </>
  );
}
