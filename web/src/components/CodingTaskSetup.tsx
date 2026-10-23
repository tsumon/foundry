import { useCallback, useEffect, useRef, useState } from "react";
import { errorMessage } from "../api";
import { codingTaskApi, type CodingTaskCapability } from "../codingTaskApi";
import { c4BackendLabel, c4ProfileSupported, type C4Profile } from "../c4";
import { C4VMIdentityFacts } from "./C4VMIdentityFacts";

export interface CodingTaskSelection {
  capability: CodingTaskCapability;
  profile: C4Profile;
}

const immutableImage = /^sha256:[a-f0-9]{64}$/;
const validAbsolutePath = (path: string) =>
  path.startsWith("/") &&
  path !== "/" &&
  !path.endsWith("/") &&
  !/[\\\0\r\n\t ]/.test(path) &&
  path
    .slice(1)
    .split("/")
    .every((part) => part !== "" && part !== "." && part !== "..");
const validRuntime = (dockerPath: string, endpoint: string) =>
  validAbsolutePath(dockerPath) &&
  endpoint.startsWith("unix://") &&
  !/[%?#]/.test(endpoint) &&
  validAbsolutePath(endpoint.slice("unix://".length));

export function CodingTaskSetup({
  disabled,
  requested,
  onRequestedChange,
  value,
  onChange,
}: {
  disabled: boolean;
  requested: boolean;
  onRequestedChange: (requested: boolean) => void;
  value: CodingTaskSelection | null;
  onChange: (selection: CodingTaskSelection | null) => void;
}) {
  const [capability, setCapability] = useState<CodingTaskCapability | null>(
    null,
  );
  const [imageID, setImageID] = useState("");
  const [backend, setBackend] = useState("embedded_vm");
  const [dockerPath, setDockerPath] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const requestEpoch = useRef(0);
  const mounted = useRef(true);
  const explicitBackend = useRef<string | null>(null);

  const read = useCallback(async () => {
    const epoch = ++requestEpoch.current;
    onChange(null);
    setLoading(true);
    setError("");
    try {
      const next = await codingTaskApi.capability();
      if (!mounted.current || requestEpoch.current !== epoch) return;
      const mode =
        explicitBackend.current ?? next.default_backend ?? next.profile.mode;
      const frozen =
        next.backends?.find((item) => item.mode === mode)?.profile ??
        (next.profile.mode === mode ? next.profile : null);
      if (!frozen) throw new Error("已选后端不可用；不会自动更换后端");
      setCapability(next);
      setBackend(mode);
      setImageID(frozen.image_id);
      setDockerPath(frozen.docker_path);
      setEndpoint(frozen.endpoint);
    } catch (cause) {
      if (!mounted.current || requestEpoch.current !== epoch) return;
      setCapability(null);
      setError(errorMessage(cause));
    } finally {
      if (mounted.current && requestEpoch.current === epoch) setLoading(false);
    }
  }, [onChange]);

  useEffect(() => {
    mounted.current = true;
    void read();
    return () => {
      mounted.current = false;
      requestEpoch.current += 1;
    };
  }, [read]);

  const selectedBackend = capability?.backends?.find(
    (item) => item.mode === backend,
  );
  const selectedProfile = selectedBackend?.profile ?? capability?.profile;
  const available =
    capability?.enabled === true &&
    (selectedBackend?.available ?? capability?.available) === true;

  useEffect(() => {
    if (loading || !requested || !capability?.enabled || !available) {
      if (value) onChange(null);
      return;
    }
    const profile = {
      ...selectedProfile!,
      ...(backend === "docker"
        ? {
            image_id: imageID.trim(),
            docker_path: dockerPath.trim(),
            endpoint: endpoint.trim(),
          }
        : {}),
    };
    if (!c4ProfileSupported(profile)) {
      if (value) onChange(null);
      return;
    }
    if (
      value?.capability === capability &&
      JSON.stringify(value.profile) === JSON.stringify(profile)
    )
      return;
    onChange({ capability, profile });
  }, [
    capability,
    dockerPath,
    endpoint,
    imageID,
    loading,
    onChange,
    requested,
    value,
    available,
    selectedProfile,
    backend,
  ]);
  return (
    <section className="controlled-create" aria-label="账户模型编码设置">
      <h3>账户模型编码 · {c4BackendLabel(backend)}</h3>
      <p className="field-help">
        这是现有任务创建的一种显式配置，不是新的导航或授权捷径。读取配置不会联系模型、读取凭据或启动
        Docker 或 VM。
      </p>
      {loading && (
        <p role="status">
          正在读取编码能力元数据；
          {requested ? "请求已保留，但配置暂不可用于创建…" : "保持关闭…"}
        </p>
      )}
      {error && (
        <p role="status" className="warning-copy">
          能力元数据不可用：{error}；
          {requested
            ? "已保留账户模型编码请求，但配置无效且不会回退到普通任务。"
            : "账户模型编码保持关闭。"}
          不会自动重试。
        </p>
      )}
      {!loading && capability && !available && (
        <p className="warning-copy">
          账户模型编码不可用：
          {!capability.default_backend || backend === capability.default_backend
            ? capability.reason || "运行时未启用"
            : "所选后端不可用"}
          。配置记录不是授权或成功证据。
        </p>
      )}
      {capability && (
        <>
          {capability.backends && (
            <>
              <label htmlFor="coding-backend">测试沙箱后端</label>
              <select
                id="coding-backend"
                value={backend}
                disabled={disabled || loading}
                onChange={(event) => {
                  const mode = event.target.value;
                  explicitBackend.current = mode;
                  const selected = capability.backends?.find(
                    (item) => item.mode === mode,
                  );
                  onChange(null);
                  setBackend(mode);
                  setImageID(selected?.profile.image_id ?? "");
                  setDockerPath(selected?.profile.docker_path ?? "");
                  setEndpoint(selected?.profile.endpoint ?? "");
                }}
              >
                {capability.backends.map((item) => (
                  <option key={item.mode} value={item.mode}>
                    {c4BackendLabel(item.mode)}
                    {item.mode === "embedded_vm" ? "（默认）" : "（显式选择）"}
                  </option>
                ))}
              </select>
              <p className="field-help">
                {selectedBackend?.prerequisite}
                。切换后端改变审批材料；不会安装系统功能、自动回退或授权执行。
              </p>
            </>
          )}
          {backend === "embedded_vm" && (
            <C4VMIdentityFacts identity={selectedProfile?.embedded_vm} />
          )}
          <dl className="metadata">
            <dt>运行时 / 提供商</dt>
            <dd>
              {available ? "可配置" : "不可用"} ·{" "}
              {capability.provider_families.join("、") || "未提供"}
            </dd>
            <dt>固定配置</dt>
            <dd>
              <code>{selectedProfile?.id}</code> · {selectedProfile?.platform}
            </dd>
            <dt>固定验证命令</dt>
            <dd>
              <code>go test -json -count=1 -timeout=90s ./...</code>
            </dd>
            <dt>调用与修复限制</dt>
            <dd>
              最多 {capability.limits.max_calls} 次模型调用 · 最多{" "}
              {capability.limits.max_repairs} 轮修复
            </dd>
            <dt>独立审批</dt>
            <dd>{capability.approval_required.join(" → ")}</dd>
          </dl>
          <p className="note">
            元数据可用不等于已授权。创建只冻结仓库、七角色账户/模型/消费类别与此配置；readiness、baseline、context、execute、delivery
            仍分别决定。Token
            用量不是订阅剩余额度；界面不宣称金额硬上限，也不会自动重试、换模型或付费降级。
          </p>
        </>
      )}
      <label className="check-label">
        <input
          type="checkbox"
          checked={requested}
          disabled={disabled || loading || (!available && !requested)}
          onChange={(event) => {
            const next = event.target.checked;
            onRequestedChange(next);
            if (!next) onChange(null);
          }}
        />
        {backend === "embedded_vm" && capability
          ? "启用账户模型与内置 VM"
          : "启用账户模型与受控 Docker"}
        （仅创建配置，不批准或执行）
      </label>
      {requested && capability && backend === "docker" && (
        <div className="form-columns">
          <div>
            <label htmlFor="coding-image-id">不可变本地镜像 ID</label>
            <input
              id="coding-image-id"
              value={imageID}
              onChange={(event) => setImageID(event.target.value)}
              required
              placeholder="sha256:…"
            />
            {!immutableImage.test(imageID.trim()) && (
              <p className="warning-copy">尚未填写不可变镜像 ID；不会创建。</p>
            )}
          </div>
          <div>
            <label htmlFor="coding-docker-path">Docker 可执行文件</label>
            <input
              id="coding-docker-path"
              value={dockerPath}
              onChange={(event) => setDockerPath(event.target.value)}
              required
            />
            {!validRuntime(dockerPath.trim(), endpoint.trim()) && (
              <p className="warning-copy">
                Docker 路径与 unix endpoint 必须是规范绝对路径；不会创建。
              </p>
            )}
            <label htmlFor="coding-endpoint">Docker endpoint</label>
            <input
              id="coding-endpoint"
              value={endpoint}
              onChange={(event) => setEndpoint(event.target.value)}
              required
            />
          </div>
        </div>
      )}
      <button
        type="button"
        disabled={disabled || loading}
        onClick={() => void read()}
      >
        刷新编码能力
      </button>
    </section>
  );
}
