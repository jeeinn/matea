# 任务清单

> 更新：2026-08-03（Hub 后端演进规划；已按 [规划评审](20260803-规划评审-Hub演进与Agent简化方案.md) 修正 P0/P1 项）  
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

- [ ] 1.5.1 保留 `free/standard/strict` 预设，不开放策略单元  
  但将 gate 评估拆成独立函数，便于 Phase 3 配置化。

- [ ] 1.5.2 隐藏 WorkflowContext stage 的用户可见面  
  用户只感知「Agent 正在处理 / 已回复 / 已创建 PR」。  
  顺带修复既有缺陷：mention 路径会 `Transition(ctx, role)` 推进 stage，但 `OnTaskComplete("reply_comment"/"solve_comment")` 不回落 stage，导致 stage 滞留（如 `analyzing`）。

- [ ] 1.5.3 保留 `EnsureGiteaAccount`，默认开启，可配置关闭  
  `gitea.auto_provision: false` 时仅校验/提示手动创建。

### 1.6 文档与引导

- [ ] 1.6.1 更新 README / 快速开始：默认体验仍是「下载二进制 → 配 Gitea/LLM → Gitea @matea-analyst」
- [ ] 1.6.2 新增「接入 Hub 后端」进阶章节，不放在快速开始里
- [ ] 1.6.3 更新 AGENTS.md 产品叙事：默认 builtin，可选 hub-*

---

## Phase 2：Hub 后端接入（2–3 月）

目标：让 Matea 可以把 Agent 思考/执行外包给 Hermes / OpenCode 等 Hub，并实现 IM 触发/回传。

### 2.1 hub-hermes 实现

- [ ] 2.1.1 实现 `internal/agents/backends/hermes`  
  `HubBackend.Submit` 按 task_type 分支；HTTP/API 调用 Hermes；Handle 持久化与重启恢复。  
  重启恢复须满足 1.2.1 的「落库 + Executor 重启拾取」要求，不得仅做 Runner 内串行 Poll。  
  📌 Phase 1.2 评审挂账（20260804-Phase1.2-code-review 🟡）：  
  ① **IdempotencyKey 去重落地**——1.2 两个实现均以 RemoteID 为缓存键，IdempotencyKey 已计算未消费；Handle 持久化时须以其防重复提交。  
  ② **Poll 重启措辞统一**——接口注释已按「重启安全只约束 Handle 落库的异步后端」收紧（hub_backend.go），持久化接管时复核实现/测试措辞一致。

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

- [ ] 2.2.1 `hub-opencode` 扩展为全 role backend  
  在 1.2.5 基础上，从仅 coder 扩展到覆盖 analyze/review/reply 全 task_type（原「改造为 hub-opencode」与 1.2.5 重复，已去重）。

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
