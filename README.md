<div align="center">

<img src="docs/media/foundry-icon.png" width="88" alt="Foundry">

# FOUNDRY

### 一个人的开发，也应该有清楚的交付过程。

计划 · 独立候选 · 失败修复 · 验证证据 · 审批 · 本地补丁

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
> **这是可运行的本地流程 Alpha，不是已接通云模型的自主编程产品。** 桌面演示使用脚本化 Agent 和精确内容检查，明确标为 `synthetic_stub`；不运行真实模型或 Docker。真实账号和订阅接入仍暂停，安装应用不会开启它们。

## 先看到结果，再决定是否交付

| 看清计划 | 隔离修改 | 留住证据 |
| :--- | :--- | :--- |
| 上下文与执行分别审批；计划可修订，保存不等于批准。 | 从固定 commit 创建独立候选；保护测试，原仓库不动。 | 保留失败、Debugger 修复、复验与 QA/Auditor 审查；交付再单独批准。 |

## 安装桌面版

从 [Desktop packages 的成功运行](https://github.com/tsumon/foundry/actions/workflows/desktop.yml)下载对应平台的 artifact。核对其提交与 `SHA256SUMS`；保留随包提供的 runtime source 材料。**这里只使用 CI artifacts，不创建 GitHub Release。**

| 平台 | 安装包 | 需要知道 |
| :--- | :--- | :--- |
| Apple Silicon Mac | `Foundry-0.1.0-alpha.1-arm64.dmg` | 复制到 Applications；本地 ad-hoc 签名，未公证 |
| Windows x64 | `Foundry-0.1.0-alpha.1-x64.exe` | 当前用户安装；未做代码签名，不要求管理员权限 |

应用内置前端和本地控制器，运行不需要另装 Go、Node.js、npm 或编译器。仓库功能需要受支持的本机 Git。GitHub 下载 artifact 可能要求登录，且产物会过期；不是永久下载地址。请不要关闭系统安全保护来绕过未签名提示。平台限制、Git 要求、数据位置与卸载说明见[桌面使用指南](docs/public/desktop-guide.md)。

## 五分钟走完本地闭环

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
| 本地仓库任务 | 已提交快照、独立候选、受保护文件、计划修订、失败归档、修复与导出 |
| 演示 Agent / 验证 | 本地脚本响应与内容断言；**不是**真实模型推理或编译器测试 |
| 审批与恢复 | 控制器绑定 revision/digest；重启不自动重放未知结果或扩大授权 |
| MCP / Skills / hooks | 管理、发现与静态说明；源码 CLI 有单独获批的本地 MCP fixture。桌面演示不启用外部工具执行 |
| Docker | 源码已有受控适配器与历史小范围验收；**桌面入口关闭**，本轮未重跑 |
| 真实模型 / 订阅 | **暂停**。不登录、不刷新、不自动重试、不切换收费 API |
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
| [仓库任务描述符](docs/public/repository-task-orchestration.md) | [安全模型](docs/public/safety.md) |
| [能力与验收范围](docs/public/alpha-status.md) | [第三方声明与运行时源码](THIRD_PARTY_NOTICES.md) |
| [变更记录](CHANGELOG.md) | [模型门禁设计（不代表真实接入）](docs/public/model-acceptance.md) |

发现安全问题请使用 [GitHub 私密漏洞报告](https://github.com/tsumon/foundry/security/advisories/new)，不要公开发布凭据或利用细节。

---

Copyright © 2026 [tsumon](https://github.com/tsumon). Foundry 源码采用 [Apache-2.0](LICENSE)，允许遵守许可条件的商业使用；第三方组件保留各自许可证。没有生产支持或服务等级承诺。
