# 任务清单

> 更新：2026-08-06（Phase 1 全部收官；**Phase 2 方案 D1–D12 全部拍板、方案冻结**，待开分支实施）  
> 产品边界：**Gitea 优先** · 内置 Agent 默认可用 · **可插拔 harness 执行内核**（builtin / OpenCode / Hermes / Phase 3 的 Pi·Codex·Claude） · Matea 是 Gitea 唯一写方 · 不自研 IM SDK · 不引入外部 harness SDK  
> 决策：  
> - [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md)  
> - 已归档 v0.11.4 任务清单 → [archived/20260803-TASKS.md](archived/20260803-TASKS.md)  
> - Phase 2 整体方案（D1–D12：Hermes Runs API 核实 + D10 Harness 抽象 + D11 ToolBox 分层 + D12 out-of-process）→ [20260805-Phase2-plan.md](20260805-Phase2-plan.md)  
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
        ├─► Phase 2：大脑可插拔地基（Harness+ToolBox）→ Hub 后端接入（OpenCode 全 role / Hermes）→ deliver 出站
        ├─► Phase 3：多触发形态（REST/CLI）+ 策略配置化
        └─► Phase 4：可选 matea gateway serve + 拆包评估
```

---

## Phase 1：稳住产品 + 关键抽象（1–2 月）

目标：不改动业务行为，为 Hub 后端演进打好接口；默认体验仍是「下载二进制 → 3 步可用 → Gitea @ 三 Agent」。

### 1.1 Agent 配置简化：role 闭集 + 三 Agent 默认模板

> 边界：**`role` 为闭集** `analyze|coder|review`（创建任何 Agent 三选一）；**Agent 实例数不限**——同一 role 可创建多个实例按仓库/技术栈特化（如 `matea-coder-go` / `matea-coder-fe`）；三个模板仅是开箱默认值，不是数量上限。

- [x] 1.1.1 明确 `role` 为 Agent 职责的唯一真相（现状已满足，仅补文档）  
  在 AGENTS.md / ARCHITECTURE.md 显式声明：`role` 为闭集 `analyze|coder|review`，同一 role 允许多 Agent 实例（按仓库/技术栈特化）；**不引入 capabilities 概念**（代码库中从不存在，引入别名是净负债）。无代码改动。
  ✅ 已完成（提交 91251ef）：已在 ARCHITECTURE.md 和 README.md 中补充完整 role 定义

- [x] 1.1.2 改造现有 `applyRoleWizard` 向导为默认模板：`matea-analyst` / `matea-coder` / `matea-review`  
  现状：向导已预设 name + system_prompt + user_template（`web/src/views/Agents.vue`）。差距仅三点：命名 `code-*` → `matea-*`、补默认 backend（`builtin`）、配合 1.1.3 填 gitea_username。命名迁移需同步 README / AGENTS.md / E2E 示例与存量用户引导。  
  默认 backend 标识符统一为 `builtin`（决策 #13，与 §九 `hub-` 前缀分流规则一致）：本项**直接写 `builtin`**，无需回退到 `internal`；源码侧 `internal`→`builtin` 的改写与读取期归一化由 1.2.6 负责，二者无顺序依赖（1.2.6 落库后 `builtin`/`internal` 均被接受）。  
  注：分析 Agent 用 `matea-analyst` 而非 `matea`，避免产品名的「总入口」误解。
  ✅ 已完成（提交 40c7454, 5a49966）：已改造向导并更新文档命名示例

- [x] 1.1.3 Agent 名自动映射 Gitea username（含账号保护）  
  `matea-analyst` → `@matea-analyst`，`matea-coder` → `@matea-coder`；可覆盖。  
  **安全前置**：`EnsureGiteaAccount` 的 else 分支会对已存在账号静默重置密码并签发 token（`internal/agents/manager.go`），自动映射必须把触发面从「手工输入」扩大到「自动」之前堵住：
  - 托管标记：仅对「由 Matea 创建」的账号允许重置密码 / 签发 token
  - 冲突即报错：命中已存在且非 Matea 托管的账号时创建失败并提示改名，绝不静默接管
  - 确需接管既有账号时走单独的显式确认流程
  - `gitea.auto_provision: false` 时只做名称建议，不触碰 Gitea  
  验收：同名真人账号存在时创建 Agent → 报错且**不调用** `AdminUpdateUserPassword`。
  ✅ 已完成（提交 f00c998）：已实现账号保护逻辑，测试全部通过

- [x] 1.1.4 合并 Assign 与 @mention 触发语义（保留 surface 维度，拆三步）  
  用户心智层统一为「拉 Agent 进入会话」，但 task_type 由 `(role, surface, intent)` 三元组解析表决定，**不能只看 role**——否则 @matea-review 在 Issue 评论会被推成 `review_pr`（无关联 PR 时被 L1 门禁拒绝，有关联 PR 时在 Issue 里错跑完整审查），@analyze 的轻量回话会放大成完整分析。
  - [x] 1.1.4a 抽取 `(role, surface, intent)` 解析表，行为与现状**完全等价**（纯重构）  
    验收：表驱动单测覆盖 3 role × 2 surface × 全 intent 组合，逐条断言与重构前等价。
    ✅ 已完成（提交 fa5d498）：已创建解析表和完整测试覆盖
  - [x] 1.1.4b 统一 Intent 命名与用户可见文案为「拉 Agent 进入会话」（只改文案与日志，不改路由）
    ✅ 已完成（提交 1d05702）：已统一注释和日志文案
  - [x] 1.1.4c 补齐 Assign 分支能力缺口：`ResolveLogicIssueAndPR`（PRID / `Fixes #N` 解析）与 `/force` 支持；顺手修复斜杠命令裸匹配（`strings.Contains(body, "/dev")` 会误中 `/development`、URL、代码块），改为行首锚定 + 剥离代码块 + 词边界
    ✅ 已完成（提交 d19a80f）：已补齐能力缺口并修复斜杠命令匹配
  ✅ 任务 1.1.4 完成

### 1.2 抽象：HubBackend 接口

- [x] 1.2.1 在 `internal/agents` 定义 `HubBackend` 接口  
  `Name() / Submit(ctx, TaskContext) → Handle / Poll(ctx, Handle) / Cancel(ctx, Handle) / Capabilities() / HealthCheck()`。  
  **异步句柄形态**（决策 #12）：`Handle`（含 RemoteID + IdempotencyKey）随任务持久化到 SQLite，支持重启后恢复长任务、防重复提交。  
  ⚠️ **接口异步 ≠ 重启恢复**：避免仅做 Runner 内串行 `Poll` 至完成的「伪异步」——那仍是同步阻塞一个 Executor worker，进程重启即丢任务、Hub 侧留孤儿会话。真正的重启恢复须满足：(a) `Submit` 返回 `Handle` 后**立即落库**到任务队列；(b) `Executor` 启动时扫描「非终态 Handle」重新拾取轮询/重放（可复用现有崩溃重放能力）。本项为验收强制点。
  ✅ 已完成（提交 ede10da）：已定义 HubBackend 接口与 HubBackendRegistry（未知 backend 显式报错）

- [x] 1.2.2 定义 `TaskContext` / `BackendResult` / `GiteaAction` / `DeliverRequest` 类型  
  覆盖全 task_type；预留 `MemoryKeys`、`Channel`、`ThreadID`。
  ✅ 已完成（提交 ede10da）：已定义全部交换类型（含 ToolAccessGrant、State、Handle）

- [x] 1.2.3 把现有 `internal/agent` 的 loop 封装为 `builtin` backend  
  不废弃 `internal/llm` 和内置 Agent Loop；`RunnerFactory` 通过 `backend` 名选择。  
  验收：封装后 `tests/integration/` 既有全部用例零修改通过。
  ✅ 已完成（提交 fc0552b）：BuiltinHubBackend 实现 HubBackend（写任务复用 AgentLoop，其余单次补全）；integration 零修改通过

- [x] 1.2.4 在四个 Runner 中预留 `hub-*` backend 分流分支  
  Phase 1 中 `hub-hermes` / `hub-openclaw` / `hub-api` 返回明确错误（尚无实现）；**`hub-opencode` 例外，保持可用**（见 1.2.5）。未知 backend 必须报错，不得静默回落 builtin。
  ✅ 已完成（提交 75d481c）：ResolveHubBackend + 注册表（builtin 常驻、hub-opencode 单例）；四个 Runner 文件全覆盖（五个 Runner 入口，Dev/Bugfix 共用 runWriteTask）；保留名/未知名显式报错

- [x] 1.2.5 将现有 `CodingBackend`（OpenCode）改造为 `hub-opencode` 可选实现  
  与 `HubBackend` 接口对齐；**Phase 1 保持 OpenCode 可用**，使接口抽象从两个真实实现（builtin + opencode）反推；保持现有配置兼容或提供迁移说明。
  ✅ 已完成（提交 f783e43）：OpenCodeHTTPBackend 同时实现 HubBackend（同步 Submit + 终态缓存 Poll + Abort 映射 Cancel）；既有 CodingBackend 路径零改动

- [x] 1.2.6 backend 标识符迁移：`internal` → `builtin`、`opencode_http` → `hub-opencode`（决策 #13，源码侧统一，非仅 DB/YAML）  
  注意：`BackendTypeBuiltin = "builtin"` 常量**值已是 `builtin`**（schema.go:275），双轨根源是运行时默认名 / `InternalCodingBackend.Name()` / store 兜底仍写字面量 `"internal"`。本项把源码、配置、测试、DB、UI 全部收敛到 `builtin`/`hub-opencode`，使 1.1.2 直接写 `builtin` 无顺序依赖。

  **(a) 归一化函数（先落地，消除顺序依赖）**：新增 `normalizeBackend(name)`：`internal`→`builtin`、`opencode_http`→`hub-opencode`、其余原样；在 config 加载与 agent 加载处调用。落库后 `builtin`/`internal` 均被接受。
  ✅ 已完成（提交 64a8b42）：`config.NormalizeBackend` + 加载期/解析期双重归一化，旧标识符全部兼容路由

  **(b) 源码标识符改写（精确位置，改完 `go build ./...` + 全量测试 PASS）**：
  - `internal/agents/coding_backend.go`：struct `InternalCodingBackend`→`BuiltinCodingBackend`；`NewInternalCodingBackend`→`NewBuiltinCodingBackend`；`Name()` 返回 `"builtin"`(L100)；局部 `name = "internal"`(L237)→`"builtin"`、`if name == "internal"`(L240)→`"builtin"`；注释 L88/99/222/224/239/264 中的后端值 "internal" 改 "builtin"。
  - `internal/agents/runners.go`：`factory.internalBackend` 字段(L83)→`factory.builtinBackend`；赋值处 `NewInternalCodingBackend`(L140)→`NewBuiltinCodingBackend`。
  - `internal/store/agent.go`：`a.Backend = "internal"`(L76/104/183)→`"builtin"`；注释 L38。
  - `internal/agents/manager.go`：注释 L54。
  - `internal/config/config.go`：默认 `backends.Default = "internal"`(L249)→`"builtin"`；`backends.Backends["internal"]`(L254-259)→`["builtin"]`。
  - `internal/config/schema.go`：注释 L246/247；`Default: "internal"`(L282)→`"builtin"`；`"internal": {Type: BackendTypeBuiltin}`(L284)→`"builtin"`；常量 `BackendTypeOpenCodeHTTP = "opencode_http"`(L276) **确定改名** `BackendTypeHubOpenCode = "hub-opencode"`（值同步改），同步改所有引用（coding_backend.go:252、config_test.go:123、opencode_http_test.go:114/153/290/298/332/365/387/406）；字段 `AllowFallbackInternal bool yaml:"allow_fallback_internal"`(L258) **改名** `AllowFallbackBuiltin bool yaml:"allow_fallback_builtin"`（当前无真实用户，不做向后兼容）。
  - `internal/config/manager_display.go`：`"name": "internal"`(L58)→`"builtin"`；`name == "internal"`(L62)→`"builtin"`。
  - `internal/agents/coding_backend.go`：除 L100/237/240 的 backend 值改写外，字段读取 `b.cfg.AllowFallbackInternal`(L268)→`b.cfg.AllowFallbackBuiltin`；注释 L38/265 中 `allow_fallback_internal`→`allow_fallback_builtin`。
  - `internal/agents/runner_write.go`：日志 `allow_fallback_internal=true → switching to internal`(L75)→`allow_fallback_builtin=true → switching to builtin`。
  - **排除**：`internal/agents/commit_message.go:137 parts[0]=="internal"` 是 conventional-commit 类型判断，**严禁改动**。

  **(c) 测试同步改写（必须零回退 PASS）**：
  - `internal/config/config_test.go`：L73/112/113/114/132/141/148/149 的 `"internal"`→`"builtin"`；L123 `BackendTypeOpenCodeHTTP`→`BackendTypeHubOpenCode`。
  - `internal/store/backend_test.go`：L27/77/83/105 `"internal"`→`"builtin"`。
  - `internal/agents/coding_backend_test.go`：`InternalCodingBackend`→`BuiltinCodingBackend`、构造器、mock 注释、各 `Name()` 断言（L18/46-154）。
  - `internal/agents/opencode_http_test.go`：L314/319/323 `"internal"`→`"builtin"`；`BackendTypeOpenCodeHTTP`(L114/153/290/298/332/365/387/406)→`BackendTypeHubOpenCode`；`AllowFallbackInternal: true`(L408)→`AllowFallbackBuiltin: true`；注释 L378 `allow_fallback_internal`→`allow_fallback_builtin`。

  **(d) DB 一次性迁移（幂等）**：`UPDATE agents SET backend='builtin' WHERE backend IN ('internal','')`；`UPDATE agents SET backend='hub-opencode' WHERE backend='opencode_http'`；放入 store 迁移段。同时回填 1.1.3 的托管标记：`managed_by_matea DEFAULT 0` 对升级前已由 Matea 创建的账号为 0，若有存量需补一次性 UPDATE 置 1（当前无真实用户，仅记录）。

  **(e) YAML 示例**：`config.full-example.yaml`：L134/137 `internal`→`builtin`、L139 块名 `internal:`→`builtin:`、L142 `type: opencode_http`→`hub-opencode`、L152 `allow_fallback_internal`→`allow_fallback_builtin`（注释中 "降级到 internal Loop"→"降级到 builtin Loop"）；`config.example.yaml` 同步（若有）。

  **(f) 前端**：`web/src/views/Agents.vue`：注释 L105 "internal 为内置"→"builtin 为内置"；`backendTypeLabel`(L380) `opencode_http`→`hub-opencode`（返回 'OpenCode'）；创建 Agent 默认 backend 若写死 `internal` 改为 `builtin`。

  验收：存量 `backend='internal'` 行经迁移 + 归一化后正确路由；全部现有测试零回退通过；`BackendTypeBuiltin` 值保持 `"builtin"`。
  ✅ 已完成（提交 64a8b42, 3a0f08e）：(a)-(f) 全部落地，全量测试零回退通过；commit_message.go:137 按排除要求未动

- [x] 1.2.7 Mock Hub 测试地基  
  TestEnv 增加 Mock Hub（模拟正常返回 / 超时 / 502 / 鉴权失败 / 异步长任务）；接口定完立刻以其验证可测性，为 Phase 2 测试铺路。
  ✅ 已完成（提交 c13addb）：MockHub 挂载 TestEnv；五场景 + 取消 + 未知任务共 7 个测试通过

### 1.3 触发入口整理：`internal/ingress/gitea`

- [x] 1.3.1 将 `internal/webhook` 事件解析逻辑迁到 `internal/ingress/gitea`  
  统一输出 `Intent` 结构。
  ✅ 已完成（提交 24dd3a1, 861f59c）：整包迁移（git mv 保留历史，消费方统一 giteaingress 别名）；Intent 成为 ingress 唯一输出，HandleEvent/回调全链接线

- [x] 1.3.2 `Intent` 结构预留 `Source` / `Channel` / `ThreadID` 字段  
  为 MCP/API/CLI 触发留扩展。
  ✅ 已完成（提交 bf96d5b）：Source 常量集（gitea + 保留 mcp/api/cli）、Channel/ThreadID omitempty、JSON 契约测试

- [x] 1.3.3 不新增其他触发器实现  
  Phase 1 只聚焦 Gitea webhook；接口先设计好。
  ✅ 已完成（无需代码）：确认 internal/ingress 仅 gitea 一个实现；Phase 2 触发器契约已由 Intent.Source/Channel/ThreadID 承载

### 1.4 LLM 配置边界 + UI 动态表单

- [x] 1.4.1 明确 `llm.providers` 仅用于 `builtin` backend  
  Hub backend 的 LLM 配置由 Hub 自己管理。
  实现：schema.go `LLMConfig.Providers` 注释、builtin_hub_backend.go `resolveProvider` 注释（明确仅 builtin 走此路径）、manager_models.go `getProviderConfig` 注释、config.full-example.yaml `llm.providers` 段注释。纯文档澄清，无逻辑变更；消费点（GetProviderModels / resolveProvider）本就仅 builtin 触达。

- [x] 1.4.2 Agent 编辑页按 `backend` 动态显示字段  
  - 新增 `selectedBackendType`/`isBuiltinBackend`/`isHubOpenCode`/`isHubHermes` 计算属性（按 `_meta.backends` 的 `type`）。
  - `builtin`：Provider / Model / Temperature / Loop Config（均加 `v-if="isBuiltinBackend"`；Loop Config 再叠加 `role==='coder'`）。
  - `hub-opencode`：仅覆盖 `opencode_model` / `opencode_provider` / `opencode_agent`（绑定 `backend_options`，`v-if="isHubOpenCode"`）；连接参数 URL/鉴权/工作区模式由服务端命名后端 `agents.backends.<name>` 统一配置，**不在** Agent 页收集。
    📌 评审修正（20260804-1.4 评审）：原拟定收集 `backend_options.url/api_key/workspace_mode`，经核对全仓无服务端消费者（opencode_http.go 仅消费 `opencode_model/opencode_provider/opencode_agent`），且 `workspace_mode` 前端选项 `container/local` 与服务端唯一接受值 `matea_path` 不符；已改为仅暴露服务端实际消费的覆盖键，并在表单内提示连接参数来源。
  - `hub-hermes`：URL / Skill / API Key / Memory Keys（绑定 `backend_options`，`v-if="isHubHermes"`，置于高级折叠；type 由前端防御性识别，Phase 2 服务端落地）。
  - **决策**：System Prompt / User Template 为 Agent 级人设，**所有后端均显示**（未按任务字面归入 builtin），避免 hub Agent 丢失人设。
  - `backend_options` 接入：create 表单默认 `{}`；edit 用 `agent.backend_options` 初始化；切换 backend 经 `onBackendChange` 清空（键集不同）；`saveAgent` 仅 hub 后端发送 `backend_options`，builtin 省略以避免清空既有值。后端 API（`handlers_agents.go`）已原生支持 `backend_options`。

- [x] 1.4.3 系统配置页提示「LLM Providers 仅用于 builtin Agent」  
  SystemConfig.vue LLM 配置 Tab 顶部 alert 改为明确：Provider 仅对 builtin Agent 生效；hub-* 由 Hub 自管、连接参数（URL/鉴权/工作区模式）在服务器端 `agents.backends.<后端名>` 按命名后端统一设置，Agent 编辑页仅可覆盖提交到 Hub 的模型/Provider。

### 1.5 工作流与体验

- [x] 1.5.1 保留 `free/standard/strict` 预设，不开放策略单元  
  但将 gate 评估拆成独立函数，便于 Phase 3 配置化。  
  ✅ 已核验（无需改码）：`internal/workflow/policy.go` 中 gate 评估已是独立函数（`EvaluateGate` + 各 `eval*`），`free/standard/strict` 三预设（`PresetFree/Standard/Strict` + `GetPreset`）齐备；`allow_skip_analyze` 等仅作前向兼容占位（无消费者），符合「Phase 3 再配置化」的约定。

- [x] 1.5.2 隐藏 WorkflowContext stage 的用户可见面  
  用户只感知「Agent 正在处理 / 已回复 / 已创建 PR」。  
  顺带修复既有缺陷：mention 路径会 `Transition(ctx, role)` 推进 stage，但 `OnTaskComplete("reply_comment"/"solve_comment")` 不回落 stage，导致 stage 滞留（如 `analyzing`）。  
  ✅ 已交付，拆为两项：
  - **1.5.2A 后端 stage 回落（修复滞留）**：`store/workflow.go` 的 `TransitionStage` 仅在「真实阶段变化」时记录 `PreviousStage`（同阶段重入清空）；`internal/workflow/context.go` 的 `OnTaskComplete` 对 `reply_comment`/`solve_comment` 新增 `rollbackTransientStage`，按推荐规则回落。
    📌 **1.5.2A 拍板（用户确认）**：`solve_comment` 回滚语义 = 有 PR 则保持 `developing`（与 `solve_issue` 一致），无 PR 则回落 `PreviousStage`；`reply_comment` 一律回落 `PreviousStage`。
  - **1.5.2B 前端语义化**：`web/src/views/WorkflowDetail.vue` 列表「阶段」→「状态」用 `semanticStatus()`（处理中/已回复/已建 PR/已完成/空闲），原始 stage-flow 与 previous_stage 收进「诊断（高级）」折叠（默认收起）。`npm run build` 通过。
  - 回归测试：`internal/workflow/context_rollback_test.go`（6 例覆盖 1.5.2A）+ 既有 dispatcher/integration 零回退。

- [x] 1.5.3 保留 `EnsureGiteaAccount`，默认开启，可配置关闭  
  `gitea.auto_provision: false` 时仅校验/提示手动创建。  
  ✅ 已交付：`gitea.auto_provision` 开关覆盖 6 处 + 测试：
  - schema：`GiteaConfig.AutoProvision bool yaml:"auto_provision"`
  - 加载默认：`config.go` `LoadWithBootstrap` 用 `*bool` 探测原始 YAML 是否存在该键——**缺失默认 `true`，显式 `false` 被尊重**（规避零值歧义）
  - 热更新链路：`config/keys.go` 白名单 + `parseConfigValue` + `getConfigValueTyped` + `applyConfigEntry` + `getConfigEntry` 五处；`config/manager.go` `getActiveMap` 加键
  - 消费点：`agents/manager.go` 的 `CreateAgent`/`UpdateAgent` 在 `m.cfg != nil && m.cfg.AutoProvision` 时才调用 `EnsureGiteaAccount`，否则跳过并保留既有 token
  - 示例：`config.full-example.yaml` 加注释段（默认开启）
  - 测试：`config_test.go`（默认 true / 显式 false / 键往返）+ `manager_test.go`（`newTestManager` 置 `AutoProvision:true` 保既有用例；新增 `TestCreateAgent_SkipsProvisionWhenDisabled` / `TestUpdateAgent_SkipsProvisionWhenDisabled`）

### 1.6 文档与引导

- [x] 1.6.1 更新 README / 快速开始：默认体验仍是「下载二进制 → 配 Gitea/LLM → Gitea @matea-analyst」
  ✅ 已完成：README.md 更新架构概览（`internal/ingress/gitea`）、核心组件表、快速开始步骤 ④（从模板创建三件套）、验证工作流（Assign + @mention）、`gitea.auto_provision` 默认自动建号说明、配置说明表标注 `llm` 仅 builtin 使用、`agents` 段含命名后端；新增「Agent backend 与 LLM 边界」说明。

- [x] 1.6.2 新增「接入 Hub 后端」进阶章节，不放在快速开始里
  ✅ 已完成：README.md 新增「接入 Hub 后端（可选）」章节，说明命名后端 `agents.backends.<name>` 配置、Agent 编辑页字段变化、builtin vs hub-opencode 运行差异，并提示 `hub-hermes` 等 Phase 2 可用。

- [x] 1.6.3 更新 AGENTS.md 产品叙事：默认 builtin，可选 hub-*
  ✅ 已完成：新建 docs/AGENTS.md，产品叙事从「Agent 是什么 → 默认 builtin → 可选 hub-opencode → role 闭集 → 触发方式 → 账号自动创建」展开，并在 README 文档索引中链接。

---

## Phase 2：Hub 后端接入（2–3 月）

> 方案：[20260805-Phase2-plan.md](20260805-Phase2-plan.md)（**D1–D12 全部拍板，方案已冻结**）
> 关键收束（评审 + Hermes 官方 API 核实 + 代码现状核对）：
> - D2 完成通知 = **Poll**（官方 `/v1/runs` 轮询，复用 1.2.1），**无入站完成端点**
> - D4 MCP Server **降级可选**（最小 4 工具）；⚠️ 与现有 MCP **Client** 方向相反，属**全新实现**
> - D5 deliver **仅出站扇出**（无入站）
> - 2.1.5（原「Hermes 经 MCP 操作 Matea 沙箱」）**删除**——Hermes 自带沙箱，结果经 Poll 回传 patch，由 Matea 落地
> - **D10/D11/D12 为大脑可插拔三支柱**：统一 `Harness` 抽象 · `ToolBox` 分层暴露 · 统一 out-of-process 集成（不引入外部 SDK）
>
> **实施顺序（D9）**：2.0 机制骨架 → 2.2.1（D7 第一刀，最早可验证）→ 2.1（hermes）→ 2.3（deliver）→ 2.4（mock 验收）

### 2.0 机制骨架：大脑可插拔地基（D10 + D11 + D12，先于 2.1/2.2）

- [x] 2.0.1 抽出统一 `Harness` 接口 + `harnessRouter` 注册表（D10）
  收敛现有三套接口（`Runner` 同步 / `CodingBackend` 写入 / `HubBackend` 异步 Submit/Poll）为：`Profile() HarnessProfile` + `RunTurn(ctx, HarnessTurnInput) (*HarnessTurnResult, error)` + `Close()` + `ResetSession(id)`。
  `HarnessTurnInput` = `Task TaskContext` + `Tools ToolContext` + `Model string`；`HarnessTurnResult` = `Reply` + `Action`(`comment`/`create_pr`/`none`) + `PendingApprovals`。
  接口**声明** `controlTransport`/`toolTransport` 元数据（对齐 qm），但只实现 in-process(builtin) + out-of-process(submit-contract)。注册表为单文件 `Map<HarnessID, Harness>`，新增大脑 = 加一行。
  📌 **拒绝**：每轮动态选 harness（Matea 任务有状态，选择粒度=每任务/每 agent）；harness×model 双轴校验矩阵（model 仅 builtin 用，hub-* 自管 LLM）。~1–2 人日（含测试）。
  ✅ 已完成（提交 b4aab51）：Harness/HarnessProfile/HarnessTurnInput/HarnessTurnResult 类型定义；harnessRouter 注册表 (Register/Lookup/GetHarness)；2x2 transport 模型常量

- [x] 2.0.2 现有 `builtin` 改造为 in-process adapter（D10 实证 1）
  内部仍跑现有多 role Agent Loop，仅套上 `Harness` 接口；行为零变化，以既有测试全 PASS 为验收基线。
  ✅ 已完成（提交 b4aab51）：BuiltinHarness 实现 Harness 接口，Profile 声明 in_process + tool_direct

- [x] 2.0.3 定义 `ToolContext`/`ToolBox` 并按**三层策略**暴露（D11）
  - **沙箱类**（现有 10 个：`read_file`/`write_file`/`list_files`/`search_code`/`rg`/`run_command`/`apply_diff`/`tree`/`git_log`/`git_blame`）：builtin 直连 Go 函数；**对远程 harness 默认不暴露**（成品 harness 自带同名工具，且直接操作共享工作区更快）。
  - **Gitea 类**（新增）：**Phase 2 只做读侧** `gitea_read_issue` / `gitea_read_pr_diff`；**写侧不做**（`gitea_create_comment`/`gitea_create_pr` 留 Phase 3），发评论/建 PR 继续由 Matea 在结果返回后统一落地，守「Gitea 唯一写方」。
  - ⚠️ 对成品 harness（OpenCode/Hermes）ToolBox 只能 **append 不能 replace** —— 其自带工具关不掉，重名会导致模型摇摆。此约束须在接口注释中写死。
  ✅ 已完成（提交 b4aab51）：ToolBox 三层暴露策略实现；ToolCatSandbox 仅 builtin；ToolCatGitea 远程读侧 (gitea_read_issue/gitea_read_pr_diff)；ToolImplFn 接口

- [x] 2.0.4 网关级 skill 经 ToolBox 暴露（D11）
  仓库内 skill（workspace 的 `skills/`）**不做集成**——随工作区交付，harness 自读（OpenCode 还会读 `AGENTS.md`）。
  网关级 skill（gatewayDir 的 `skills/`）分两半：**正文（body）** 作为 system prompt 片段注入（任何 harness 通吃）；**script 工具** 经 ToolBox 导出、在 **Matea 侧沙箱内执行**。
  远程 harness 自身的 skill/plugin 机制（OpenCode plugin、Claude Code skills）**不接管**，各管各的。
  ⚠️ 安全前置（Phase 3 落 MCP 时强制）：script 为任意 shell，导出即 RCE 面 → 必须同时满足「沙箱工作目录内执行 + API Key 鉴权 + 单 workflow 作用域绑定」。
  ✅ 已完成（提交 b4aab51）：RegisterGatewaySkills/GetGatewaySkillBody/ListGatewaySkillNames；skillToolToDecl 工具转换；matea_skill_ 前缀避免冲突

- [x] 2.0.5 配置新增 `workspace_transport` 语义位（D11 部署档位）
  取值 `shared_path`（默认，Phase 2 唯一实现）｜ `mcp`（Phase 3）。
  **`hub-opencode` 显式声明仅支持 L0/L1**：`opencode_http.go` 用 `filepath.Abs(req.WorkDir)` 传绝对本地路径给 `?directory=`/`X-Opencode-Directory`，事实要求同机或共享卷 —— 配置校验需拒绝 `mcp`，文档需写明「配 URL ≠ 可异地」。
  L1 共享卷需明确「单 workflow 期间独占该工作区」。
  ✅ 已完成（提交 b4aab51）：BackendConfig.WorkspaceTransport 字段；ApplyBackendDefaults 默认 shared_path；ValidateBackendWorkspaceTransport 验证；BackendTypeHubHermes 常量预留

### 2.1 hub-hermes 实现（基于官方 Runs API：`POST /v1/runs` + `GET /v1/runs/{run_id}` Poll）

- [x] 2.1.1 实现 `internal/agents/backends/hermes`
  `HubBackend.Submit` → `POST /v1/runs`（`{input, session_id?, instructions?, conversation_history?, previous_response_id?}`），返回 `{run_id, status}`；`Poll` → `GET /v1/runs/{run_id}` 取 `{status, output, session_id, usage}`，终态 completed/failed 即结束；Bearer 鉴权（`API_SERVER_KEY`）；`session_id` 用于同 repo 多任务续接。
  **Handle 持久化 + 重启恢复**：满足 1.2.1「落库 + Executor 重启拾取」强制点；IdempotencyKey 去重（1.2 评审挂账 ①）；Poll 重启措辞统一（挂账 ②）。
  📌 全 task_type 经同一 Submit/Poll 路径（含 `solve_issue`/`fix_bug`：Hermes 返回 patch/summary → Matea `finalizeWriteChanges` 落地，不反向操作 Matea 沙箱——见 2.1.5 删除说明）。
  ✅ 已完成（提交 8c5a6f2）：`internal/agents/backends/hermes/hermes.go` 实现 HubBackend 接口（Submit/Poll/Cancel/Capabilities/HealthCheck）；Bearer 鉴权；session_id 按 repo 派生支持跨任务记忆共享；17 个单元测试覆盖正常/失败/鉴权/502/对话历史/diff 场景

- [ ] 2.1.2 `analyze_issue` → Hermes
  验证 `TaskContext` 打包、`MemoryKeys` 传递、评论写回。

- [ ] 2.1.3 `review_pr` → Hermes
  验证 diff 传递、审查结论、记忆沉淀。

- [ ] 2.1.4 `reply_comment` → Hermes
  验证多轮 session（用 `session_id` 续接）、历史评论注入。

- [ ] 2.1.5 验证同一 repo 上 analyze → review → code 的记忆共享（D3）
  新增轻量 `memories` 表（repo/issue 级 KV）+ `MemoryKeys` 读写；analyze 写键、code 读键后行为一致；Hermes 侧用 `session_id` 续接同一会话上下文。
  📌 **2.1.5（原「Hermes 经 MCP 操作 Matea 沙箱」）已删除**：Hermes 自带沙箱（local/Docker/SSH/Singularity/Modal），应在自身环境跑、把结果经 Poll 回传，由 Matea 落地——既符合 Hermes 执行模型，也守住「Matea 是唯一 Gitea 写方」不变量。

### 2.2 hub-opencode 改造（D7 三刀，第一刀最早可验证）

- [ ] 2.2.1 **D7 第一刀（最小可验证）**：`AnalyzeRunner` 后端感知 + analyze 带工作区给 OpenCode
  复用 `prepareAnalyzeWorkspace`（默认分支 shallow clone，`write_workspace.go:322`）+ OpenCode 目录绑定（`?directory=`+`X-Opencode-Directory`）；放宽 `opencodeWriteSubType`→`opencodeSubType(taskType)→(subType,isWrite)`；返回 `Action:"comment"`，不建 PR；`defer wwc.Sandbox.Cleanup()`。复杂度低–中（~0.5–1 人日）。
  📌 依赖 2.0.1/2.0.3：作为 out-of-process adapter 的**实证 2**（证明 remote transport + 带上下文）；工作区经 `workspace_transport=shared_path` 抵达，沙箱类工具不外暴（OpenCode 用自带工具直读该目录）。

- [ ] 2.2.2 **D7 第二刀**：review 带工作区给 OpenCode
  新增 `prepareReviewWorkspace`（clone PR head 到临时 sandbox），复用 OpenCode 目录绑定做代码审查。

- [ ] 2.2.3 **D7 第三刀**：reply 全 role（对话类，单轮，工作区非必需）
  `InteractionRunner` 加后端感知分支，复用 `executeSingleShot` 式单轮。

- [ ] 2.2.4 OpenCode 无 IM 渠道时，配置 `deliver.webhook_url` 回传（出站，由 2.3.3 提供）
  文档说明用户自建 bridge 或对接企业微信/钉钉机器人。

### 2.3 MCP Server + Deliver（MCP 降级可选；deliver 仅出站）

- [ ] 2.3.1 （可选）实现 `internal/ingress/mcp`：Matea 作为 MCP Server
  若实现则**严格最小 4 工具**：`matea_get_issue_status` / `matea_assign_agent` / `matea_list_agents` / `matea_post_comment`。不做 `create_issue`/`list_open_issues`/`reset_session`。
  📌 降级原因：Matea↔Hermes 主链路为 HTTP Runs API，人类渠道由 Hermes gateway `deliver`（feishu/wecom 原生）承担；2.4 验收不依赖 MCP Server。
  ⚠️ **不可复用现有代码**：`internal/agent/tools_mcp.go` 是 MCP **Client**（Matea 消费外部 MCP），此处需要的是 MCP **Server**（Matea 暴露工具），方向相反，属全新实现，工作量按「从零起」计。
  📌 若落地，暴露的工具集须遵循 2.0.3 分层策略（Gitea 读侧 + 网关级 skill script；沙箱类默认不给）。

- [ ] 2.3.2 （可选）MCP 入站鉴权：API Key 或 mTLS
  默认仅监听 localhost，公网配合反向代理。

- [ ] 2.3.3 实现 `deliver` 模块：**仅出站事件扇出**
  `event/channel/thread_id/repo/issue_id/pr_id/action/content` → POST `deliver.webhook_url`。**无入站接收模块**（Hermes Poll / OpenCode 同步均不推完成事件）。

- [ ] 2.3.4 配置 `deliver.webhook_url`：可指向用户自建 IM bridge 或 Hub 接收端
  Hermes 自带渠道时可选不配；OpenCode 无 IM 时必配（2.2.4）。

- [ ] 2.3.5 系统配置页增加 MCP + Deliver 配置块（评审新增，工作量小）
  SystemConfig 新增「MCP Server」Tab（enable + listen + auth）与「Deliver」Tab（webhook_url）。即便 2.3.1 可选，配置块保留以便启用。

- [ ] 2.3.6 飞书/企微/钉钉等渠道**不自研 SDK**，通过 Hub 或用户自配 bridge 解决
  文档给出推荐拓扑。

### 2.4 验证场景（mock Hub 验收，不依赖真实飞书）

- [ ] 2.4.1 飞书用户「帮我看看 issue #12」 → Hermes 经 runs API 查 Gitea 上下文（若 MCP 落地则 `matea_get_issue_status`）→ 回飞书
- [ ] 2.4.2 飞书用户「让 AI 改」 → Hermes `matea_assign_agent`（或 Matea HTTP API）→ Matea 执行 → 写回 Gitea
- [ ] 2.4.3 PR 合并后，Matea deliver 出站事件 → 用户飞书（经 outbound deliver 或 Hermes gateway `deliver` 回传）
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

- [ ] 3.7 新增 harness：Pi / Codex / Claude Code（D12，机制验证后 trivial）  
  接入方式 = **spawn 其 CLI 或连其本地 server（out-of-process）**，**不引入任何外部 SDK**（全为 TS/Python，无法嵌进 Go 进程）。每新增一个 = 实现一个 `Harness` + 注册表加一行。

- [ ] 3.8 Gitea **写侧**工具评估（D11 挂账）  
  `gitea_create_comment` / `gitea_create_pr` 是否暴露给 harness。风险：推理中途可写 → 乱序评论、重复评论、部分失败不可回滚。需先给出幂等与回滚方案再评估。

- [ ] 3.9 `workspace_transport=mcp`（L2 全隔离档，D11 部署档位）  
  真异地/跨组织边界部署，工作区经 MCP 文件工具抵达。⚠️ 性能税：agent loop 内每次 `read_file` 一次 round-trip，大仓库比 L0 慢一个数量级 —— 是**为隔离付费，不是升级**。依赖 2.3.1 MCP Server 落地。

- [ ] 3.10 MCP Server 实时工具桥 + 网关级 skill script 远程执行（D11 安全前置）  
  必须同时满足：① 只在沙箱工作目录内执行；② API Key 鉴权；③ 单 workflow 作用域绑定。三者缺一不可。

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
| 接管远程 harness 自身的 skill/plugin 机制 | OpenCode plugin、Claude Code skills 各管各的；Matea 只保证自己的 skill 能被任何 harness 用到（D11） |
| 用 ToolBox 替换成品 harness 自带工具 | 只能 append 不能 replace；重名会导致模型在两套同功能工具间摇摆（D11） |
| 给远程 harness 塞一套沙箱文件工具 | 负收益：它自带且直读本地盘更快，经 ToolBox 多一个 round-trip（D11） |
| 引入外部 harness 的 SDK（Pi/Codex/Claude/OpenCode） | 全为 TS/Python，无法嵌进 Go；统一 out-of-process spawn/connect（D12） |

---

## 现行文档（非归档）

| 文档 | 用途 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 现行架构 |
| [AGENTS.md](AGENTS.md) | Agent 配置与 role 说明（1.6.3 新增） |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 部署 |
| [20260805-Phase2-plan.md](20260805-Phase2-plan.md) | **Phase 2 实施方案（D1–D12 已拍板，方案冻结）** |
| [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md) | 产品定位与演进规划 |
| [server-runtime-design-v4.md](server-runtime-design-v4.md) | OpenCode / CodingBackend（待按 HubBackend 刷新） |
| [todo-20260714-LLMProvider-可选增强.md](todo-20260714-LLMProvider-可选增强.md) | LLM 可选增强 |
| [archived/20260804-Phase1.5-plan.md](archived/20260804-Phase1.5-plan.md) | Phase 1.5 计划（已收官归档） |
| [archived/20260805-Phase2-evolution-direction.md](archived/20260805-Phase2-evolution-direction.md) | Phase 2 演进方向讨论（结论已并入 Phase2-plan） |
| [archived/20260808-IM-channel-integration-analysis.md](archived/20260808-IM-channel-integration-analysis.md) | IM 渠道接入分析（结论已并入 D5/D6） |
| [archived/20260803-TASKS.md](archived/20260803-TASKS.md) | v0.11.4 任务清单归档 |
| [archived/20260724-TASKS.md](archived/20260724-TASKS.md) | 写路径 / 摩擦 / Bootstrap |
| [archived/20260723-TASKS.md](archived/20260723-TASKS.md) | P3 + 开源后加固 |
| [archived/](archived/) | 历史设计、签核、E2E |
