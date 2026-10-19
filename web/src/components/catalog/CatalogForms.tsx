import { useState, type FormEvent, type ReactNode } from "react";
import type {
  CatalogCommand,
  CatalogSnapshot,
  DeliveryTarget,
  Environment,
  Project,
  Resource,
} from "../../catalogTypes";

export type Save = (command: CatalogCommand) => Promise<boolean>;
type Actions = { disabled: boolean; execute: Save };
export function CatalogForm({
  title,
  disabled,
  submit,
  onSubmit,
  children,
}: {
  title: string;
  disabled: boolean;
  submit: string;
  onSubmit: () => Promise<void>;
  children: ReactNode;
}) {
  const submitForm = (e: FormEvent) => {
    e.preventDefault();
    if (!disabled) void onSubmit();
  };
  return (
    <form className="catalog-form" aria-label={title} onSubmit={submitForm}>
      <fieldset disabled={disabled}>
        <legend>{title}</legend>
        {children}
        <button className="primary" type="submit">
          {submit}
        </button>
      </fieldset>
    </form>
  );
}
export function NameField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <label>
      {label}
      <input
        required
        maxLength={120}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  );
}
export function ProjectForm({
  project,
  disabled,
  execute,
}: Actions & { project?: Project }) {
  const [name, setName] = useState(project?.name ?? "");
  const [description, setDescription] = useState(project?.description ?? "");
  return (
    <CatalogForm
      title={project ? "项目资料" : "新项目"}
      disabled={disabled}
      submit={project ? "保存项目" : "新建项目"}
      onSubmit={async () => {
        const input = { name: name.trim(), description };
        if (
          await execute(
            project
              ? { kind: "project.update", input: { ...input, id: project.id } }
              : { kind: "project.create", input },
          )
        ) {
          if (!project) {
            setName("");
            setDescription("");
          }
        }
      }}
    >
      <NameField label="项目名称" value={name} onChange={setName} />
      <label>
        项目说明
        <textarea
          maxLength={2000}
          rows={3}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
        />
      </label>
    </CatalogForm>
  );
}

const matrix = {
  windows: {
    format: "exe",
    architectures: ["x64", "arm64"],
    distributions: ["direct"],
  },
  macos: {
    format: "dmg",
    architectures: ["x64", "arm64", "universal"],
    distributions: ["direct"],
  },
  android: {
    format: "apk",
    architectures: ["arm64", "x86_64", "universal"],
    distributions: ["direct"],
  },
  ios: {
    format: "ipa",
    architectures: ["arm64"],
    distributions: ["development", "ad_hoc", "testflight", "app_store"],
  },
} as const;
export function TargetForm({
  projectId,
  disabled,
  execute,
}: Actions & { projectId: string }) {
  const [name, setName] = useState("");
  const [platform, setPlatform] =
    useState<DeliveryTarget["platform"]>("windows");
  const [architecture, setArchitecture] = useState("x64");
  const [distribution, setDistribution] = useState("direct");
  const options = matrix[platform];
  return (
    <CatalogForm
      title="登记交付目标"
      disabled={disabled}
      submit="登记目标"
      onSubmit={async () => {
        if (
          await execute({
            kind: "target.create",
            input: {
              project_id: projectId,
              name: name.trim(),
              platform,
              format: options.format,
              architecture,
              distribution,
            },
          })
        )
          setName("");
      }}
    >
      <NameField label="目标名称" value={name} onChange={setName} />
      <div className="catalog-fields">
        <label>
          目标平台
          <select
            value={platform}
            onChange={(e) => {
              const next = e.target.value as DeliveryTarget["platform"];
              setPlatform(next);
              setArchitecture(matrix[next].architectures[0]);
              setDistribution(matrix[next].distributions[0]);
            }}
          >
            {Object.keys(matrix).map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </label>
        <label>
          目标格式
          <select value={options.format} disabled>
            <option>{options.format}</option>
          </select>
        </label>
        <label>
          目标架构
          <select
            value={architecture}
            onChange={(e) => setArchitecture(e.target.value)}
          >
            {options.architectures.map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </label>
        <label>
          目标分发
          <select
            value={distribution}
            onChange={(e) => setDistribution(e.target.value)}
          >
            {options.distributions.map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </label>
      </div>
      <p className="muted small">
        格式由平台限定，仅登记意向；不会构建、签名或上传安装包。
      </p>
    </CatalogForm>
  );
}
export function EnvironmentForm({
  projectId,
  disabled,
  execute,
}: Actions & { projectId: string }) {
  const [name, setName] = useState("");
  const [tier, setTier] = useState<Environment["tier"]>("dev");
  return (
    <CatalogForm
      title="登记环境"
      disabled={disabled}
      submit="登记环境"
      onSubmit={async () => {
        if (
          await execute({
            kind: "environment.create",
            input: { project_id: projectId, name: name.trim(), tier },
          })
        )
          setName("");
      }}
    >
      <NameField label="环境名称" value={name} onChange={setName} />
      <label>
        环境级别
        <select
          value={tier}
          onChange={(e) => setTier(e.target.value as Environment["tier"])}
        >
          {["local", "dev", "staging", "production"].map((value) => (
            <option key={value}>{value}</option>
          ))}
        </select>
      </label>
      <p className="muted small">级别创建后固定，不探测环境、不创建目录。</p>
    </CatalogForm>
  );
}
export function ResourceForm({
  snapshot,
  disabled,
  execute,
}: Actions & { snapshot: CatalogSnapshot }) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<Resource["kind"]>("service");
  const [owner, setOwner] = useState("");
  const [provider, setProvider] = useState<Resource["provider"]>("manual");
  const [account, setAccount] = useState("");
  const [region, setRegion] = useState("");
  const [externalId, setExternalId] = useState("");
  const ownerExists =
    !owner || snapshot.projects.some((project) => project.id === owner);
  return (
    <CatalogForm
      title="登记工作空间资源"
      disabled={disabled}
      submit="登记资源"
      onSubmit={async () => {
        if (!ownerExists) return;
        if (
          await execute({
            kind: "resource.create",
            input: {
              name: name.trim(),
              kind,
              owner_project_id: owner || null,
              provider,
              account,
              region,
              external_id: externalId,
            },
          })
        ) {
          setName("");
          setAccount("");
          setRegion("");
          setExternalId("");
        }
      }}
    >
      <NameField label="资源名称" value={name} onChange={setName} />
      <div className="catalog-fields">
        <label>
          资源类型
          <select
            value={kind}
            onChange={(e) => setKind(e.target.value as Resource["kind"])}
          >
            {["service", "database", "storage", "domain", "runner"].map(
              (value) => (
                <option key={value}>{value}</option>
              ),
            )}
          </select>
        </label>
        <label>
          资源归属
          <select value={owner} onChange={(e) => setOwner(e.target.value)}>
            <option value="">工作空间共享</option>
            {!ownerExists && (
              <option value={owner}>原项目已不可用，请重新选择</option>
            )}
            {snapshot.projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          资源提供方
          <select
            value={provider}
            onChange={(e) => {
              const next = e.target.value as Resource["provider"];
              setProvider(next);
              if (next === "manual") {
                setAccount("");
                setRegion("");
                setExternalId("");
              }
            }}
          >
            {["manual", "local", "aws", "gcp", "azure", "apple"].map(
              (value) => (
                <option key={value}>{value}</option>
              ),
            )}
          </select>
        </label>
      </div>
      {provider !== "manual" && (
        <div className="catalog-fields">
          {(
            [
              ["账号标识", account, setAccount],
              ["区域标识", region, setRegion],
              ["外部资源标识", externalId, setExternalId],
            ] as const
          ).map(([label, value, setter]) => (
            <label key={label}>
              {label}
              <input
                required
                maxLength={256}
                pattern="[A-Za-z0-9_.:\/\-]+"
                value={value}
                onChange={(e) => setter(e.target.value)}
              />
            </label>
          ))}
        </div>
      )}
      <p className="muted small">
        归属和身份创建后固定。仅允许非秘密标识，禁止密钥、连接串及
        URL。保护：保留。
      </p>
    </CatalogForm>
  );
}

export function RenameForm({
  kind,
  id,
  name,
  disabled,
  execute,
}: Actions & {
  kind: "target.rename" | "environment.rename" | "resource.rename";
  id: string;
  name: string;
}) {
  const [draft, setDraft] = useState(name);
  const [editing, setEditing] = useState(false);
  if (!editing)
    return (
      <button
        disabled={disabled}
        onClick={() => {
          setDraft(name);
          setEditing(true);
        }}
      >
        重命名 {name}
      </button>
    );
  return (
    <CatalogForm
      title={`重命名 ${name}`}
      disabled={disabled}
      submit="保存名称"
      onSubmit={async () => {
        if (await execute({ kind, input: { id, name: draft.trim() } }))
          setEditing(false);
      }}
    >
      <NameField label={`新名称 ${name}`} value={draft} onChange={setDraft} />
      <button type="button" onClick={() => setEditing(false)}>
        取消
      </button>
    </CatalogForm>
  );
}
