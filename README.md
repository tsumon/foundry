<div align="center">

<img src="docs/media/foundry-icon.png" width="88" alt="Foundry">

# FOUNDRY

### 一个人的开发，也应该有清楚的交付过程。

计划 · 隔离修改 · 内置沙盒 · 失败修复 · 审批 · 本地补丁

[![Verify](https://github.com/tsumon/foundry/actions/workflows/verify.yml/badge.svg?branch=main)](https://github.com/tsumon/foundry/actions/workflows/verify.yml)
[![Desktop packages](https://github.com/tsumon/foundry/actions/workflows/desktop.yml/badge.svg?branch=main)](https://github.com/tsumon/foundry/actions/workflows/desktop.yml)
[![Alpha](https://img.shields.io/badge/stage-desktop%20alpha-F0A44B)](docs/public/alpha-status.md)
[![Apache 2.0](https://img.shields.io/badge/license-Apache--2.0-2C56F5)](LICENSE)

[简体中文](README.md) · [English](README.en.md) · [安装与使用](docs/public/desktop-guide.md) · [架构](docs/public/architecture.md) · [安全边界](docs/public/safety.md)

</div>

<p align="center">
  <img src="docs/media/desktop-delivery.jpg" alt="实际安装后的 Foundry：独立审批完成的模拟交付与 patch、报告导出" width="100%">
</p>
<p align="center"><sub>真实工作台截图，使用一次性合成数据。界面中的计划和状态不是对真实模型或 Docker 执行的认证。</sub></p>

Foundry 是面向一人团队的软件交付工作台：读取已提交的 Git 快照，把计划、候选修改、失败记录、复验和最终交付放在同一个可核对的流程里。原仓库保持只读，最后得到的是 **patch 与 Markdown 报告**，而不是一次未经审阅的自动推送。

> [!IMPORTANT]
> **这是有审批边界的 Coding Alpha，不是无人值守的自主开发产品。** 受控编程任务默认使用随应用安装的 Linux 轻量虚拟机，Docker 是显式可选项；不需要 Docker Desktop，也不会悄悄在宿主机执行仓库代码。模型接入与测试权限分别控制，安装和打开入口都不等于授权调用。原生沙盒验收使用本地模型协议响应，**不宣称真实账号端到端验收通过**；原纯模拟入口仍明确标记为 `synthetic_stub`。

## 先看到结果，再决定是否交付

| 看清计划 | 隔离修改 | 留住证据 |
| :--- | :--- | :--- |
| 上下文与执行分别审批；计划可修订，保存不等于批准。 | 从固定 commit 创建独立候选；保护测试，原仓库不动。 | 保留失败、Debugger 修复、复验与 QA/Auditor 审查；交付再单独批准。 |

## 安装桌面版

从 [Desktop packages](https://github.com/tsumon/foundry/actions/workflows/desktop.yml) 中对应平台通过全部门禁的任务下载 artifact；一个平台通过不代表另一个平台通过。核对其提交与 `SHA256SUMS`；保留随包提供的 runtime source 和 `sandbox-sources` 材料。**这里只使用 CI artifacts，不创建 GitHub Release；不要把旧版产物当成当前版本。**

### 已验收构建 · 2026-10-24

| 平台 / 提交 | 下载包 | 实际通过的验收 |
| :--- | :--- | :--- |
| Apple Silicon Mac · `46e1247` | [DMG + 校验值 + 运行时源码](https://github.com/tsumon/foundry/actions/runs/39964561670/artifacts/12506990609) | 本机安装、原生界面、重启；真实 Apple Virtualization 测试失败 → 修复 → 复验 |
| Windows x64 · `0c87188` | [EXE + 校验值 + 测试报告与截图](https://github.com/tsumon/foundry/actions/runs/39983328865/artifacts/12515976175) | 中文路径安装、跨盘数据目录、真实 WHPX 闭环；原生 UI 自动验收、重启、卸载后证据保留 |

核对 [Mac Verify](https://github.com/tsumon/foundry/actions/runs/39964561514)、[Windows Verify](https://github.com/tsumon/foundry/actions/runs/39983328874) 和[Windows 原生安装验收](https://github.com/tsumon/foundry/actions/runs/39983328865/job/120516374476)。以上证据只覆盖表中提交，**不是后续构建自动通过的保证**。两个平台均由真实虚拟机执行测试；角色响应为本地协议测试数据，未调用真实模型账号。

Mac 包使用 ad-hoc 完整性签名，未作 Developer ID 签名或公证；Windows 是未签名的当前用户安装包。Windows 需要硬件虚拟化和已启用的 Windows Hypervisor Platform，不会自动安装或启用这些系统功能。

应用内置前端、控制器、Linux 客户系统和 Go 工具链，不需要另装 Go、Node.js、npm、MSYS2 或 Docker。仓库功能仍需要受支持的本机 Git。虚拟化不可用时明确阻断，不启用系统功能、不自动重启，不降级到主机、软件模拟或 Docker。GitHub artifact 可能要求登录且会过期；不是永久下载地址。不要关闭系统安全保护来绕过未签名提示。平台限制、数据位置与卸载说明见[桌面使用指南](docs/public/desktop-guide.md)。

## 从需求到可审阅补丁

在应用菜单中选择 **Controlled Coding Workbench**，进入与普通工作台、演示数据隔离的持久编程空间：

1. 明确选择 OpenAI 账户、路由和各角色模型。API Key 与订阅是不同路由，不会互相兜底。Mac 编码入口可在模型账户卡中显式准备订阅连接，经单独审批、执行和手动浏览器授权后，再独立审批目录查询；从有效的本地目录证据保存模型连接并分配角色。普通桌面和 Windows 不开放这一准备入口。
2. 填写需求、已提交的 Go 仓库、可修改文件和受保护文件。默认内置沙盒，运行时摘要随任务冻结；创建任务不调用模型，也不启动虚拟机。
3. 分别审批并执行沙盒就绪检查、基线测试；基线通过后，再审批发送上下文，由 Researcher 和 Planner 生成计划。
4. 审阅或修订计划，单独批准执行。Worker 提出候选补丁；真实验证器发现明确的测试失败后，Debugger 最多修复两轮，再复验。
5. QA、Auditor 审查当前候选和测试证据；交付仍需独立批准，最后导出 patch 与报告。

**当前范围是使用标准库的受限 Go 项目，不是任意技术栈。** Mac 和 Windows 都运行 Linux 客户系统中的测试，不等于验证原生 DMG、EXE、浏览器或移动应用。没有依赖安装、主机共享目录或客户系统网络，不改原仓库或受保护测试。凭据过期、额度不足、结果不明或沙盒关闭未确认就停止；不会自动重试、刷新或改用收费 API。调用次数与超时不是费用硬上限。见[内置沙盒设计](docs/public/embedded-sandbox.md)和[使用指南](docs/public/desktop-guide.md)。

Mac 订阅接线目前仅有本地合成验收；没有验收真实 Keychain、登录、目录或模型任务。目录选择绑定账户、路由、授权代次/到期和回执，已有任务快照不变；目录不是推理批准。详见[订阅工作台](docs/public/mac-subscription-workbench.md)。

需要已有 Docker 环境时，可在**新任务**中明确选择 Docker（当前仅 Mac 受控路径）。它有自己的固定镜像、就绪检查和审批材料，不继承内置沙盒的批准。

## 不用账号，也能体验流程

打开应用菜单 **Foundry → New Isolated Local Repository Simulation**，选择一个标有 `SYNTHETIC` 的任务：

1. 检查基线材料，分别点击**批准**和**执行模拟基线**。
2. 核对上下文文件与模拟端点，再授权生成计划。
3. 审阅或修订计划，然后批准执行。Worker 只修改独立候选。
4. 在“验证”里检查**失败 → Debugger 修复 → 模拟复验 → QA/Auditor**的完整记录。
5. 单独批准交付，保存 patch 与报告。应用补丁前，在 Foundry 外再次审查。

每次模拟创建独立输入和数据库，退出后保留证据。**Persistent Workbench** 是另一个持久工作台入口，不会把演示数据混入其中，也不会自动为你连接模型。

已有一次性仓库？用 **Open Committed Descriptor Repository…** 选择包含已提交 `foundry.task.json` 的仓库。它支持有边界的多文件提案、新文件和依赖步骤；未提交内容不参与任务。描述符仍是预设提案，不是模型自主理解任意仓库。参阅[任务格式与限制](docs/public/repository-task-orchestration.md)。

## 哪些能用，哪些仍关闭

| 能力 | 当前真实边界 |
| :--- | :--- |
| 项目与资产工作台 | 项目、交付目标、环境、共享资源台账与任务关联 |
| 模型驱动仓库任务 | 单独启用的编程入口；OpenAI 冻结账户/模型快照、已提交 Go 仓库、独立候选、修复与导出 |
| 默认内置沙盒 | Mac Virtualization / Windows WHPX；无客户系统网络和主机共享，Go 测试由独立客户系统执行；原生验收与模拟分开记录 |
| 演示 Agent / 验证 | 本地脚本响应与内容断言；**不是**真实模型推理或编译器测试 |
| 审批与恢复 | 控制器绑定 revision/digest；重启不自动重放未知结果或扩大授权 |
| MCP / Skills / hooks | 管理、发现与静态说明；源码 CLI 有单独获批的本地 MCP fixture。桌面演示不启用外部工具执行 |
| 可选 Docker | Mac 显式选择已有固定镜像，独立就绪、基线和候选审批；Windows Docker 仍关闭，不作为兜底 |
| 真实模型 / 订阅 | 生产协议已接线；真实账户验收另行授权。任务不隐式登录、查目录、刷新、重试或切换收费 API |
| 自动 apply / Git 写入 / 部署 / 数据库迁移 | **不提供**；导出的 patch 由你在产品外处理 |

角色是职责，不是权限来源。模型审查不等于测试证据，模拟通过不等于真实项目已验证。完整范围见 [Alpha 状态](docs/public/alpha-status.md)。

## 从源码运行

Mac 开发基线：Go **1.27.3**、Node.js **22.23.2**、npm **10.9.8**、cgo/C 编译器。系统 `/usr/bin/git --no-lazy-fetch --version` 必须成功。

```sh
git clone https://github.com/tsumon/foundry.git
cd foundry
npm ci --prefix web
./scripts/build.sh
go run ./cmd/foundry-acceptance --repository-tasks-fixture --web-dir web/dist
```

打开命令输出的随机回环 URL。这个入口使用一次性数据，不读取真实凭据、不执行 Docker。普通 CLI 与专用操作员模式有不同能力，不能由演示的隔离性推断全部 CLI 都离线。不要复用个人运行数据或在评估中填入真实凭据。

开发、验证和桌面构建步骤见[贡献指南](CONTRIBUTING.md)与[桌面设计](docs/public/desktop-delivery.md)。

## 深入了解

| 使用 | 工程与安全 |
| :--- | :--- |
| [桌面安装、演示、排障](docs/public/desktop-guide.md) | [架构与批准边界](docs/public/architecture.md) |
| [模型驱动编程与权限](docs/public/live-coding.md) | [安全模型](docs/public/safety.md) |
| [能力与验收范围](docs/public/alpha-status.md) | [第三方声明与运行时源码](THIRD_PARTY_NOTICES.md) |
| [变更记录](CHANGELOG.md) | [模型门禁设计（不代表真实接入）](docs/public/model-acceptance.md) |

发现安全问题请使用 [GitHub 私密漏洞报告](https://github.com/tsumon/foundry/security/advisories/new)，不要公开发布凭据或利用细节。

---

Copyright © 2026 [tsumon](https://github.com/tsumon). Foundry 源码采用 [Apache-2.0](LICENSE)，允许遵守许可条件的商业使用；第三方组件保留各自许可证。没有生产支持或服务等级承诺。
