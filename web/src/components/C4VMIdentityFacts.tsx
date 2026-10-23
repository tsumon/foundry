import { Fragment } from "react";
import { c4VMIdentityValid, type C4VMIdentity } from "../c4";

export function C4VMIdentityFacts({ identity }: { identity?: C4VMIdentity }) {
  if (!c4VMIdentityValid(identity))
    return (
      <p className="warning-copy">
        内置 VM 资源未安装或未验证；不能创建或执行，不会自动回退 Docker。
      </p>
    );
  return (
    <dl className="metadata" aria-label="冻结 VM 身份">
      <dt>主机 → 客体</dt>
      <dd>
        {identity.host_platform} → {identity.guest_platform}
      </dd>
      <dt>协议 / 策略</dt>
      <dd>
        {identity.protocol} / {identity.policy_version}
      </dd>
      {(
        [
          ["资源清单 SHA-256", identity.manifest_digest],
          ["运行时 SHA-256", identity.runtime_digest],
          ["内核 SHA-256", identity.kernel_digest],
          ["根文件系统 SHA-256", identity.rootfs_digest],
          ["验证器 SHA-256", identity.verifier_digest],
        ] as const
      ).map(([label, digest]) => (
        <Fragment key={label}>
          <dt>{label}</dt>
          <dd>
            <code>{digest}</code>
          </dd>
        </Fragment>
      ))}
    </dl>
  );
}
