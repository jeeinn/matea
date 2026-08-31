# 任务清单

> 更新：2026-08-21（Phase 2.5 / 2.6 已落地；Phase 3/4 经复盘砍掉远期与不相关项，仅保留贴近现状的任务）  
> 产品边界：**Gitea 优先** · 内置 Agent 默认可用 · **可插拔 harness 执行内核**（builtin / OpenCode / Hermes） · Matea 是 Gitea 唯一写方 · 不自研 IM SDK · 不引入外部 harness SDK  
> 核心决策：
> - [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md)
> - Phase 2 实施方案（已执行完毕，归档）→ [archived/20260805-Phase2-plan.md](archived/20260805-Phase2-plan.md)
> - git_sync 三阶段方案（v3.1，已落地，归档）→ [archived/20260815-git-sync-3phase-plan.md](archived/20260815-git-sync-3phase-plan.md)
> - 配置自动化细化方案（已落地，归档）→ [archived/20260814-CONFIG-AUTOMATION.md](archived/20260814-CONFIG-AUTOMATION.md)
> 已归档交付记录：
> - **Phase 2.5 配置自动化 + Phase 2.6 git_sync 落地** → [archived/20260820-TASKS.md](archived/20260820-TASKS.md)
> - **Phase 1 + Phase 2 已完成部分** → [archived/20260814-TASKS.md](archived/20260814-TASKS.md)
> - v0.11.4 任务清单 → [archived/20260803-TASKS.md](archived/20260803-TASKS.md)
> - P0–P2 核心演进 → [archived/20260716-TASKS.md](archived/20260716-TASKS.md)

---

## 演进主线

```text
P0–P2 → 写路径/摩擦/Bootstrap（已归档）→ PR 续作注入 review / 逻辑 Issue 归一 / Agent 并发 / 可观测性（已交付）
         │
         ├─► Phase 1：HubBackend 抽象 + Gitea 触发入口整理 + 体验简化 ✅（已归档）
         ├─► Phase 2：大脑可插拔地基 → Hub 后端接入（OpenCode / Hermes）→ deliver 出站 ✅（已归档）
         ├─► Phase 2.5：配置自动化与首次用户体验 ✅（已归档）
         ├─► Phase 2.6：git_sync 落地（v3.1）✅（已归档）
         └─► 近期收尾 + 可选增量（本文件下方，仅保留贴近现状的任务）
```

> 复盘结论（2026-08-21）：Phase 3（多触发形态/策略配置化）与 Phase 4（企业网关/拆包）主体属**规模化与生态扩展**，在当前单实例、Gitea-first 场景下非阻塞。已将其中的远期/不相关项砍掉或并入「明确不做」，仅保留与现有架构（builtin + hub-* + git_sync + deliver + workflow 门禁）最贴近的任务。

---

## 一、Phase 2 收尾（贴近现状，优先）

### 2.4 验证场景（mock Hub 验收，不依赖真实飞书）

- [ ] **2.4.4 Gitea Assign `@matea-coder`（backend=hub-opencode）→ OpenCode 执行 → 结果写回 Gitea**
  需在真实 OpenCode 环境下跑通完整写任务链路（workspace 制备 → OpenCode 执行 → patch/summary 返回 → Matea finalize → PR 创建）。当前代码路径已具备，缺端到端真实环境验证。
  📌 这是与现状最贴近的待办：hub-opencode / hub-hermes 已落地但缺真实 Hub 的写任务 E2E 验收。

### MCP Server 相关项（原 2.3.1 / 2.3.2 / 2.3.5 / 2.3.6）

> 原 `internal/ingress/mcp`（Matea 作为 MCP Server）、MCP 入站鉴权、SystemConfig 的 MCP Tab、渠道拓扑文档，均因 ROI 低（Server 与现有 Client 反向，从零实现；2.4 验收不依赖）**已延期**。其下游 Phase 3 的 3.9 / 3.10 一并取消。详见「明确不做」。

---

## 二、近期可选增量（轻量、与现有架构对齐，按需启动）

> 以下项不进主线，仅在有真实需求时启动；均复用现有 `ingress`→`Intent`、`dispatcher`、`workflow` 门禁、`api` 框架，不引入新抽象。

- [ ] **3.1 实现 `POST /api/v1/tasks`：REST 直接提交任务**
  将 webhook 已归一化的 `Intent` 暴露一个程序化入口，便于自动化 / 测试。复用 `internal/api` 现有鉴权与 `dispatcher` 入队；不新增触发模型。

- [ ] **3.3 把 workflow gate 拆为可配置策略单元**
  `require-analysis-before-code`、`no-self-review` 等当前写死在 `internal/workflow`，改为可配置开关（仍保持默认值不变）。

- [ ] **3.4 策略 Skill 化（YAML/JSON）**
  高级用户可用 YAML/JSON 描述策略，与 Agent 解耦。依赖 3.3。

- [ ] **3.5 Cron 扫描：定时处理 backlog issue/PR**
  可选触发方式，复用 `internal/gitea` 客户端轮询未处理 Issue/PR 并入队。

- [ ] **3.6 中英双语界面（i18n）**
  抽出 PR/评论/状态卡/L3 通知等用户可见文案为语义化键，按任务/Agent/仓库/全局四级 locale 渲染。详见 [plan-20260831-internationalization.md](plan-20260831-internationalization.md)。

---

## 明确不做

| 项 | 原属 | 说明 |
|---|---|---|
| MCP Server（2.3.1 / 2.3.2 / 2.3.5 / 2.3.6） | Phase 2 | 与现有 MCP Client 反向、从零实现、ROI 低；2.4 验收不依赖 |
| CLI `matea run`（原 3.2） | Phase 3 | 与「Matea 是网关 / 可独立运行产品」定位冲突，不自降为 CLI 工具 |
| 给 harness 暴露 Gitea 写工具 `gitea_create_comment` / `gitea_create_pr`（原 3.8） | Phase 3 | 与「Matea 是唯一 Gitea 写方」核心边界冲突；乱序/重复/不可回滚风险未解 |
| `workspace_transport=mcp`（原 3.9） | Phase 3 | 依赖已取消的 MCP Server；且是「为隔离付费」的性能税，非升级 |
| MCP Server 实时工具桥 + 网关级 skill 远程执行（原 3.10） | Phase 3 | 依赖已取消的 MCP Server |
| 新增 harness Pi / Codex / Claude Code（原 3.7） | Phase 3 | 机制 trivial，但取决于真实用户需求，不主动做 |
| 拆包 `matea-core` / `matea` / `hub-hermes` / `hub-opencode`（原 3.6） | Phase 3 | 过早抽象，后端数量增多后再评估 |
| `matea gateway serve` 企业网关（原 4.1 / 4.2 / 4.3 / 4.4） | Phase 4 | 单实例已靠 `ingress` 去重 + `dispatcher` 并发控覆盖；多实例集中调度需求出现再立项 |

> 延续的产品边界（不变）：GitHub / GitLab / Gitee 多平台 Host（Gitea-first）、Issue 级任意 PR base、自研 IM SDK、把 Matea 降级为纯库、强制先装 Hub、同 Agent 硬拒入队、自动双向同步 Prompt 与 Hub Skill、接管远程 harness 自身 skill/plugin、用 ToolBox 替换成品 harness 自带工具、给远程 harness 塞沙箱文件工具、引入外部 harness SDK。

---

## 继续保留的「按需 / 可选」项

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

## 现行文档（非归档）

| 文档 | 用途 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 现行架构 |
| [AGENTS.md](AGENTS.md) | Agent 配置与 role 说明 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 部署 |
| [HUB-BACKENDS.md](HUB-BACKENDS.md) | **Hub 后端权威文档**（git_sync 信任模型 / 接入契约 / SLO） |
| [remote-hub-deployment-flow.md](remote-hub-deployment-flow.md) | 远端 Hub 部署流程（含实现状态横幅） |
| [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md) | 产品定位与演进规划 |
| [todo-20260714-LLMProvider-可选增强.md](todo-20260714-LLMProvider-可选增强.md) | LLM 可选增强 |
| [plan-20260831-internationalization.md](plan-20260831-internationalization.md) | 中英双语 i18n 方案（待实现） |
| [archived/](archived/) | 历史设计、计划、评审、签核、E2E（索引见 [archived/README.md](archived/README.md)） |
