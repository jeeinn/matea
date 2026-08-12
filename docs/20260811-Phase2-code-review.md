# Phase 2 `phase2/hub-ecosystem` 分支代码评审报告

**评审日期**：2026-08-11
**对比分支**：`master` → `phase2/hub-ecosystem`
**提交范围**：12 个提交（`b4aab51` → `379ae9b`）
**变更规模**：47 个文件，**+5,812 / -285** 行（净增约 5.5k 行）

---

## 一、总体进度核实

### 1.1 TASKS.md 勾选状态 vs 代码实际落地（逐项对照）

| 任务 ID | 描述 | TASKS.md 勾选 | 代码实际落地 | 核实结论 |
|---|---|---|---|---|
| **2.0.1** | Harness 接口 + harnessRouter 注册表（D10） | ✅ | 接口 + 注册表已定义；BuiltinHarness 已实现；**但未收敛三套执行接口**（Runner 仍直连 HubBackend/CodingBackend） | ⚠️ **部分属实**：基础设施就绪，未接线；TASKS.md 已诚实标注"接口就绪、未接线" |
| **2.0.2** | builtin 改造为 in-process adapter | ✅ | `internal/agents/harness_builtin.go` 已实现，单测 `harness_builtin_test.go` 通过 | ✅ 属实 |
| **2.0.3** | ToolBox 三层策略暴露（D11） | ✅ | `internal/agents/toolbox.go` 实现三层分类；Gitea 读侧工具已实现；**`NewToolBox` 全仓零非测试引用**，无生产调用方 | ⚠️ **部分属实**：工具箱已造好，未插入任何执行路径；TASKS.md 已诚实更正"悬空基础设施" |
| **2.0.4** | 网关级 skill → ToolBox | ✅ | `RegisterGatewaySkills` / `GetGatewaySkillBody` / `ListGatewaySkillNames` 已实现；`matea_skill_` 前缀防冲突；`toolbox_skill_test.go` 7 个用例全过 | ✅ 属实 |
| **2.0.5** | workspace_transport 语义位 | ✅ | schema 已加 `WorkspaceTransport` 字段；`ValidateBackendWorkspaceTransport` 验证；`hub-opencode` 仅支持 `shared_path`；`workspace_transport_test.go` 6 用例过 | ✅ 属实 |
| **2.1.1** | hub-hermes 后端实现（Runs API + Poll） | ✅ | `internal/agents/backends/hermes/hermes.go` 实现 Submit/Poll/Cancel/Capabilities/HealthCheck；Bearer 鉴权；session_id；**17 个单测** `hermes_test.go` + **4 个 e2e** `e2e_test.go` 全过 | ✅ 属实 |
| **2.1.1-a** | Handle 持久化 + Executor 重启拾取 | ✅ | `internal/store/hub_handle.go` `hub_handles` 表 + CRUD；`internal/dispatcher/executor.go` `ReattachHubHandles`；`internal/dispatcher/dispatcher.go` `FailOrphanedRunningTasksExceptHub`；`reattach_test.go` 6 用例过 | ✅ 属实 |
| **2.1.1-b** | IdempotencyKey 去重 | ✅ | `internal/agents/hub_run.go` 提交前先查 `GetHubHandle`，命中非终结态则复用，绝不重复 Submit | ✅ 属实 |
| **2.1.2** | analyze_issue → Hermes | ✅ | `internal/agents/runner_analyze.go` 分支已接；`e2e_test.go::TestAnalyzeRunnerViaHermes` PASS | ✅ 属实 |
| **2.1.3** | review_pr → Hermes | ✅ | `internal/agents/runner_review.go` 分支已接；diff 注入；`TestReviewRunnerViaHermes` PASS | ✅ 属实 |
| **2.1.4** | reply_comment → Hermes | ✅ | `internal/agents/runner_interaction.go` 分支已接；评论历史转 `CommentSnapshot`；session_id 续接；`TestInteractionRunnerViaHermes` PASS | ✅ 属实 |
| **2.1.5** | 跨任务记忆共享（D3） | ✅ | `internal/store/memory.go` `memories` 表（repo/issue KV + UNIQUE）；`saveAnalyzeMemory` / `saveReviewMemory`；Hermes 请求体注入 MemoryKeys；`TestHermesMemorySharing` PASS | ✅ 属实 |
| **2.2.1** | D7 第一刀：analyze → OpenCode | ✅ | `internal/agents/runner_analyze.go` 分支已接；`prepareAnalyzeWorkspace` + 失败降级；`TestReviewRunnerViaOpenCode` 等探针验证 | ✅ 属实 |
| **2.2.2** | D7 第二刀：review → OpenCode | ✅ | `internal/agents/runner_review.go` 分支已接；`prepareReviewWorkspace`（PR head 克隆）；`saveReviewMemory`；`internal/gitea/pr.go` 新增 `PRHeadRef` | ✅ 属实 |
| **2.2.3** | D7 第三刀：reply → OpenCode | ✅ | `internal/agents/runner_interaction.go` 分支已接；**决策 B：制备最小空 workspace**（不 clone）；`runSingleShotReply` 降级路径抽取；内置路径也复用 | ✅ 属实 |
| **2.2.4** | deliver.webhook_url 配置 + 文档 | ✅ | `config.example.yaml` + `config.full-example.yaml` deliver 段含注释、Flask bridge 骨架；README / AGENTS.md / DEPLOYMENT.md 同步更新 | ✅ 属实 |
| **2.3.1** | （可选）MCP Server 实现 | ❌ 未勾 | 代码中不存在 `internal/ingress/mcp` 包；方向正确：按冻结方案为"降级可选" | ✅ 按降级决策正确跳过 |
| **2.3.2** | （可选）MCP 入站鉴权 | ❌ 未勾 | 未实现 | ✅ 同上 |
| **2.3.3** | deliver 出站扇出模块 | ✅ | `internal/deliver/deliver.go` Config/Event/Client；空 URL=no-op；5xx/网络退避重试；4xx 不重试；`deliver_test.go` 5 例 + `deliver_wiring_test.go` 接线验证全过 | ✅ 属实 |
| **2.3.4** | deliver.webhook_url 配置指向说明 | ❌ 未勾 | config.full-example.yaml 给了三选一说明 + Flask 骨架；README 有拓扑说明；但未独立成章 | ⚠️ 部分落地但未勾选，与"不勾选=不做"的文档约定偏差轻微 |
| **2.3.5** | 系统配置页 MCP + Deliver 配置块 | ❌ 未勾 | `web/src/views/SystemConfig.vue` 中 `grep deliver\|mcp` 零命中；前端 UI 无法热修改 deliver 配置 | ❌ **未落地**：与 TASKS.md 勾选一致，但为用户痛点 |
| **2.3.6** | IM 渠道不自研 SDK + 拓扑文档 | ❌ 未勾 | README / config.full-example.yaml 有部分拓扑说明；未独立成章 | ⚠️ 部分覆盖 |
| **2.4.1~2.4.4** | Mock Hub 验收场景 | ❌ 全未勾 | Hermes/OpenCode 各自 e2e 有单 task 级 mock；跨任务 2.4.1~2.4.4 全流程未做独立集成测试 | ⚠️ 验收缺口见 §三 |

**进度总结**：Phase 2 方案冻结的 **核心必做项（2.0 + 2.1 + 2.2 + 2.3.3）全部落地且测试通过**；TASKS.md 对 D10/D11 的"悬空基础设施"和 2.3.x 可选项的未勾选状态**与代码实际一致**，无"代码注水勾选"现象。整体勾选诚实度 **高**。

---

## 二、方向性与架构问题分析

### 2.1 ✅ 符合方向性的设计（值得肯定）

1. **HubBackend 异步契约 + Handle 持久化**：`Submit → 立即落库 → Poll → Handle 终态` 的完整闭环已正确实现，`Executor.ReattachHubHandles` 使 Matea 重启不再丢失在途 Hub 任务。这是 Phase 2 可靠性的核心基础，实现符合 1.2.1 的强制验收标准。

2. **Runner 分支接线策略正确**：
   - hub-hermes 先路由（`ResolveHubExecution`），gitea 信息作为 TaskContext 传入，**Hub 侧绝不直接调 Gitea**，守住了"Matea 是 Gitea 唯一写方"的边界。
   - hub-opencode 分析/评论走 `prepareXxxWorkspace` + 共享路径绑定，符合 D7 "OpenCode 自带工具且直读本地盘更快"的推理。
   - OpenCode reply 采用 **决策 B（最小空 workspace）** 而非放宽 Submit 契约，保持了 `SandboxPath` 的强校验，避免后续引入隐形 bug。

3. **deliver 仅出站 + 尽力不阻塞任务**：`emitDeliver` 失败只 `log.Printf`，不回写失败，不影响 Gitea 评论主流程——符合"通知是附赠品"的产品定位。4xx 不重试 / 5xx 退避重试的区分也合理。

4. **ToolBox 只 append 不 replace 原则已写死**：`ToolDecl.Exposure = ExposureBuiltinOnly` 对远程 harness 过滤掉沙箱类工具，避免与 Hermes/OpenCode 自带工具重名导致模型摇摆。D11 核心约束执行到位。

5. **写任务（runner_write.go）未硬接 Hermes 分支**：`runWriteTask` 仍走旧 `ResolveCodingBackend`，**未贸然把 solve_issue / fix_bug 推进 hub-hermes 路径**。这是**正确的保守策略**——写任务涉及 git/PR 落地，需等读路径验证稳定后再加，与 Phase 2 计划中 D7 三刀"先接通读/回复三类"的顺序一致。

### 2.2 ⚠️ 需要关注的方向性问题

#### 问题 A：Harness / ToolBox 基础设施悬空 → Phase 3 合并后技术债风险

**现状**：
- `internal/agents/harness.go` 的 `Harness.RunTurn` 不是活跃执行路径；Runner 仍直连 `HubBackend.Submit/Poll` 和 `CodingBackend.Run` 三套接口。
- `internal/agents/toolbox.go` 的 `NewToolBox` 在生产代码中**零引用**。

**风险**：
- Phase 3 要真正收敛执行路径时，发现 Harness 抽象与真实执行流（尤其写任务的 prepareWorkspace → coding → finalizeWriteChanges 三段式）不匹配，需返工重构。
- ToolBox 的"沙箱类 builtin 暴露、远程不暴露"策略，未在 OpenCode/Hermes 的真实请求中验证过（目前 builtin 用的是独立 `AssembleToolRegistry` 路径，不是 ToolBox）。

**建议**：Phase 2 合 master 前至少补一个 `// TODO(phase3): wire Harness.RunTurn into runners` 标记，并在 ARCHITECTURE.md 记录当前执行流的双轨状态，避免后人误判 D10/D11 已完成收敛。

#### 问题 B：写路径（OpenCode）新旧双轨不一致

**现状**：
- 读任务（analyze/review/reply）的 hub-opencode 分支 → 走 `ResolveHubOpenCode + runViaHub`（**有 Handle 持久化、重启拾取、幂等去重**）
- 写任务（solve_issue/fix_bug）→ 走旧 `ResolveCodingBackend → CodingBackend.Run`（**无 Handle 持久化、无重启拾取、无幂等**）

**风险**：生产用户给 coder Agent 配 `hub-opencode` 后，遇到 Matea 崩溃会丢失正在运行的写任务，且同一任务重复入队会触发重复 sidecar session。这是 Phase 1 到 Phase 2 升级引入的**不一致退化**。

**建议**（二选一，在合并前或合入后一个 sprint 内解决）：
1. **推荐**：把写任务也接入 `runViaHub`（给 `OpenCodeHTTPBackend.Submit` 加写任务的请求体映射 + 写任务 PrepareWorkspace 的接线），统一所有 hub 后端走 Submit/Poll。
2. **最小修复**：在 `runner_write.go` 的 CodingBackend 路径也加 Handle 持久化和幂等检查（至少保证不丢任务）。

#### 问题 C：builtin 路径不会触发 deliver 事件

**现状**：`emitDeliver` 只在 `mapHubResult`（`internal/agents/hub_run.go`）里调用。builtin 执行路径（runAnalyzeLoop / runSingleShot / runWriteTask 的 finalizeWriteChanges 后）完全没有 deliver 调用点。

**与文档冲突**：`config.full-example.yaml` 注释写着"builtin — 可选（默认结果评论已写回 Gitea，IM 通知按需）"。按此语义，builtin 用户配置 `webhook_url` 应该能收到通知，但实际上不会。

**风险**：用户配了 deliver 后发现 builtin 任务无通知，误以为 deliver 模块坏了。

**建议**：要么在 builtin 的所有 runner 返回 `Result` 后补一个 `emitDeliver` 调用（推荐，使语义与文档一致），要么把文档注释改为"builtin 当前不支持 deliver 出站，仅 hub 路径支持"。

#### 问题 D：2.3.5 SystemConfig 前端缺 Deliver/MCP 配置块

**现状**：`docs/TASKS.md 2.3.5` 已明确标记未做，但这是**评审新增的工作量较小的需求**。当前交付结果：
- deliver 仅可通过 YAML 文件静态配置，改后需重启
- `config.manager.go` 的热更新链路未验证 `deliver` 键是否在热更新白名单中

**建议**：合 master 前至少确认 `config/keys.go` 热更新白名单已包含 `deliver.*`（若未包含，补一行）；UI 可延后，但热更新必须可用，否则用户改 webhook_url 必须重启服务的体验太差。

#### 问题 E：Hermes Cancel 实际是空实现

**现状**：`internal/agents/backends/hermes/hermes.go` `Cancel` 直接 return nil，未调任何 HTTP endpoint。注释写"Hermes Runs API has no documented cancel endpoint in the minimal contract"。

**风险**：当 Matea 侧 `hubPollTimeout`（30min）触发 `abortHubRun` 时，会调 `backend.Cancel` 期望停止 Hermes 侧 run，但实际 run 会继续运行到 Hermes 自身的 timeout，产生费用 / 资源浪费，且 Matea 重启后 `ReattachHubHandles` 会重新拾取这个已被本地 abort 的 run（因为 Handle 未标终态）。

**建议**：
1. 在 `abortHubRun` 的 Cancel 调用失败后，**本地把 Handle 标记为 canceled**（`UpdateHubHandleStatus(taskID, store.HubHandleStatusCanceled)`），避免重启后重新拾取。
2. 发邮件/文档核实 Hermes 是否真的没有 cancel endpoint，如果有（例如 `DELETE /v1/runs/{id}` 或 `POST /v1/runs/{id}/cancel`），立即接线；如果没有，在 Hermes 后端的 Capabilities 或文档中明确告知用户"abort 后 Hermes 侧 run 可能继续"。

---

## 三、代码质量与测试

### 3.1 构建与测试结果

```
✅ go build ./...                         — PASS（零错误零警告）
✅ go test ./internal/agents/...          — PASS（agents + hermes backend，约 40s）
✅ go test ./internal/store/...           — PASS（约 16s）
✅ go test ./internal/deliver/...         — PASS
✅ go test ./internal/dispatcher/...      — PASS（含 reattach 测试）
✅ go test ./tests/integration/...        — PASS（约 39s，MockHub + workflow 全量）
```

全量构建 + 核心包测试 **零失败**，质量基线良好。

### 3.2 新增代码的测试覆盖率评估

| 模块 | 新增测试文件 | 用例数量估计 | 覆盖特点 |
|---|---|---|---|
| `agents/harness*` | harness_test.go / harness_builtin_test.go | ~30 个 | 注册表 CRUD、Profile 元数据、Builtin RunTurn |
| `agents/toolbox*` | toolbox_test.go / toolbox_skill_test.go | ~25 个 | 三层暴露策略、Gitea 工具正反例、skill 注册与脚本执行 |
| `agents/backends/hermes` | hermes_test.go / e2e_test.go | ~21 个 | HTTP 正常/失败/鉴权/502/对话历史/diff/记忆共享 |
| `agents/hub_run*` | hub_run_test.go | ~10 个 | 分析/评论记忆写入 |
| `agents/opencode_hubbackend_test.go` | （扩展） | +~10 个 | OpenCode 探针 + 降级路径 |
| `store/hub_handle*` | hub_handle_test.go | ~10 个 | CRUD / 状态流转 / 孤儿任务过滤 |
| `deliver/*` | deliver_test.go + deliver_wiring_test.go | ~7 个 | 重试策略、4xx 不重试、客户端为空不 panic、hub 接线 |
| `dispatcher/reattach_test.go` | reattach_test.go | ~6 个 | 重启拾取全场景 |
| `config/workspace_transport_test.go` | workspace_transport_test.go | ~6 个 | 验证值 + 后端默认值 |

新增测试约 **115 个用例**。**测试投入合理，质量合格**。主要缺口：
1. `mapHubResult` 的 `GiteaActions` 非空 warning 分支未覆盖
2. `abortHubRun` 的 context 取消归因（executor cancel vs timeout vs deadline）分支未覆盖
3. `ReattachHubHandles` 的 task=terminal 分支已覆盖，但"task pending 应跳过"分支未见断言

---

## 四、评审结论与合并建议

### 4.1 结论摘要

| 维度 | 评级 | 说明 |
|---|---|---|
| **进度真实度** | 🟢 高 | TASKS.md 勾选与代码实际高度一致；D10/D11 的"就绪未接线"已在文档中诚实更正，无注水勾选 |
| **方向性** | 🟢 正确 | 核心决策（异步契约、Gitea 唯一写方、ToolBox 只 append、Hermes 不反操沙箱）均按 Phase2-plan 冻结方案执行，未偏航 |
| **代码质量** | 🟢 良好 | 全量构建 + 核心测试全过；~115 个新增用例；命名/注释/包结构符合项目惯例 |
| **架构一致性** | 🟡 有轻微技术债 | 写任务新旧双轨、builtin 不触发 deliver、Harness/ToolBox 悬空——都是可接受的阶段性留债，但建议在合 master 前列清单跟踪 |
| **可靠性硬伤** | 🔴 2 处需修 | **Hermes Cancel 空实现导致的重启重复拾取**；**写任务 hub-opencode 路径无持久化无重启恢复** |

### 4.2 合入 `master` 的前置条件（必做，共 3 项）

1. **修复 E-1**：Hermes abort 后 Handle 未标终态导致重启重跑
   - 在 `abortHubRun` 结束前（或 Cancel 调用后），调用 `factory.markHubHandleTerminal(taskID, store.HubHandleStatusCanceled)`。
   - 因为 `abortHubRun` 目前是独立函数，不接收 factory/taskID，需把函数签名改为 `abortHubRun(ctx, pollCtx context.Context, factory *RunnerFactory, task *store.Task, backend HubBackend, handle *Handle)` 或在调用者侧补。

2. **修复 E-2（最小方案）**：写任务 OpenCode 路径的一致性
   - 方案 A（推荐，~1 人日）：写任务走 `runViaHub` + 持久化。
   - 方案 B（最小，~2 小时）：在 CodingBackend.Run 的 session 创建后也 `SaveHubHandle`，并在 Executor 的 Reattach 逻辑中对 OpenCode 的旧 session 做兼容（至少把孤儿 task 标 failed 不丢结果）。
   - 推荐方案 A，因为可一次性消除双轨不一致。

3. **确认热更新**：检查 `internal/config/keys.go` 的热更新白名单是否包含 `deliver.webhook_url`、`deliver.timeout`、`deliver.max_retries` 三个键。若未包含，补上。

### 4.3 建议合入后立即跟的后续项（P0，1~2 个 sprint）

- **S1**：builtin 路径补 deliver 调用，使文档注释与行为一致。
- **S2**：SystemConfig.vue 补 Deliver Tab（仅 webhook_url/timeout/max_retries 三字段，MCP 可延后）。
- **S3**：在 ARCHITECTURE.md 新增「当前执行双轨状态」说明，记录 Harness/ToolBox 将于 Phase 3 收敛的计划。
- **S4**（可选）：Hermes Cancel endpoint 核实与接线。

---

## 五、方向性评审总评

**整体判断：方向正确，进度基本属实，在满足 3 项前置条件后可合入 master。**

核心价值已交付：HubBackend + Handle 持久化地基（D1/D2/D9）、Hermes 读/回复三类 + 跨任务记忆（2.1）、OpenCode D7 三刀（2.2）、deliver 出站（2.3.3）。这四块构成了 Phase 2 的可用产品主体，且测试全部通过。TASKS.md 的勾选诚实度高，没有"拍脑袋勾选"现象，符合项目评审流程的严肃性。Harness/ToolBox 的阶段性悬空是可接受的，已在文档中显式标记，不构成合入阻塞。

---

## 六、P0 前置项落地记录（2026-08-12）

三项合入前置（E-1 / E-2 / D）已在本分支以代码改动形式落地。`go build ./...` 通过；受影响包测试（`internal/config`、`internal/deliver`、`internal/store`、`internal/agents` 的 hub/opencode/abort/reattach 过滤子集）全部 PASS；`go vet` 全绿。

### E-1 — Hermes abort 后标记 Handle 终态
- **文件**：`internal/agents/hub_run.go`
- **改动**：`abortHubRun` 签名扩展为 `(f *RunnerFactory, task *store.Task, ctx, pollCtx context.Context, backend HubBackend, handle *Handle)`；在 `backend.Cancel` 调用后新增 `f.markHubHandleTerminal(task.ID, store.HubHandleStatusCanceled)`；更新 `runViaHub` 两处调用点。
- **效果**：Matea 侧 abort（executor 取消 / `hubPollTimeout`）后，本地 Handle 立即标 `canceled`，重启不再被 `ReattachHubHandles` 重新拾取该孤儿 run，避免重复跑 + 重复计费。
- **测试**：新增 `TestRunViaHubAbortMarksHandleCanceled`（取消 ctx 触发 abort 分支，断言 Handle 状态为 `canceled`）。

### E-2 — 写任务 hub-opencode 双轨一致性（方案 B，最小）
- **文件**：`internal/agents/runner_write.go`（`runWriteTask`）
- **改动**：
  - 入口通过 `factory.ResolveHubOpenCode(agentCfg)` 识别 hub-opencode 写任务；
  - **幂等 / 重启重接**：若 DB 已存在该任务的非终结态 Handle，则经 `hb.Poll` 重接仍存活的 sidecar session 恢复 summary，绝不创建第二个 session（关闭「重复入队触发重复 sidecar session」漏洞）；
  - **持久化**：新建 session 成功后 `SaveHubHandle`（Running）；成功 / 失败分别 `markHubHandleTerminal(done/failed)`；
  - 非 session 工作区重入时会重新 clone，sidecar 磁盘改动无法带回，故重接不可恢复时标记 `failed`（避免产出空 PR），交由人工重试；session 工作区复用原目录，可真正恢复。
- **效果**：hub-opencode 写任务与读/回复 hub 路径在 Handle 持久化 + 重启恢复 + 幂等上保持一致；不再因 stale scanner 重置或重启而重复 coding。`builtin` 与其他后端路径不受影响（`isHubOpenCode=false` 时整段跳过）。
- **测试**：依赖既有 `OpenCodeHTTPBackend.Poll` 重接测试（`opencode_hubbackend_test.go`）；端到端重接建议在 CI 用真实 sidecar 补集成测试（本机沙箱无法跑完整 agents 套件，会被网络/git 类测试拦截）。

### D — deliver 热更新白名单
- **文件**：`internal/config/keys.go`
- **改动**：`configKeys` 列表新增 `deliver.webhook_url` / `deliver.timeout` / `deliver.max_retries`；并在 `parseConfigValue`、`getConfigValueTyped`、`applyConfigEntry`、`getConfigEntry` 四个 switch 中补齐对应分支（`webhook_url`/`timeout` 为 string，`max_retries` 为 int）。
- **效果**：Web UI「系统配置」热修改 `deliver.*` 后无需重启即可生效。
- **测试**：`internal/config` 包测试通过。

### 仍需合入后跟的（非阻塞）
- S1：builtin 路径补 `emitDeliver`（与文档语义一致）；S2：`SystemConfig.vue` 补 Deliver Tab；S3：`ARCHITECTURE.md` 记录双轨状态；S4：Hermes 真实 cancel endpoint 核实与接线。
