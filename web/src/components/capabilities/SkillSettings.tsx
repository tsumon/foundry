import { useRef, useState } from "react";
import { capabilityApi } from "../../capabilityApi";
import type {
  CapabilityPanelProps,
  SkillFile,
  SkillInput,
  SkillRevision,
} from "../../capabilityTypes";
import { useCapabilityRequest } from "./useCapabilityRequest";

const blank = (): SkillInput => ({
  expected_revision: 0,
  source: "",
  files: [{ path: "SKILL.md", content: "" }],
});
const readBytes = (file: File): Promise<ArrayBuffer> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("file_read_failed"));
    reader.onload = () =>
      reader.result instanceof ArrayBuffer
        ? resolve(reader.result)
        : reject(new Error("file_read_failed"));
    reader.readAsArrayBuffer(file);
  });
function FilesText({ files }: { files: SkillFile[] }) {
  return (
    <>
      {files.map((file, index) => (
        <div className="capability-file" key={`${index}-${file.path}`}>
          <h4>{file.path || "路径尚未填写"}</h4>
          <pre>{file.content || "（空正文）"}</pre>
        </div>
      ))}
    </>
  );
}

export function SkillSettings({
  enabled,
  snapshot,
  update,
}: CapabilityPanelProps) {
  const [form, setForm] = useState<SkillInput>(blank);
  const [view, setView] = useState<SkillRevision | null>(null);
  const [notice, setNotice] = useState("");
  const request = useCapabilityRequest(enabled);
  const editor = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);
  const edit = (revision?: SkillRevision) => {
    if (request.locked()) return;
    editor.current++;
    setForm(
      revision
        ? {
            id: revision.id,
            expected_revision: revision.revision,
            source: revision.source,
            files: [
              ...revision.files.filter((file) => file.path === "SKILL.md"),
              ...revision.files.filter((file) => file.path !== "SKILL.md"),
            ].map((file) => ({ ...file })),
          }
        : blank(),
    );
    setNotice("");
    request.setError("");
    if (fileInput.current) fileInput.current.value = "";
  };
  const change = (next: (old: SkillInput) => SkillInput) => {
    if (request.locked()) return;
    editor.current++;
    setForm(next);
    setNotice("");
  };
  const selectFiles = (files: File[]) =>
    void request.run(async (valid) => {
      const generation = editor.current;
      if (!files.length) return;
      if (
        files.length > 16 ||
        files.some((file) => file.size > 65536) ||
        files.reduce((total, file) => total + file.size, 0) > 262144
      ) {
        request.setError(
          "文件超出限制：最多 16 个文件，单文件 64 KiB，总正文 256 KiB。草稿已保留。",
        );
        return;
      }
      let selected: SkillFile[];
      try {
        selected = await Promise.all(
          files.map(async (file) => ({
            path: file.name,
            content: new TextDecoder("utf-8", {
              fatal: true,
              ignoreBOM: true,
            }).decode(await readBytes(file)),
          })),
        );
      } catch {
        if (valid())
          request.setError(
            "文件读取失败或不是有效 UTF-8 文本。全部原草稿已保留，请重新选择。",
          );
        return;
      }
      if (!valid() || generation !== editor.current) return;
      // Only a deliberately selected root file replaces the pasted root. Other paths
      // remain editable and server validation rejects duplicate/case-conflicting paths.
      const root = selected.find((file) => file.path === "SKILL.md");
      const combined = root
        ? [
            root,
            ...form.files.filter((file) => file.path !== "SKILL.md"),
            ...selected.filter((file) => file !== root),
          ]
        : [...form.files, ...selected];
      if (
        combined.length > 16 ||
        combined.reduce(
          (total, file) =>
            total + new TextEncoder().encode(file.content).length,
          0,
        ) > 262144
      ) {
        request.setError(
          "合并后的文件超出 16 个或 256 KiB 限制。原草稿已保留。",
        );
        return;
      }
      editor.current++;
      setForm((old) => ({ ...old, files: combined }));
      setNotice(
        "仅已选文件进入预览。请检查或编辑静态参考路径；未扫描目录或读取外链。",
      );
    });
  const save = () =>
    void request.run(async (valid) => {
      const generation = editor.current;
      const { revision } = await capabilityApi.saveSkill(form);
      if (!valid()) return;
      update((old) => ({
        ...old,
        skills: [
          revision,
          ...old.skills.filter(
            (item) =>
              item.id !== revision.id || item.revision !== revision.revision,
          ),
        ],
      }));
      if (generation === editor.current) {
        editor.current++;
        setForm(blank());
        setView(revision);
        if (fileInput.current) fileInput.current.value = "";
        setNotice(
          "静态版本已登记；未绑定、未加载或执行。编辑器正文已清空，完整版本保留在下方。",
        );
      }
    });
  const inspect = (item: SkillRevision) =>
    void request.run(async (valid) => {
      const { revision } = await capabilityApi.skillRevision(
        item.id,
        item.revision,
      );
      if (valid()) setView(revision);
    });
  const latest = new Map<string, SkillRevision>();
  for (const item of snapshot.skills)
    if (!latest.has(item.id) || latest.get(item.id)!.revision < item.revision)
      latest.set(item.id, item);
  return (
    <>
      <div className="capability-layout">
        <section className="connection-section">
          <div className="section-heading">
            <h2>{form.id ? "追加静态 Skill 版本" : "登记静态说明"}</h2>
            {form.id && (
              <button
                disabled={!enabled || request.busy}
                onClick={() => edit()}
              >
                新建 Skill 草稿
              </button>
            )}
          </div>
          {form.id && (
            <p>基于 v{form.expected_revision} 追加新版本；历史正文不会覆盖。</p>
          )}
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
              <legend className="sr-only">静态 Skill 文件</legend>
              <label htmlFor="skill-source">Skill 来源说明</label>
              <input
                id="skill-source"
                value={form.source}
                maxLength={2048}
                onChange={(e) =>
                  change((old) => ({ ...old, source: e.target.value }))
                }
                placeholder="由谁提供、为何登记"
              />
              <label htmlFor="skill-files">选择静态文本文件</label>
              <input
                id="skill-files"
                ref={fileInput}
                type="file"
                multiple
                accept=".md,.txt,.json,.yaml,.yml"
                onChange={(e) => {
                  const files = Array.from(e.target.files ?? []);
                  e.target.value = "";
                  selectFiles(files);
                }}
              />
              <p className="field-help">
                只读明确选择的文件，严格 UTF-8。选中 SKILL.md
                替换根正文；其他文件追加，可编辑 references/ 或 assets/
                路径。不递归读取目录。
              </p>
              {form.files.map((file, index) => (
                <div className="capability-file-editor" key={index}>
                  {index === 0 ? (
                    <label htmlFor="skill-root">SKILL.md 正文</label>
                  ) : (
                    <>
                      <label htmlFor={`skill-path-${index}`}>
                        文件 {index + 1} 路径
                      </label>
                      <input
                        id={`skill-path-${index}`}
                        required
                        value={file.path}
                        onChange={(e) =>
                          change((old) => ({
                            ...old,
                            files: old.files.map((item, at) =>
                              at === index
                                ? { ...item, path: e.target.value }
                                : item,
                            ),
                          }))
                        }
                      />
                      <label htmlFor={`skill-body-${index}`}>
                        文件 {index + 1} 正文
                      </label>
                    </>
                  )}
                  <textarea
                    id={index === 0 ? "skill-root" : `skill-body-${index}`}
                    required={index === 0}
                    rows={index === 0 ? 10 : 5}
                    spellCheck={false}
                    value={file.content}
                    onChange={(e) =>
                      change((old) => ({
                        ...old,
                        files: old.files.map((item, at) =>
                          at === index
                            ? { ...item, content: e.target.value }
                            : item,
                        ),
                      }))
                    }
                    placeholder={
                      index === 0
                        ? "---\nname: review-source\ndescription: Review source structure\n---\n静态说明正文"
                        : "静态参考正文"
                    }
                  />
                  {index > 0 && (
                    <button
                      type="button"
                      onClick={() =>
                        change((old) => ({
                          ...old,
                          files: old.files.filter((_, at) => at !== index),
                        }))
                      }
                    >
                      移除文件 {index + 1}
                    </button>
                  )}
                </div>
              ))}
              <div className="actions">
                <button
                  type="button"
                  disabled={form.files.length >= 16}
                  onClick={() =>
                    change((old) => ({
                      ...old,
                      files: [
                        ...old.files,
                        { path: "references/notes.md", content: "" },
                      ],
                    }))
                  }
                >
                  添加静态参考文件
                </button>
                <button className="primary" type="submit">
                  {form.id ? "保存 Skill 新版本" : "登记静态 Skill"}
                </button>
              </div>
            </fieldset>
          </form>
        </section>
        <aside className="connection-guide">
          <h2>仅保存静态文本</h2>
          <p>
            必须包含根 SKILL.md 和 name / description frontmatter。最多 16
            files、每文件 64 KiB、总正文 256 KiB。
          </p>
          <p>
            参考文件仅允许根目录或 references/、assets/ 下的 .md / .txt / .json
            / .yaml / .yml。拒绝脚本、路径穿越及外部包含。
          </p>
          <div className="note">
            正文与 allowed-tools
            等字段只是说明，登记本身不会加载上下文或授予权限。只有在新受控任务中明确选择冻结版本后，静态正文才会发送给所选角色的冻结模型连接。外链不访问，HTML
            不渲染，命令不执行。
          </div>
        </aside>
      </div>
      <section
        className="saved-connections"
        role="region"
        aria-label="待登记文件预览"
      >
        <h2>待登记文件预览 · {form.files.length} 个文件</h2>
        <FilesText files={form.files} />
      </section>
      <section className="saved-connections">
        <h2>静态 Skill 版本</h2>
        {!snapshot.skills.length && <p className="muted">尚未登记 Skill</p>}
        {Array.from(latest.values()).map((item) => (
          <article className="capability-record" key={item.id}>
            <h3>{item.name}</h3>
            <p>{item.description}</p>
            <p>
              已登记不可变版本；可在新受控任务中精确选择。绑定只提供上下文，不执行文件。
            </p>
            <p className="muted">
              最新版本 v{item.revision} · {item.created_at}
            </p>
            <div className="actions">
              <button
                disabled={!enabled || request.busy}
                aria-label={`创建新版本 ${item.name}`}
                onClick={() => edit(item)}
              >
                创建新版本
              </button>
              {snapshot.skills
                .filter((version) => version.id === item.id)
                .sort((a, b) => b.revision - a.revision)
                .map((version) => (
                  <button
                    key={version.revision}
                    disabled={!enabled || request.busy}
                    aria-label={`查看 ${item.name} v${version.revision}`}
                    onClick={() => inspect(version)}
                  >
                    查看 v{version.revision}
                  </button>
                ))}
            </div>
          </article>
        ))}
      </section>
      {view && (
        <section
          className="saved-connections"
          role="region"
          aria-label="Skill 完整版本"
        >
          <h2>
            {view.name} · v{view.revision}
          </h2>
          <p>
            来源说明：{view.source || "未填写"} · 登记时间：{view.created_at}
          </p>
          <p>
            摘要：<code>{view.digest}</code>
          </p>
          <FilesText files={view.files} />
        </section>
      )}
    </>
  );
}
