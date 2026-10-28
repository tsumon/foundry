<div align="center">

<img src="docs/media/foundry-icon.png" width="88" alt="Foundry">

# FOUNDRY

### 一个人的开发，也应该有清楚的交付过程。

计划 · 隔离修改 · 内置沙盒 · 失败修复 · 审批 · 本地补丁

[![Verify](https://github.com/tsumon/foundry/actions/workflows/verify.yml/badge.svg?branch=main)](https://github.com/tsumon/foundry/actions/workflows/verify.yml)
[![Desktop packages](https://github.com/tsumon/foundry/actions/workflows/desktop.yml/badge.svg?branch=main)](https://github.com/tsumon/foundry/actions/workflows/desktop.yml)
[![Version](https://img.shields.io/badge/version-0.1.0-2C56F5)](https://github.com/tsumon/foundry/releases/tag/v0.1.0)
[![Apache 2.0](https://img.shields.io/badge/license-Apache--2.0-2C56F5)](LICENSE)

[简体中文](README.md) · [English](README.en.md) · [安装与使用](docs/public/desktop-guide.md) · [架构](docs/public/architecture.md) · [安全边界](docs/public/safety.md)

[![下载 Mac DMG](https://img.shields.io/badge/macOS_Apple_Silicon-下载_DMG-24292F?style=for-the-badge)](https://github.com/tsumon/foundry/releases/download/v0.1.0/Foundry-0.1.0-arm64.dmg)
[![下载 Windows EXE](https://img.shields.io/badge/Windows_x64-下载_EXE-2563EB?style=for-the-badge)](https://github.com/tsumon/foundry/releases/download/v0.1.0/Foundry-0.1.0-x64.exe)

[v0.1.0 · 发行说明与全部文件](https://github.com/tsumon/foundry/releases/tag/v0.1.0) · [SHA-256 校验值](https://github.com/tsumon/foundry/releases/download/v0.1.0/SHA256SUMS)

</div>

<p align="center"><img src="docs/media/desktop-delivery.jpg" alt="实际安装后的 Foundry：独立审批完成的模拟交付与补丁、报告导出" width="100%"></p>
<p align="center"><sub>真实工作台截图，使用一次性合成数据。截图本身不是模型或沙盒执行证据。</sub></p>

Foundry 是面向一人团队的 **软件交付工作台**。接入你选定的 API 模型，从已提交的 Git 快照出发，生成计划、修改独立候选、在内置虚拟机中测试、修复明确的失败，再交付可审阅的 **patch 和 Markdown 报告**。原仓库不动，部署与应用补丁由你控制。

> [!IMPORTANT]
> **以上下载仍是支持标准库 Go 的正式版 0.1.0；当前源码正在准备候选版 0.2.0**，新增下表中的受限语言配置。候选版的原生 VM、安装与界面、升级、供应商和精确提交 CI 验收尚待完成，这不是 0.2.0 发布公告。内置 VM 不需要 Docker Desktop，虚拟化不可用时停止。API 模型是推荐路径，订阅登录仍为实验性功能。

## 三件事放在同一个工作台

| 看清计划 | 验证修改 | 留住证据 |
| :--- | :--- | :--- |
| Researcher 调研，Planner 规划；发送上下文和执行计划分别审批。计划可以修订。 | Worker 生成独立候选，受保护测试交给真实 VM；明确失败后由 Debugger 最多修复两轮。 | QA 与 Auditor 审查同一份候选和测试结果；最后单独批准导出，失败和已用预算仍保留。 |

## 候选版 0.2.0 的项目范围

| 语言 | 固定验证方式 | 依赖输入 |
| :--- | :--- | :--- |
| Go | Go 1.27.3 测试，保留 v1 配置 | 仅标准库 |
| JavaScript | Node 24.22.1 `node:test` | 预先准备的纯 JS `node_modules`，已提交的 npm lock v3 |
| TypeScript | TypeScript 5.9.3 类型检查，再运行 `node:test` | 同一冻结依赖树，含声明的类型定义；固定 `tsconfig.json` |
| Python | Python 3.13.5 `unittest` | 精确锁定的 `py3-none-any` 纯 wheel |

每次选择一种受支持的单包项目和框架。第三方依赖需在 Foundry 外预先准备，再单独同意复制本地依赖目录，之后检查已提交的源码。应用内置解释器与编译器，但不会获取任意项目依赖，也不运行 npm、pip 或安装脚本。测试、辅助目录、清单、锁文件、配置和导入依赖均受保护。源码与测试目录须符合[输入约定](docs/public/desktop-guide.md#candidate-020-language-and-dependency-inputs)；变更绑定选项后需重新导入或检查。

三种新配置仅使用 VM，不会降级到宿主机执行，不支持原生扩展、插件、monorepo 或其他测试框架。配置显示可用不等于就绪自检或测试已通过。类型检查、初始化、发现失败或零测试基础设施错误会停止任务，不能冒充通过或作为修复证据。[候选版验收与历史支持范围](docs/public/alpha-status.md)

## 下载与安装

以下固定下载仍是 v0.1.0；此处尚未提供候选版 0.2.0 的已发布安装包。

| 平台 | 安装包 | 对应第三方源码 |
| :--- | :--- | :--- |
| Apple Silicon Mac | [Foundry-0.1.0-arm64.dmg](https://github.com/tsumon/foundry/releases/download/v0.1.0/Foundry-0.1.0-arm64.dmg) | [运行时与沙盒源码](https://github.com/tsumon/foundry/releases/download/v0.1.0/Foundry-0.1.0-macos-sources.tar.gz) |
| Windows x64 | [Foundry-0.1.0-x64.exe](https://github.com/tsumon/foundry/releases/download/v0.1.0/Foundry-0.1.0-x64.exe) | [运行时与沙盒源码](https://github.com/tsumon/foundry/releases/download/v0.1.0/Foundry-0.1.0-windows-sources.tar.gz) |

下载后核对 [SHA256SUMS](https://github.com/tsumon/foundry/releases/download/v0.1.0/SHA256SUMS)。[Release](https://github.com/tsumon/foundry/releases/tag/v0.1.0) 保留对应提交、安装与升级验收、真实调用范围、干净源码和第三方许可材料；它是固定版本，不随 main 滚动替换。

**签名状态：** Mac 使用 ad-hoc 完整性签名，未作 Developer ID 签名或公证；Windows 安装包未签名，没有已验证发布者。校验值不等于发布者认证。不要关闭 Gatekeeper、SmartScreen 或杀毒软件；组织策略不允许时请采用审阅过的源码工作流。正式版本号不代表已获平台公证或商业服务承诺。

正式版安装包内置控制器、前端、Linux 客户系统和 Go 工具链；候选版 0.2.0 另打包固定的 Node/Python 解释器和 TypeScript 编译器。运行应用无需另装语言工具链或 Docker；准备第三方依赖输入可能需要 Foundry 外的工具。仓库导入需要本机受支持的 Git；Windows 还需已启用的硬件虚拟化及 Windows Hypervisor Platform。不会自动启用系统功能、安装服务或重启系统。Intel Mac、Windows ARM 和 Linux 桌面包暂不支持。详见[桌面指南](docs/public/desktop-guide.md)。

## 用自己的模型完成一次交付

在应用菜单选择 **Controlled Coding Workbench**：

1. **配置 API。** 在模型设置创建 OpenAI 或兼容 Responses 网关账户，保存 Key。用「API 快速配置」填入该服务的模型 ID，确认计费方式，再分配给新任务的角色。兼容 Magpie/CPA 的显式 Responses 端点；不需要 ChatGPT 登录，不在保存时查询目录或推理。[接入说明](docs/public/api-key-coding.md)
2. **导入仓库。** 正式版 0.1.0 选择已提交的标准库 Go；候选版 0.2.0 选择受支持配置，先单独导入预先准备的依赖。填写需求、可修改文件与受保护测试，检查精确的已提交快照，再确认模型、调用次数上限和沙盒。
3. **建立基线。** 分别审批并执行 VM 就绪检查、基线测试。基线必须通过；不会让模型顺带修复原本已失败的仓库。
4. **审批计划。** 确认发送的文件与接收端，授权 Researcher / Planner 生成计划。审阅或修订后，再单独批准执行。
5. **检验结果。** Worker 修改独立候选；真实测试明确失败时，Debugger 根据错误修复并复验。QA / Auditor 分别审查证据。网络、基础设施和结果不明的失败直接停止。
6. **交付。** 审批导出 patch 与报告，在产品外审查并应用。Foundry 不自动提交、推送、部署或修改原仓库。

API Key 在 Mac 存入 Keychain，在 Windows 存入 Credential Manager，不写入浏览器存储或明文配置。任务固定接收地址、账户和模型，不自动重试调用或切换收费 API。调用次数和超时 **不是金额上限**，请同时设置供应商支出限额。已发送但结果不明的调用也消耗预算，不会在重启后自动重放。

测试在 Linux 客户系统中执行，不等于验证原生 DMG、EXE、网页或移动应用。客户系统无网络、无主机共享，不安装外部依赖。已有 Docker 的 Mac 用户可为新 Go 任务显式选择固定本地镜像，仍需独立审批；三种新语言配置仅支持 VM。

## 验收证据怎样读

| 证据 | 覆盖范围 |
| :--- | :--- |
| [历史 v0.1.0 Mac Go CPA/Grok 真实交付](docs/public/compatible-api-acceptance.md) | 5 次真实角色调用；基线和候选各 14 项真实 VM 测试通过，原仓库与受保护测试未改。首版通过，未触发真实 Debugger。 |
| [历史 v0.1.0 Mac Go 故障修复交付](docs/public/compatible-api-acceptance.md#real-repair-and-delivery--stable-candidate) | 6 个角色各一次真实调用；明确注入的错误被 VM 检出，Debugger 修复后 9 项测试通过。不是模型自然出错的宣称。 |
| 版本 Release 的验收回执 | 区分供应商调用、协议 fixture、安装升级与 VM 执行，按提交和安装包摘要绑定。 |
| 历史 v0.1.0 Windows 安装与 WHPX 验收 | 对应发行回执限定安装、界面、重启和 Go VM 失败→修复→复验；候选版 0.2.0 的 Windows 验收尚待完成。 |
| 候选版 0.2.0 原生矩阵 | 源码验收驱动定义各语言闭环及失败检查，模型回复标记为 `local_protocol_fixture/not_real_provider`；实际安装后的原生结果尚待完成。 |
| 本地演示 | `SYNTHETIC` / `synthetic_stub`，不调用云模型，也不执行 Go 编译器。 |

托管 Windows 无法访问绑定在 Mac 回环地址的供应商服务，因此 WHPX/模型协议 fixture 不能证明 Windows 真实供应商访问，旧 Mac Go 回执也不能证明新语言。OpenAI 订阅的历史响应失败仍保留，不因 API/Grok 路径成功而改写。每次真实验收只证明对应接收端、模型、项目及宿主机的结果。完整支持范围见[能力状态](docs/public/alpha-status.md)。

## 升级与恢复

更新应用不重置任务预算，不重写历史基线和证据。已有任务按完整摘要选择保留的旧沙盒，新任务使用当前默认沙盒。旧资源丢失或损坏就阻断，不偷偷换运行时。卸载应用保留任务数据；升级前请备份，恢复用一致性备份，不直接把旧程序指向已升级的数据。

订阅登录、目录查询和持续续期是独立的实验性授权；API 快速配置不需要它们。历史订阅任务不会被悄悄迁移到收费 API。详见[升级兼容](docs/public/desktop-parity-upgrade.md)和[会话续期](docs/public/session-renewal.md)。

## 不用账号也能体验

选择 **Foundry → New Isolated Local Repository Simulation**，打开 `SYNTHETIC` 任务，按基线 → 上下文 → 计划 → 修改/失败修复 → 交付的顺序审批。每次演示使用独立输入与数据库，退出后保留证据。**Persistent Workbench** 则提供项目、交付目标、环境、共享资源台账与任务关联，不混入演示数据。

MCP、Skills 和观察型 hooks 提供管理、发现和静态说明；桌面不开放外部脚本或任意工具执行。数据库迁移、部署、自动 apply 和 Git 写入不属于本版能力。

## 从源码运行

开发基线：Go **1.27.3**、Node.js **22.23.2**、npm **10.9.8**、cgo/C 编译器和受支持的本机 Git。候选版 v2 的源码测试与构建另需显式指定可信 Python **3.13+** 路径和固定离线运行时/编译器输入，见[贡献指南](CONTRIBUTING.md#pinned-v2-inputs)。以下快速示例有意使用已发布的 v0.1.0 和模拟工作流。

```sh
git clone --branch v0.1.0 --depth 1 https://github.com/tsumon/foundry.git
cd foundry
npm ci --prefix web
./scripts/build.sh
go run ./cmd/foundry-acceptance --repository-tasks-fixture --web-dir web/dist
```

打开输出的随机回环 URL。此示例使用一次性数据和本地模拟，不读取真实凭据；其他 CLI 操作员模式的权限以各自配置为准。

| 使用 | 工程与安全 |
| :--- | :--- |
| [安装、演示与排障](docs/public/desktop-guide.md) | [架构与审批](docs/public/architecture.md) |
| [API 配置与调用边界](docs/public/api-key-coding.md) | [安全模型](docs/public/safety.md) |
| [版本能力与限制](docs/public/alpha-status.md) | [第三方声明](THIRD_PARTY_NOTICES.md) |
| [变更记录](CHANGELOG.md) | [开发与贡献](CONTRIBUTING.md) |

安全问题请使用 [GitHub 私密漏洞报告](https://github.com/tsumon/foundry/security/advisories/new)，不要公开凭据。

---

Copyright © 2026 [tsumon](https://github.com/tsumon). Foundry 源码采用 [Apache-2.0](LICENSE)，允许遵守许可条件的商业使用。Linux、QEMU、Electron 等第三方组件保留各自许可证，再分发时须保留相应源码和声明。无商业支持或服务等级承诺。
