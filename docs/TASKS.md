# 任务清单

> 更新：2026-08-14（Phase 1 全部收官；Phase 2 主链路已落地并通过全量 E2E；**新增配置自动化专项规划**）  
> 产品边界：**Gitea 优先** · 内置 Agent 默认可用 · **可插拔 harness 执行内核**（builtin / OpenCode / Hermes / Phase 3 的 Pi·Codex·Claude） · Matea 是 Gitea 唯一写方 · 不自研 IM SDK · 不引入外部 harness SDK  
> 决策：  
> - [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md)  
> - [20260805-Phase2-plan.md](20260805-Phase2-plan.md)  
> - 配置自动化细化方案 → [CONFIG-AUTOMATION.md](CONFIG-AUTOMATION.md)  
> 已归档交付记录：  
> - **Phase 1 + Phase 2 已完成部分** → [archived/20260814-TASKS.md](archived/20260814-TASKS.md)  
> - v0.11.4 任务清单 → [archived/20260803-TASKS.md](archived/20260803-TASKS.md)  
> - P0–P2 核心演进 → [archived/20260716-TASKS.md](archived/20260716-TASKS.md)  
> - P3 开源 + 开源后加固 → [archived/20260723-TASKS.md](archived/20260723-TASKS.md)  
> - 写路径 / Agent 摩擦 / Bootstrap（Issue #4）→ [archived/20260724-TASKS.md](archived/20260724-TASKS.md)  
> 架构评估（点时）→ [archived/20260722-architecture-evaluation.md](archived/20260722-architecture-evaluation.md)

---

## 演进主线

```text
P0–P2 → P3 → 写路径/摩擦/Bootstrap（已归档）→ PR 续作注入 review / 逻辑 Issue 归一 / Agent 并发 / 可观测性（已交付）
        │
        ├─► Phase 1：HubBackend 抽象 + Gitea 触发入口整理 + 体验简化 ✅（已归档）
        ├─► Phase 2：大脑可插拔地基 → Hub 后端接入（OpenCode / Hermes）→ deliver 出站 ✅（已归档）
        ├─► Phase 2 收尾：MCP Server（可选）、剩余跨系统验证场景、配置自动化 🔄（当前）
        ├─► Phase 3：多触发形态（REST/CLI）+ 策略配置化
        └─► Phase 4：可选 matea gateway serve + 拆包评估
```

---

## Phase 2：Hub 后端接入（剩余待办）

> 方案：[20260805-Phase2-plan.md](20260805-Phase2-plan.md)（D1–D12 已拍板，方案冻结）  
> 已完成部分已归档至 [archived/20260814-TASKS.md](archived/20260814-TASKS.md)。

### 2.3 MCP Server + Deliver（剩余项）

- [ ] **2.3.1 （可选）实现 `internal/ingress/mcp`：Matea 作为 MCP Server**
  若实现则**严格最小 4 工具**：`matea_get_issue_status` / `matea_assign_agent` / `matea_list_agents` / `matea_post_comment`。不做 `create_issue`/`list_open_issues`/`reset_session`。
  📌 降级原因：Matea↔Hermes 主链路为 HTTP Runs API，人类渠道由 Hermes gateway `deliver`（feishu/wecom 原生）承担；2.4 验收不依赖 MCP Server。
  ⚠️ **不可复用现有代码**：`internal/agent/tools_mcp.go` 是 MCP **Client**（Matea 消费外部 MCP），此处需要的是 MCP **Server**（Matea 暴露工具），方向相反，属全新实现，工作量按「从零起」计。
  📌 若落地，暴露的工具集须遵循 2.0.3 分层策略（Gitea 读侧 + 网关级 skill script；沙箱类默认不给）。
  🔶 **决策（2026-08-13）**：本期（phase2/hub-ecosystem 收尾）**不做，向后延迟至后续周期**。理由：MCP Server 为可选项（2.4 验收不依赖），且实现需从零起步（Server 与现有 Client 反向），投入产出比低；不影响 Phase 2 主进度与全量测试。

- [ ] **2.3.2 （可选）MCP 入站鉴权：API Key 或 mTLS**
  默认仅监听 localhost，公网配合反向代理。
  🔶 **决策（2026-08-13）**：随 2.3.1 一并**向后延迟**；2.3.1 不做则本项无承载对象。

- [x] **2.3.3 实现 `deliver` 模块：仅出站事件扇出** ✅（已归档）

- [x] **2.3.4 配置 `deliver.webhook_url`** ✅（已归档）

- [ ] **2.3.5 SystemConfig 页增加 MCP 配置块（Deliver 已完成）**
  SystemConfig 新增「MCP Server」Tab（enable + listen + auth）。即便 2.3.1 可选，配置块保留以便启用。
  🔶 **进度（2026-08-13，S2 + 决策更新）**：「Deliver」Tab 已落地（webhook_url/timeout/max_retries 三字段，见 2.3.4）。「MCP Server」Tab **本期不做、向后延迟**——随 2.3.1 决策（MCP Server 实现延迟），后端无 `mcp.*` schema，做 UI 即无后端支撑的死 UI；待后续周期 2.3.1 落定后再补对应 Tab 与 keys.go 白名单。

- [ ] **2.3.6 飞书/企微/钉钉等渠道不自研 SDK，文档给出推荐拓扑**
  文档给出推荐拓扑。
  🔶 **备注（2026-08-13）**：属文档/架构决策项，非代码阻塞点；「不自研 SDK、走 Hub 或用户 bridge」的立场已在 2.2.4 / 2.3.3 / review 报告多处落地，推荐拓扑文档可在后续周期补，不阻塞本期全量测试。

### 2.4 验证场景（mock Hub 验收，不依赖真实飞书）

- [ ] **2.4.1 飞书用户「帮我看看 issue #12」 → Hermes 经 runs API 查 Gitea 上下文（若 MCP 落地则 `matea_get_issue_status`）→ 回飞书**
  🔶 依赖 2.3.1 MCP Server，向后延迟。

- [ ] **2.4.2 飞书用户「让 AI 改」 → Hermes `matea_assign_agent`（或 Matea HTTP API）→ Matea 执行 → 写回 Gitea**
  🔶 依赖 2.3.1 MCP Server，向后延迟。

- [x] **2.4.3 PR 合并后，Matea deliver 出站事件 → 用户飞书** ✅（已归档）

- [ ] **2.4.4 Gitea Assign `@matea-coder`（backend=hub-opencode）→ OpenCode 执行 → 结果写回 Gitea**
  需在真实 OpenCode 环境下跑通完整写任务链路（workspace 制备 → OpenCode 执行 → patch/summary 返回 → Matea finalize → PR 创建）。当前代码路径已具备，缺端到端真实环境验证。

---

## Phase 2.5：配置自动化与首次用户体验（新增）

> 目标：让 MATEa 配置从「填表」变成「向导」，从 70 个字段变成 15 行 + 点几下。  
> 详细方案：[CONFIG-AUTOMATION.md](CONFIG-AUTOMATION.md)（细化版）  
> 实施顺序建议：P0 与 Phase 2 收尾并行开发 → P1 → P2。

### P0（优先落地）

- [ ] **C-1 `/setup` 向导页面（三步：Gitea → LLM → 确认）**
  前端新建 `SetupWizard.vue`；后端支持批量配置写入；完成后跳转强制改密。

- [ ] **C-2 Setup Token 安全模型**
  首次启动生成一次性随机 token，打印在控制台；30 分钟有效期；与默认密码解耦；未初始化时 `/setup` 可免登访问。

- [ ] **C-3 未初始化时 `/` 自动跳转 `/setup`，初始化后 `/setup` 跳登录**
  前端路由守卫根据 `/api/setup/status` 判断。

- [ ] **C-4 自动检测本地 Ollama**
  扫描 `http://localhost:11434/api/tags`；命中后自动填充 base_url 并拉取模型列表。

- [ ] **C-5 自动检测本地 OpenCode（端口可配置）**
  允许用户输入端口，扫描 `/health`；命中后提示可在 Hub 后端配置中接入。

- [ ] **C-6 `gitea.webhook_secret` 可选，留空自动生成**
  修改 `internal/config/setup.go:CheckSetup` 不再把 `webhook_secret` 视为必填；向导完成时自动生成 32 字节 hex 写入 DB。

- [ ] **C-7 Gitea Token scope 自检**
  `TestConnection()` 调用 `/api/v1/user` 解析权限；至少检查 `write:admin` + `repo`。

- [ ] **C-8 配置写入 DB + 热重启组件**
  复用现有 `ConfigManager.Update()` 与 `onConfigChange` 回调；向导完成页批量写入并触发 LLM Registry / Gitea Client / Dispatcher 热更新。

- [ ] **C-9 首次登录强制修改默认管理员密码**
  向导完成后跳转 `/setup-password`；不改密码无法进入 Dashboard；支持 `auth.default_admin_password` 自动生成。

- [ ] **C-10 Dashboard 初始化引导卡**
  未初始化时显示引导卡片，点击跳转 `/setup`。

### P1

- [ ] **C-11 Provider 预设模板**
  DeepSeek / OpenAI / Anthropic / SenseNova / Ollama / 自定义；选择预设后自动填充 base_url + 默认模型，用户只填 API Key。

- [ ] **C-12 自动拉取 LLM 模型列表**
  测试连接时请求 `/models` 或 `/api/tags`，返回可选模型下拉。

- [ ] **C-13 站点级 Webhook 自动注册/状态检查**
  向导完成时调用 `GET /api/v1/admin/hooks` 检查目标 URL 是否已存在；可选自动注册站点级 webhook。

- [ ] **C-14 `agents.backends`（Hub 后端）配置子页面**
  在 `ConfigManager` 中新增对 `agents.backends` 的 CRUD 支持；或拆分为独立命名后端资源；SystemConfig 新增「Hub 后端」Tab。

- [ ] **C-15 精简 `config.example.yaml` 到 ~15 行**
  只保留 `server` / `database` / 可选的 `gitea` / `llm` / `auth`；其余全部移到 Web UI 或自动检测。

- [ ] **C-16 配置变更审计日志**
  利用 `operation_logs` 表记录 who/when/key（敏感值 mask）。

- [ ] **C-17 敏感配置字段 API 不回显、前端 mask**
  `gitea.admin_token`、`llm.providers.*.api_key`、`gitea.webhook_secret` 等返回时 mask，表单显示 `••••••••`。

### P2

- [ ] **C-18 一键连接测试（Gitea/LLM/Hub 后端状态灯）**
  Dashboard 或 SystemConfig 顶部显示各组件健康状态。

- [ ] **C-19 健康检查面板**
  汇总 Gitea / LLM / Hub 后端 / Deliver / DB 状态。

- [ ] **C-20 配置导入/导出（JSON，不含 secrets）**
  支持将 `system_config` 导出为 JSON（敏感字段脱敏），并在新实例导入。

- [ ] **C-21 环境变量自动发现与提示**
  启动时检测 `GITEA_URL`、`SENSENOVA_API_KEY` 等，向导中提示「检测到环境变量，是否使用」。

- [ ] **C-22 文件系统可用空间预警**
  启动时 `statfs` 检查 `data/` 所在分区，低于阈值时日志/前端提示。

---

## Phase 3：多触发形态与策略配置化（3–6 月）

- [ ] **3.1 实现 `internal/ingress/api`：REST API 直接提交任务**
  `POST /api/v1/tasks` 标准化入口。

- [ ] **3.2 CLI 触发：`matea run --repo=... --issue=... --agent=...`**

- [ ] **3.3 把 workflow gate 拆为独立策略单元**
  `require-analysis-before-code`、`no-self-review` 等可配置。

- [ ] **3.4 策略 Skill 化：高级用户可写 YAML/JSON 策略**
  与 Agent 解耦。

- [ ] **3.5 Cron 扫描：定时处理 backlog issue/PR**
  可选触发方式。

- [ ] **3.6 评估是否拆包：`matea-core` / `matea` / `hub-hermes` / `hub-opencode`**
  先在一个仓库内抽象，后端数量增多后再拆。

- [ ] **3.7 新增 harness：Pi / Codex / Claude Code（D12，机制验证后 trivial）**
  接入方式 = **spawn 其 CLI 或连其本地 server（out-of-process）**，**不引入任何外部 SDK**（全为 TS/Python，无法嵌进 Go 进程）。每新增一个 = 实现一个 `Harness` + 注册表加一行。

- [ ] **3.8 Gitea 写侧工具评估（D11 挂账）**
  `gitea_create_comment` / `gitea_create_pr` 是否暴露给 harness。风险：推理中途可写 → 乱序评论、重复评论、部分失败不可回滚。需先给出幂等与回滚方案再评估。

- [ ] **3.9 `workspace_transport=mcp`（L2 全隔离档，D11 部署档位）**
  真异地/跨组织边界部署，工作区经 MCP 文件工具抵达。⚠️ 性能税：agent loop 内每次 `read_file` 一次 round-trip，大仓库比 L0 慢一个数量级 —— 是**为隔离付费，不是升级**。依赖 2.3.1 MCP Server 落地。

- [ ] **3.10 MCP Server 实时工具桥 + 网关级 skill script 远程执行（D11 安全前置）**
  必须同时满足：① 只在沙箱工作目录内执行；② API Key 鉴权；③ 单 workflow 作用域绑定。三者缺一不可。

---

## Phase 4：企业网关 + 拆包评估（远期）

- [ ] **4.1 实现 `matea gateway serve` 子命令**
  集中处理：幂等 / 同 issue 锁 / 多 backend 路由 / 审计门禁。

- [ ] **4.2 单用户默认 `matea serve`；企业用 `matea gateway serve`**

- [ ] **4.3 多 VPS 调度扩展：`hubapi.Provider` 按 task/repo/channel 选择 Hub 实例**
  首版手动配置，需要中央调度时再实现。

- [ ] **4.4 拆包评估：core / adapter / gateway 独立仓库/模块**
  视后端数量和社区需求决定。

---

## 继续保留的「按需 / 可选」项

> 来自 v0.11.4 清单，未纳入主线的可选增强，继续延后或按需启动。

| 类别 | 项 | 说明 |
|---|---|---|
| WebUI | 高亮未推送分支 | 可选，当前未推送给提示 |
| 运维 | bootstrap 启动日志打印「Logging to file: …」 | 可选提示 |
| 沙箱 | `cat` 行号范围、`find` glob、审计日志内容摘要 | 按需 |
| 沙箱 | 拦截 agent 侧 `git commit` / `git push` | 可选安全增强 |
| LLM | tiktoken 精确计数 | 可选，有估算即可 |
| LLM | 超长 Session 语义摘要 | 可选 |
| LLM | per-task 成本预算上限 | 可选 |
| 继续延后 | 文件级 analyze 落地 checklist | soft gate 文案已归档 |
| 继续延后 | API 中间件链 | 有运维痛点再立项 |
| 继续延后 | `gitea.Client` Transport 显式复用 | DefaultTransport 已够用 |

---

## 明确不做

| 项 | 说明 |
|---|---|
| GitHub / GitLab / Gitee 多平台 Host SPI | Gitea-first |
| Issue 级任意 PR base（label/body） | 边缘场景 |
| 自研飞书/企微/Slack SDK | 由 Hub 或用户自配 bridge 解决 |
| 把 Matea 降级为纯库 | 默认仍是可独立运行产品 |
| 强制用户先装 Hub 才能使用 | builtin 默认可用 |
| 同 Agent 忙碌时硬拒绝入队 | 串行必须可排队 |
| 自动双向同步 Matea Prompt 与 Hub Skill | Phase 2 手动 export/import，不做自动写 |
| 接管远程 harness 自身的 skill/plugin 机制 | OpenCode plugin、Claude Code skills 各管各的；Matea 只保证自己的 skill 能被任何 harness 用到（D11） |
| 用 ToolBox 替换成品 harness 自带工具 | 只能 append 不能 replace；重名会导致模型在两套同功能工具间摇摆（D11） |
| 给远程 harness 塞一套沙箱文件工具 | 负收益：它自带且直读本地盘更快，经 ToolBox 多一个 round-trip（D11） |
| 引入外部 harness 的 SDK（Pi/Codex/Claude/OpenCode） | 全为 TS/Python，无法嵌进 Go；统一 out-of-process spawn/connect（D12） |

---

## 现行文档（非归档）

| 文档 | 用途 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 现行架构 |
| [AGENTS.md](AGENTS.md) | Agent 配置与 role 说明 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 部署 |
| [CONFIG-AUTOMATION.md](CONFIG-AUTOMATION.md) | **配置自动化细化方案（Phase 2.5）** |
| [20260805-Phase2-plan.md](20260805-Phase2-plan.md) | Phase 2 实施方案（D1–D12 已拍板，方案冻结） |
| [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md) | 产品定位与演进规划 |
| [server-runtime-design-v4.md](server-runtime-design-v4.md) | OpenCode / CodingBackend（待按 HubBackend 刷新） |
| [todo-20260714-LLMProvider-可选增强.md](todo-20260714-LLMProvider-可选增强.md) | LLM 可选增强 |
| [archived/20260814-TASKS.md](archived/20260814-TASKS.md) | **Phase 1 + Phase 2 已完成部分归档** |
| [archived/20260804-Phase1.5-plan.md](archived/20260804-Phase1.5-plan.md) | Phase 1.5 计划（已收官归档） |
| [archived/20260805-Phase2-evolution-direction.md](archived/20260805-Phase2-evolution-direction.md) | Phase 2 演进方向讨论（结论已并入 Phase2-plan） |
| [archived/20260808-IM-channel-integration-analysis.md](archived/20260808-IM-channel-integration-analysis.md) | IM 渠道接入分析（结论已并入 D5/D6） |
| [archived/20260803-TASKS.md](archived/20260803-TASKS.md) | v0.11.4 任务清单归档 |
| [archived/](archived/) | 历史设计、签核、E2E |
