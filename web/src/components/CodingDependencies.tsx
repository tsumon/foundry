import { useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { supportedRepositoryPath } from "../repositoryPath";
import { codingTaskApi, type CodingTaskCapability } from "../codingTaskApi";
import {
  importSelectionV2Valid,
  languageContracts,
  materialEqual,
  type CodingProfileTemplateV2,
  type DependencyAdmission,
  type ImportSelectionV2,
  type RepositorySelector,
} from "../codingV2";
import type { CodingTaskSelection } from "./CodingTaskSetup";

export function DependencyFacts({
  admission,
}: {
  admission: DependencyAdmission;
}) {
  const s = admission.summary;
  return (
    <div aria-label="已保留依赖事实">
      <p role="status">已保留依赖准入结果</p>
      <p>
        {s.file_count} 文件 · {s.total_bytes} bytes · {s.package_count} 包
      </p>
      <dl className="metadata compact-metadata">
        <dt>准入摘要</dt>
        <dd>
          <code>{admission.admission_digest}</code>
        </dd>
        <dt>依赖包 / 清单摘要</dt>
        <dd>
          <code>{s.bundle_digest}</code>
          <br />
          <code>{s.manifest_digest}</code>
        </dd>
        <dt>来源 / 策略</dt>
        <dd>
          {s.provenance} · {s.policy}
        </dd>
      </dl>
      <p className="field-help">
        用户提供的文件与已提交锁文件匹配；不证明发布者真实性。准入不是就绪或测试通过，不授权模型、执行或交付。
      </p>
      <details>
        <summary>已提交准入元数据摘要</summary>
        <ul className="path-list">
          {admission.source_files.map((f) => (
            <li key={f.path}>
              <code>{f.path}</code> · {f.bytes} bytes · <code>{f.sha256}</code>
            </li>
          ))}
        </ul>
      </details>
      {!!s.packages.length && (
        <details>
          <summary>
            包身份摘要（{s.packages.length} / {s.package_count}）
          </summary>
          <ul className="path-list">
            {s.packages.map((p) => (
              <li key={p.location}>
                <code>{p.location}</code> · {p.name} · {p.version}
              </li>
            ))}
          </ul>
        </details>
      )}
      {s.omitted_count > 0 && (
        <p>另有 {s.omitted_count} 个包未列出；未展示依赖文件内容。</p>
      )}
    </div>
  );
}
export function CodingDependencies({
  template,
  capability,
  source,
  requested,
  disabled,
  value,
  onChange,
}: {
  template: CodingProfileTemplateV2;
  capability: CodingTaskCapability;
  source?: RepositorySelector;
  requested: boolean;
  disabled: boolean;
  value: CodingTaskSelection | null;
  onChange: (value: CodingTaskSelection | null) => void;
}) {
  const [directory, setDirectory] = useState("");
  const [consent, setConsent] = useState(false);
  const [sourceRoot, setSourceRoot] = useState<"." | "src">(".");
  const [testRoot, setTestRoot] = useState<"test" | "tests">("tests");
  const [support, setSupport] = useState("");
  const [admission, setAdmission] = useState<{
    epoch: number;
    result: DependencyAdmission;
  } | null>(null);
  const [pending, setPending] = useState<number | null>(null);
  const [error, setError] = useState("");
  const requestKey = JSON.stringify([source, template, requested, directory]);
  const fence = useRef({ key: requestKey, epoch: 0 });
  if (fence.current.key !== requestKey)
    fence.current = { key: requestKey, epoch: fence.current.epoch + 1 };
  const epoch = fence.current.epoch;
  const alive = useRef(true);
  const inFlight = useRef<number | null>(null);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    setConsent(false);
    setError("");
  }, [epoch]);
  const selected = admission?.epoch === epoch ? admission.result : null;
  const prefixes = support
    .split(/\r?\n/)
    .map((p) => p.trim())
    .filter(Boolean)
    .sort();
  const selection: ImportSelectionV2 | null =
    selected && template.embedded_vm && template.runtime_inventory_digest
      ? {
          schema_version: 2,
          profile_id: template.profile_id,
          embedded_vm: template.embedded_vm,
          runtime_inventory_digest: template.runtime_inventory_digest,
          admission_digest: selected.admission_digest,
          dependencies: selected.dependencies,
          source_root: sourceRoot,
          test_root: testRoot,
          test_support_prefixes: prefixes,
        }
      : null;
  const available = capability.enabled && template.available;
  useEffect(() => {
    if (
      !requested ||
      !available ||
      !selection ||
      !importSelectionV2Valid(selection)
    ) {
      if (value) onChange(null);
      return;
    }
    const next: CodingTaskSelection = {
      capability,
      profile: {
        schema_version: 2,
        id: template.profile_id,
        mode: "embedded_vm",
      },
      verificationV2: selection,
      admission: selected!,
    };
    if (!materialEqual(value, next)) onChange(next);
  }, [
    requested,
    available,
    capability,
    template.profile_id,
    selection,
    selected,
    value,
    onChange,
  ]);
  const canImport =
    available &&
    requested &&
    !!source &&
    supportedRepositoryPath(directory.trim()) &&
    consent &&
    !disabled &&
    pending !== epoch;
  const importDependencies = async () => {
    if (
      !canImport ||
      inFlight.current === epoch ||
      !source ||
      !template.embedded_vm ||
      !template.runtime_inventory_digest
    )
      return;
    const ticket = epoch;
    inFlight.current = ticket;
    setPending(ticket);
    setError("");
    setAdmission(null);
    onChange(null);
    try {
      const result = await codingTaskApi.importDependencies({
        ...source,
        profile_id: template.profile_id,
        embedded_vm: template.embedded_vm,
        runtime_inventory_digest: template.runtime_inventory_digest,
        input_directory: directory.trim(),
        consent: true,
      });
      if (alive.current && fence.current.epoch === ticket)
        setAdmission({ epoch: ticket, result });
    } catch (cause) {
      if (alive.current && fence.current.epoch === ticket)
        setError(errorMessage(cause));
    } finally {
      if (inFlight.current === ticket) inFlight.current = null;
      if (alive.current && fence.current.epoch === ticket) {
        setPending(null);
        setConsent(false);
      }
    }
  };
  const change = () => onChange(null);
  return (
    <div aria-label="语言依赖与输入选择">
      <p className="field-help">
        {languageContracts[template.profile_id].label} 仅内置
        VM；仅纯语言依赖，不执行安装脚本或包管理器。
        {template.profile_id === "c4_python_model_v2"
          ? "仅使用锁定的纯 Python wheels。"
          : "不支持 native addons、安装脚本或嵌套 workspace。"}
      </p>
      <dl className="metadata compact-metadata">
        <dt>运行时清单摘要</dt>
        <dd>
          <code>{template.runtime_inventory_digest ?? "未提供；不可用"}</code>
        </dd>
      </dl>
      <div className="form-columns">
        <div>
          <label htmlFor="coding-source-root">源代码根目录</label>
          <select
            id="coding-source-root"
            value={sourceRoot}
            disabled={disabled}
            onChange={(e) => {
              change();
              setSourceRoot(e.target.value as "." | "src");
            }}
          >
            <option value=".">.</option>
            <option value="src">src</option>
          </select>
        </div>
        <div>
          <label htmlFor="coding-test-root">测试根目录</label>
          <select
            id="coding-test-root"
            value={testRoot}
            disabled={disabled}
            onChange={(e) => {
              change();
              setTestRoot(e.target.value as "test" | "tests");
            }}
          >
            {template.profile_id !== "c4_python_model_v2" && (
              <option value="test">test</option>
            )}
            <option value="tests">tests</option>
          </select>
        </div>
      </div>
      <p className="field-help">
        源代码根目录不扩大允许修改范围；仍需填写具体可编辑路径。所选测试根目录、测试支持目录、manifest
        / lock / config 均冻结保护。
      </p>
      <label htmlFor="coding-support">测试支持目录（每行一项）</label>
      <textarea
        id="coding-support"
        value={support}
        disabled={disabled}
        onChange={(e) => {
          change();
          setSupport(e.target.value);
        }}
        placeholder="helpers"
      />
      <p className="field-help">
        保留保护前缀：.foundry-build · .venv · node_modules ·
        site-packages；另冻结所选测试根与支持目录。目录不带尾部斜线；只接受规范路径，不能重复。
      </p>
      {selection && !importSelectionV2Valid(selection) && (
        <p role="alert">
          输入选择无效；检查根目录与测试支持路径，不能创建任务。
        </p>
      )}
      <label htmlFor="coding-dependency-input">本地依赖输入目录</label>
      <input
        id="coding-dependency-input"
        value={directory}
        disabled={disabled}
        onChange={(e) => {
          change();
          setDirectory(e.target.value);
          setConsent(false);
        }}
        placeholder={
          template.profile_id === "c4_python_model_v2"
            ? "/absolute/path/to/wheels"
            : "/absolute/path/to/node_modules"
        }
      />
      <label className="check-label">
        <input
          type="checkbox"
          checked={consent}
          disabled={disabled || !available || !requested || pending === epoch}
          onChange={(e) => setConsent(e.target.checked)}
        />
        同意本次有界本地依赖读取、复制与存储（独立于只读来源检查）
      </label>
      <p className="field-help">
        最多 {template.limits.max_dependency_files} 个依赖文件 /{" "}
        {template.limits.max_dependency_bytes}{" "}
        bytes；读取已提交根锁文件。仅执行本地验证与保留，不授权模型、就绪、测试或交付。
      </p>
      <button
        type="button"
        disabled={!canImport}
        onClick={() => void importDependencies()}
      >
        {pending === epoch ? "正在导入依赖…" : "导入本地依赖"}
      </button>
      {error && (
        <p role="alert">
          依赖导入失败：{error}；没有可用准入结果，不会自动重试。
        </p>
      )}
      {selected ? (
        <DependencyFacts admission={selected} />
      ) : (
        <p role="status">未保留可用于当前选择的依赖准入结果</p>
      )}
    </div>
  );
}
