import { c4ProfileSupported, type C4Profile } from "../c4";
import { languageContracts, languageProfileID } from "../codingV2";

export function C4LanguageFacts({
  profile: p,
  compact = false,
}: {
  profile?: C4Profile;
  compact?: boolean;
}) {
  if (p?.schema_version !== 2) return null;
  if (!c4ProfileSupported(p) || !languageProfileID(p.id) || !p.v2)
    return (
      <p className="warning-copy">
        语言 v2 配置不完整或不受支持；不能确认运行时或验证结果。
      </p>
    );
  const c = languageContracts[p.id];
  const facts = (
    <div aria-label="冻结语言与依赖合约">
      <dl className="metadata compact-metadata">
        <dt>固定语言 / 框架</dt>
        <dd>
          {c.label} · {c.framework}
        </dd>
        <dt>固定 runner</dt>
        <dd>{p.v2.runner_id}</dd>
        <dt>固定验证命令</dt>
        <dd>
          <code>{c.command}</code>
        </dd>
        <dt>运行时清单摘要</dt>
        <dd>
          <code>{p.v2.runtime_inventory_digest}</code>
        </dd>
        <dt>保护依赖包摘要</dt>
        <dd>
          <code>{p.v2.dependency_bundle_digest}</code>
        </dd>
        <dt>保护输入摘要</dt>
        <dd>
          <code>{p.v2.protected_inputs_digest}</code>
        </dd>
        <dt>执行输入合约摘要</dt>
        <dd>
          <code>{p.v2.execution_contract_digest}</code>
        </dd>
      </dl>
      <p className="field-help">
        仅纯语言依赖；不执行安装脚本。运行时、源代码、测试 /
        支持目录与依赖合约冻结；不展示依赖内容或本地存储路径。声明不等于就绪或通过；测试用例数仅来自实际执行证据。
      </p>
    </div>
  );
  return compact ? (
    <details>
      <summary>冻结 {c.label} 语言与依赖合约</summary>
      {facts}
    </details>
  ) : (
    facts
  );
}
