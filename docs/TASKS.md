# 任务清单

> 更新：2026-08-03（Hub 后端演进规划）  
> 产品边界：**Gitea 优先** · 内置 Agent 默认可用 · 可插拔 Hub 后端（OpenCode / Hermes / OpenClaw / 自定义） · 不自研 IM SDK  
> 决策：  
> - [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md)  
> - 已归档 v0.11.4 任务清单 → [archived/20260803-TASKS.md](archived/20260803-TASKS.md)  
> 已归档交付记录：  
> - P0–P2 核心演进 → [archived/20260716-TASKS.md](archived/20260716-TASKS.md)  
> - P3 开源 + 开源后加固 → [archived/20260723-TASKS.md](archived/20260723-TASKS.md)  
> - 写路径 / Agent 摩擦 / Bootstrap（Issue #4）→ [archived/20260724-TASKS.md](archived/20260724-TASKS.md)  
> 架构评估（点时）→ [archived/20260722-architecture-evaluation.md](archived/20260722-architecture-evaluation.md)

---

## 演进主线

```text
P0–P2 → P3 → 写路径/摩擦/Bootstrap（已归档）→ PR 续作注入 review / 逻辑 Issue 归一 / Agent 并发 / 可观测性（已交付）
        │
        ├─► Phase 1：HubBackend 抽象 + Gitea 触发入口整理 + 体验简化
        ├─► Phase 2：Hub 后端接入（analyze/review → Hermes/OpenCode → code → MCP/deliver）
        ├─► Phase 3：多触发形态（REST/CLI）+ 策略配置化
        └─► Phase 4：可选 matea gateway serve + 拆包评估
```

---

## Phase 1：稳住产品 + 关键抽象（1–2 月）

目标：不改动业务行为，为 Hub 后端演进打好接口；默认体验仍是「下载二进制 → 3 步可用 → Gitea @ 三 Agent」。

### 1.1 Agent 配置简化：固定 role，默认三 Agent 模板

- [ ] 1.1.1 Agent 模型兼容 `role` 字段，不做多 capabilities  
  DB 不变，UI 用 `capabilities` 仅作别名显示；内部按 `role` 映射 task_type。

- [ ] 1.1.2 UI 创建 Agent 时提供默认模板：`matea` / `matea-coder` / `matea-review`  
  每个模板预设 role、system_prompt、默认 backend。

- [ ] 1.1.3 Agent 名自动映射 Gitea username  
  `matea` → `@matea`，`matea-coder` → `@matea-coder`；可覆盖。

- [ ] 1.1.4 合并 Assign 与 @mention 触发语义  
  统一视为「拉 Agent 进入会话」；根据 `role` 决定 task_type。

### 1.2 抽象：HubBackend 接口

- [ ] 1.2.1 在 `internal/agents` 定义 `HubBackend` 接口  
  `Name() / Execute(ctx, TaskContext) / Capabilities() / HealthCheck()`。

- [ ] 1.2.2 定义 `TaskContext` / `BackendResult` / `GiteaAction` / `DeliverRequest` 类型  
  覆盖全 task_type；预留 `MemoryKeys`、`Channel`、`ThreadID`。

- [ ] 1.2.3 把现有 `internal/agent` 的 loop 封装为 `builtin` backend  
  不废弃 `internal/llm` 和内置 Agent Loop；`RunnerFactory` 通过 `backend` 名选择。

- [ ] 1.2.4 在四个 Runner 中预留 `if strings.HasPrefix(agent.Backend, "hub-")` 分支  
  Phase 1 只走 builtin；分支保持空实现或返回明确错误。

- [ ] 1.2.5 将现有 `CodingBackend`（OpenCode）改造为 `hub-opencode` 可选实现  
  与 `HubBackend` 接口对齐；保持现有配置兼容或提供迁移说明。

### 1.3 触发入口整理：`internal/ingress/gitea`

- [ ] 1.3.1 将 `internal/webhook` 事件解析逻辑迁到 `internal/ingress/gitea`  
  统一输出 `Intent` 结构。

- [ ] 1.3.2 `Intent` 结构预留 `Source` / `Channel` / `ThreadID` 字段  
  为 MCP/API/CLI 触发留扩展。

- [ ] 1.3.3 不新增其他触发器实现  
  Phase 1 只聚焦 Gitea webhook；接口先设计好。

### 1.4 LLM 配置边界 + UI 动态表单

- [ ] 1.4.1 明确 `llm.providers` 仅用于 `builtin` backend  
  Hub backend 的 LLM 配置由 Hub 自己管理。

- [ ] 1.4.2 Agent 编辑页按 `backend` 动态显示字段  
  - `builtin`：Provider / Model / Temperature / System Prompt / Loop Config  
  - `hub-opencode`：URL / API Key / Workspace Mode  
  - `hub-hermes`：URL / Skill / API Key / Memory Keys（高级）

- [ ] 1.4.3 系统配置页提示「LLM Providers 仅用于 builtin Agent」

### 1.5 工作流与体验

- [ ] 1.5.1 保留 `free/standard/strict` 预设，不开放策略单元  
  但将 gate 评估拆成独立函数，便于 Phase 3 配置化。

- [ ] 1.5.2 隐藏 WorkflowContext stage 的用户可见面  
  用户只感知「Agent 正在处理 / 已回复 / 已创建 PR」。

- [ ] 1.5.3 保留 `EnsureGiteaAccount`，默认开启，可配置关闭  
  `gitea.auto_provision: false` 时仅校验/提示手动创建。

### 1.6 文档与引导

- [ ] 1.6.1 更新 README / 快速开始：默认体验仍是「下载二进制 → 配 Gitea/LLM → Gitea @matea」
- [ ] 1.6.2 新增「接入 Hub 后端」进阶章节，不放在快速开始里
- [ ] 1.6.3 更新 AGENTS.md 产品叙事：默认 builtin，可选 hub-*

---

## Phase 2：Hub 后端接入（2–3 月）

目标：让 Matea 可以把 Agent 思考/执行外包给 Hermes / OpenCode 等 Hub，并实现 IM 触发/回传。

### 2.1 hub-hermes 实现

- [ ] 2.1.1 实现 `internal/agents/backends/hermes`  
  `HubBackend.Execute` 按 task_type 分支；HTTP/API 调用 Hermes。

- [ ] 2.1.2 `analyze_issue` → Hermes  
  验证 `TaskContext` 打包、`MemoryKeys` 传递、评论写回。

- [ ] 2.1.3 `review_pr` → Hermes  
  验证 diff 传递、审查结论、记忆沉淀。

- [ ] 2.1.4 `reply_comment` → Hermes  
  验证多轮 session、历史评论注入。

- [ ] 2.1.5 `solve_issue` / `fix_bug` → Hermes + Matea MCP tools  
  Hermes 通过 MCP 操作 Matea 沙箱文件；Matea 执行 git/PR。

- [ ] 2.1.6 验证同一 repo 上 analyze → review → code 的记忆共享  
  这是接入 Hermes 的核心价值。

### 2.2 hub-opencode 改造

- [ ] 2.2.1 将现有 `CodingBackend` 对齐为 `hub-opencode`  
  覆盖 analyze/review/code/reply 全 role，而不只是 coder。

- [ ] 2.2.2 验证 OpenCode 作为全 role backend 的接口通用性  
  与 Hermes 并行跑，确保 `HubBackend` 抽象足够通用。

- [ ] 2.2.3 OpenCode 无 IM 渠道时，配置 `deliver.webhook_url` 回传  
  文档说明用户自建 bridge 或对接企业微信/钉钉机器人。

### 2.3 MCP Server + Deliver Webhook

- [ ] 2.3.1 实现 `internal/ingress/mcp`：Matea 作为 MCP Server  
  暴露 `matea_create_issue` / `matea_assign_agent` / `matea_get_issue_status` / `matea_comment_on_issue` / `matea_list_open_issues` / `matea_reset_session`。

- [ ] 2.3.2 MCP 入站鉴权：API Key 或 mTLS  
  默认仅监听 localhost，公网配合反向代理。

- [ ] 2.3.3 实现 `deliver` 模块：标准化事件回传  
  `event/channel/thread_id/repo/issue_id/pr_id/action/content`。

- [ ] 2.3.4 配置 `deliver.webhook_url`：可指向 Hub 接收端或自建 IM bridge  
  Hermes 自带渠道时可选不配。

- [ ] 2.3.5 飞书/企微/钉钉等渠道**不自研 SDK**，通过 Hub 或用户自配 bridge 解决  
  文档给出推荐拓扑。

### 2.4 验证场景

- [ ] 2.4.1 飞书用户说「帮我看看 issue #12」 → Hermes 调用 `matea_get_issue_status`
- [ ] 2.4.2 飞书用户说「让 AI 改」 → Hermes 调用 `matea_assign_agent` → Matea 执行
- [ ] 2.4.3 PR 合并后，Matea 推送 deliver 事件 → Hermes 回传飞书
- [ ] 2.4.4 Gitea Assign `@matea-coder`（backend=hub-opencode）→ OpenCode 执行 → 结果写回 Gitea

---

## Phase 3：多触发形态与策略配置化（3–6 月）

- [ ] 3.1 实现 `internal/ingress/api`：REST API 直接提交任务  
  `POST /api/v1/tasks` 标准化入口。

- [ ] 3.2 CLI 触发：`matea run --repo=... --issue=... --agent=...`
- [ ] 3.3 把 workflow gate 拆为独立策略单元  
  `require-analysis-before-code`、`no-self-review` 等可配置。

- [ ] 3.4 策略 Skill 化：高级用户可写 YAML/JSON 策略  
  与 Agent 解耦。

- [ ] 3.5 Cron 扫描：定时处理 backlog issue/PR  
  可选触发方式。

- [ ] 3.6 评估是否拆包：`matea-core` / `matea` / `hub-hermes` / `hub-opencode`  
  先在一个仓库内抽象，后端数量增多后再拆。

---

## Phase 4：企业网关 + 拆包评估（远期）

- [ ] 4.1 实现 `matea gateway serve` 子命令  
  集中处理：幂等 / 同 issue 锁 / 多 backend 路由 / 审计门禁。

- [ ] 4.2 单用户默认 `matea serve`；企业用 `matea gateway serve`
- [ ] 4.3 多 VPS 调度扩展：`hubapi.Provider` 按 task/repo/channel 选择 Hub 实例  
  首版手动配置，需要中央调度时再实现。

- [ ] 4.4 拆包评估：core / adapter / gateway 独立仓库/模块  
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

---

## 现行文档（非归档）

| 文档 | 用途 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 现行架构 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 部署 |
| [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md) | 产品定位与演进规划 |
| [server-runtime-design-v4.md](server-runtime-design-v4.md) | OpenCode / CodingBackend（待按 HubBackend 刷新） |
| [todo-20260714-LLMProvider-可选增强.md](todo-20260714-LLMProvider-可选增强.md) | LLM 可选增强 |
| [archived/20260803-TASKS.md](archived/20260803-TASKS.md) | v0.11.4 任务清单归档 |
| [archived/20260724-TASKS.md](archived/20260724-TASKS.md) | 写路径 / 摩擦 / Bootstrap |
| [archived/20260723-TASKS.md](archived/20260723-TASKS.md) | P3 + 开源后加固 |
| [archived/](archived/) | 历史设计、签核、E2E |
