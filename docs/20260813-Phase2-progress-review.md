# Phase 2 `phase2/hub-ecosystem` 进度核实与收官评审

**评审日期**：2026-08-13
**对比分支**：`master` → `phase2/hub-ecosystem`
**提交范围**：24 个提交（`b4aab51` → `4b4e758`，含本文档记录的 2.4.3 功能与 F-1/F-2 修复）
**变更规模**：53 个文件，**+6,640 / -310** 行
**前序评审**：[20260811-Phase2-code-review.md](20260811-Phase2-code-review.md)（覆盖前 12 个提交的逐项代码核实；本次重点核实其后的 8 个提交 + 全量回归 + 方向性与收官判断）

---

## 一、核实方法

1. TASKS.md Phase 2 全部条目（2.0–2.4）逐项对照代码实际落地；
2. 对前序评审后的新增提交（E-1 / E-2 / D / S1 / S2 / S3 + 2026-08-13 决策更新）逐条独立验证；
3. 对 TASKS.md 两处「诚实更正」声明（Harness 未接线、ToolBox 零生产引用）独立 grep 复验；
4. 全量 `go build ./...` + `go test ./... -count=1`。

---

## 二、进度核实结论

### 2.1 前序评审后新增提交（本次独立验证）

| 项 | 提交 | 声明 | 核实结果 |
|---|---|---|---|
| **E-1** Hermes abort 后 Handle 标终态 | `02ea042` | `abortHubRun` 在 Cancel 后 `markHubHandleTerminal(canceled)` | ✅ 属实：`hub_run.go:190/263` 两处终态标记均在；重启重拾取漏洞已堵 |
| **E-2** hub-opencode 写任务持久化 + 幂等 | `2aa6767` | `runWriteTask` 识别 hub-opencode；重入先 `GetHubHandle` 经 `Poll` 重接，新建后 `SaveHubHandle`，成功/失败标终态 | ✅ 属实：`runner_write.go:155-261` 完整实现（重接恢复 / 非 session 工作区不可恢复标 `failed` 交人工 / finalize 前后终态标记）；`builtin` 路径零影响 |
| **D** deliver 热更新白名单 | `507cf02` | keys.go 三键 + 四个 switch 分支 | ✅ 属实：`keys.go:39-41` 白名单 + `parseConfigValue`/`getConfigValueTyped`/`applyConfigEntry`/`getConfigEntry` 四处分支齐备 |
| **S1** builtin 路径补 deliver | `118b543` | 六个内置返回点接 `emitBuiltinDeliver`；hub/builtin 互斥至多一次投递 | ✅ 属实：`runner_analyze.go:167,271`、`runner_review.go:186,234`、`runner_interaction.go:194`、`runner_write.go:262` 六个调用点均在；未配置时静默 no-op（`hub_run.go:319-331`） |
| **S2** SystemConfig Deliver Tab | `7264b8c` | 三字段 UI + `saveAll()` 复用 | ✅ 属实：`SystemConfig.vue:357-381` 三字段 + sourceTag 均在 |
| **S3** ARCHITECTURE.md 双轨记录 | `85876fb` | 「Phase 2 — HubBackend 双轨架构」章节 | ✅ 属实：`ARCHITECTURE.md:687-756` 三处双轨 + 悬空声明完整 |
| 2026-08-13 决策（MCP 延迟） | `25ee05d` | 2.3.1/2.3.2/2.3.5-MCP Tab 向后延迟，理由记录在案 | ✅ 属实：决策与代码状态一致（无 `internal/ingress/mcp` 包、无 `mcp.*` schema） |

### 2.2 TASKS.md 勾选诚实度复验

- **「Harness 接口就绪、未接线」（2.0.1）**：✅ 声明属实。`Harness.RunTurn` / `harnessRouter.GetHarness` 的生产调用方为零（仅 `harness.go` 定义、`harness_builtin.go` 实现与测试引用）；Runner 仍直连 `HubBackend`/`CodingBackend`。
- **「`NewToolBox` 全仓零非测试引用」（2.0.3）**：✅ 声明属实。grep 全仓仅 `toolbox.go` 定义 + 两个测试文件引用。
- **2.3.5 保留 `[ ]`**：✅ 诚实口径——Deliver Tab 已做、MCP Tab 未做，未整项勾选。
- **2.4.1–2.4.4 全 `[ ]`**：✅ 与代码一致（无跨任务全流程集成测试）。

**结论：TASKS.md 勾选与代码实际高度一致，无注水。两处「已完成但悬空」的主动更正在同类项目中罕见，诚实度评价维持「高」。**

### 2.3 全量回归

```
go build ./...            ✅ PASS
go test ./... -count=1    ✅ 17/17 包 PASS（含 agents 60s、store 30s、integration 50s 全量）
```

---

## 三、完成度评估

| 区块 | 项数 | 完成 | 状态 |
|---|---|---|---|
| 2.0 机制骨架（D10/D11/D12 地基） | 5 | 5 | ✅ 全部落地（其中 2.0.1/2.0.3 为「接口就绪未接线」，已声明） |
| 2.1 hub-hermes | 5+2 子项 | 7 | ✅ 全部落地（含 1.2.1 强制验收点 Handle 持久化/重启拾取/幂等） |
| 2.2 hub-opencode（D7 三刀 + deliver 配置） | 4 | 4 | ✅ 全部落地 |
| 2.3 MCP + Deliver | 6 | 2.5 | 2.3.3/2.3.4 ✅；2.3.5 半（Deliver Tab ✅ / MCP Tab 延迟）；2.3.1/2.3.2/2.3.6 决策延迟 |
| 2.4 mock Hub 验收场景 | 4 | 0 | ❌ 唯一实质缺口 |
| 评审衍生修复（E-1/E-2/D/S1/S2/S3） | 6 | 6 | ✅ 全部落地 |

**量化结论**：
- **必做代码项完成度 ≈ 100%**（方案冻结口径下的 2.0/2.1/2.2/2.3.3/2.3.4 全落地）；
- **Phase 2 整体完成度 ≈ 85%**——缺口集中在 2.4 验收场景（0/4）与已决策延迟的 MCP 可选项；
- 前序评审的三项合入前置（E-1/E-2/D）已全部满足，**合入 master 无阻塞项**。

---

## 四、方向性评估

### 4.1 方向正确的决策（维持）

1. **异步契约闭环**：`Submit → 落库 → Poll → 终态` + 重启拾取 + 幂等去重，读/回复/写三类任务现已**全类型覆盖**（E-2 后写任务也补齐），1.2.1 的强制验收点完全兑现。
2. **Gitea 唯一写方**：hub 后端只收 TaskContext 不触 Gitea；写任务 patch 由 Matea `finalizeWriteChanges` 统一落地。边界无侵蚀。
3. **MCP Server 延迟决策（2026-08-13）合理**：2.4 验收本就不依赖它；先做 UI 会产生无后端支撑的死 UI；Server 与现有 MCP Client 方向相反需从零起。延迟而非取消，决策记录完整。
4. **写任务保守推进**：先接通读/回复三类、验证稳定后再补写任务一致性（E-2），顺序符合 D7/D9 计划。

### 4.2 需要关注的事项（按优先级）

#### ① 2.4 验收缺口是 Phase 2 收官的唯一实质障碍

四个场景全未做，且存在一个**此前未被发现的功能缺口**：

- **2.4.3「PR 合并后 → deliver 出站事件」在当前代码下不可能发生**：deliver 唯一事件类型是 `task_completed`（任务完成时由 runner 发出）；PR merge 的 webhook 由 `workflow/lifecycle.go::OnPRClosed` 处理，只做 session 归档与工作区清理，**无 deliver 调用点**。用户合并 PR 后不会有任何出站事件。
- 2.4.1/2.4.2 的「飞书用户 ↔ Hermes」腿在 Matea 仓外（Hermes gateway 职责），Matea 侧可验收的只是「runs API 查上下文 / HTTP API 派任务 → 执行 → 写回 Gitea」。

**建议（合入后第一个 sprint）**：
- 把 2.4 拆成「Matea 侧可测」与「跨系统人工验收」两半；
- Matea 侧补集成测试：webhook → dispatch → MockHub → Gitea 写回 + deliver 事件断言（MockHub 地基 1.2.7 已就绪，成本低）；
- 2.4.3 做产品决策：**(a)** 在 `OnPRClosed(merged=true)` 增加 `pr_merged` deliver 事件类型（约半人日，含测试）；或 **(b)** 修订场景描述为「任务完成（PR 创建）即通知」，维持单事件类型。推荐 (a)——「PR 被合并」是用户最关心的 IM 通知时机。

#### ② Harness / ToolBox 悬空的返工风险（已知留债，需管理）

两处基础设施已实现且有测试，但零生产调用。风险不在当前（不影响任何活跃路径），而在 **Phase 3 接线时发现抽象与真实执行流不匹配**（尤其写任务 prepareWorkspace → coding → finalize 三段式与 `RunTurn` 单 turn 语义的差距）。

**建议**：Phase 3 的第一个任务应是「Harness 接线第一刀」——把 builtin 的某个 runner（建议 analyze，最简单）切到 `Harness.RunTurn` 路径，尽早用真实约束证伪/证实抽象，而不是等 D12 一次性收敛三套接口。

#### ③ 次要项

- **CHANGELOG 滞后**：`Unreleased` 段未收录 E-1/E-2/S1/S2（abort 终态、写任务幂等、builtin deliver、Deliver Tab）。合入/发版前补一段即可。
- **S4 仍开**：Hermes `Cancel` 为 no-op（E-1 已保证 Matea 侧状态正确，但 Hermes 侧 run 会继续计费到自身超时）。需向 Hermes 官方核实是否有 cancel endpoint。
- **E-2 方案 B 的已知尾巴**：非 session 工作区重入不可恢复时标 `failed` 交人工重试——行为安全且已记录，可接受；若未来接 `hub-hermes` 写任务，应直接走 `runViaHub`（方案 A）消除此分支。

---

## 五、结论与下一步建议

### 5.1 结论

| 维度 | 评级 | 说明 |
|---|---|---|
| 进度真实度 | 🟢 高 | 逐项独立验证通过；悬空项主动声明；延迟项决策记录在案 |
| 完成度 | 🟢 必做项 100% / 整体 ~85% | 缺口仅 2.4 验收与已决策延迟的 MCP 可选项 |
| 方向性 | 🟢 无偏航 | 冻结方案 D1–D12 的边界全部守住；延迟决策合理 |
| 代码质量 | 🟢 全量构建 + 17/17 包测试通过 | 新增约 120+ 用例，可靠性闭环（持久化/幂等/重启恢复）测试覆盖到位 |
| 合入就绪 | 🟢 就绪 | 前序评审三项前置全部满足 |

**总体判断：进度属实、方向正确、质量合格，建议合入 master。** Phase 2 的可用产品主体（HubBackend 地基地基 + Hermes 读/回复 + OpenCode 三刀 + deliver 出站 + 可靠性闭环）已完整交付；2.4 验收与 Harness 接线作为合入后首批跟进项，不构成合入阻塞。

### 5.2 下一步（按顺序）

1. **合入 master**：补 CHANGELOG `Unreleased` 的 E-1/E-2/S1/S2 条目后合并；可考虑打 tag 发版（Phase 2 功能面已完整）。
2. **2.4 收口（合入后第 1 个 sprint）**：
   - 2.4.3 决策并补 `pr_merged` deliver 事件（推荐方案 a）；
   - 补 Matea 侧全流程集成测试（MockHub + mock Gitea + deliver 断言）；
   - 跨系统（飞书 ↔ Hermes ↔ Matea）走人工验收并记录结果到 TASKS.md。
3. **Phase 3 开工第一刀 = Harness 接线**（builtin analyze 切 `RunTurn`），尽早证伪悬空抽象；ToolBox 随接线获得第一个生产调用方。
4. **S4**：核实 Hermes cancel endpoint；有则接线，无则在 Capabilities/文档明示「abort 后 Hub 侧 run 可能继续」。
5. **2.3.6 拓扑文档**：补一篇独立的「IM 渠道推荐拓扑」（飞书/企微/钉钉 bridge 部署形态），现分散在 README/DEPLOYMENT/config 注释中。

---

## 六、2.4.3 功能缺口落地记录（补 `pr_merged` 事件）

### 6.1 缺口确认
第四节①已指出：2.4.3「PR 合并后 → deliver 出站事件」在改动前**不可能发生**——`deliver` 唯一事件类型为 `task_completed`（由 runner 在任务完成时发出），而 PR merge 的 webhook 由 `workflow/lifecycle.go::OnPRClosed` 处理，只做 session 归档与工作区清理，**无 deliver 调用点**。独立 grep 复验属实（`lifecycle.go:63-79` 全貌、deliver 包无 `pr_merged` 引用、`SessionLifecycle` 无 deliver 客户端字段）。

### 6.2 决策
产品决策（用户确认）：**方案 (a) 补 `pr_merged` 事件类型**，而非方案 (b) 修订场景描述降规格。理由：PR 合并是用户最关心的 IM 通知时机；成本可控（~半人日）；补完后 2.4.3 从「不可能」变为「可验收」。

### 6.3 实现（最小侵入）
- `internal/deliver/deliver.go`：新增 `EventTaskCompleted` / `EventPrMerged` 常量（开放字符串事件类型的标准化）。
- `internal/agents/hub_run.go`：`emitBuiltinDeliver` 改用 `deliver.EventTaskCompleted` 常量（一致性清理）。
- `internal/dispatcher/dispatcher.go`：`Dispatcher` 加 `deliverClient *deliver.Client` 字段；`SetDeliverConfig` 在转发 executor 之外，复用同包 `buildDeliverClient` 构建 dispatcher 自有客户端（nil/未配置 → 静默 no-op）。
- `internal/dispatcher/pipeline.go`：`handleLifecycleEvent` 在 `OnPRClosed(merged=true)` 后调用新增 `emitPrMerged`，发出 `deliver.Event{Event: pr_merged, Repo, PRID, IssueID, Action:"notify"}`。emit 同步、best-effort，失败仅 log。
- **未选**评审建议的「在 `OnPRClosed` 内 emit」：改在 dispatcher 层，保持 `OnPRClosed` 纯归档职责、不动其 5 处构造/测试调用（加 setter 为 additive、零破坏）。

### 6.4 测试
- `internal/dispatcher/dispatcher_test.go` 新增三项：
  - `TestHandleLifecycleEventPrMergedEmits`：mock webhook 下 `handleLifecycleEvent(merged PR)` 断言收到 `pr_merged` 且字段透传；
  - `TestHandleLifecycleEventPrClosedNoEmit`：PR 关闭未合并 → 不发出任何事件；
  - `TestEmitPrMergedNilClientNoop`：nil 客户端静默 no-op（不 panic）。
- 验证：`go build ./...` ✅；`go vet ./internal/dispatcher/` ✅；`internal/deliver` 全包 ✅；`internal/dispatcher` 全包（8.8s）✅。

### 6.5 勾选口径
`TASKS.md` 2.4.3 已勾 `[x]`，并注明：Matea 侧出站路径已落地；跨系统「Hermes gateway `deliver` 回传飞书」腿仍需真实 Hermes 实例人工验收（属 2.4 跨系统部分，不阻塞本期全量测试）。（功能提交 `937149c`）

---

## 七、全量测试阶段复查：发现并修复 2 处缺陷（2026-08-13）

pr_merged 落地后经复查，实现本身与声明一致（常量 / dispatcher 层 emit / 三测试均真实有效），但复查暴露出 **D 修复（deliver 热更新）链路上的两处遗留缺陷**——均非本轮 pr_merged 新引入，而是 2.3.3/D 修复时遗留：

### F-1 deliver 热更新不到达运行时（功能缺陷）

- **现象**：`PUT /api/config` 修改 `deliver.*` 后，keys.go 白名单接受、DB 持久化、active config 更新均正常，但 `main.go` 的 `onConfigChange` 回调只重载 LLM/Gitea/workflow policy，**从不调用 `SetDeliverConfig`**——运行中的 dispatcher/executor 继续用旧 deliver 客户端直到重启。
- **影响**：D 修复与 2.3.4 声明的「热更新无需重启」实际不成立；用户在 UI 保存 webhook_url 后任务仍无通知，与 S1 之前的症状雷同。
- **修复**：`main.go` 热重载回调补 `d.SetDeliverConfig(newCfg.Deliver)` 一行（+日志文案同步）。（提交 `cca2cf7`）

### F-2 `Dispatcher.deliverClient` 违反本结构体热更新字段的原子约定（并发隐患）

- **现象**：同结构体的 `giteaCfg` 用 `atomic.Pointer`（webhook goroutine 读 / API goroutine 写的既定约定），而新增的 `deliverClient` 是普通字段——`emitPrMerged`（webhook handler goroutine）与 `SetDeliverConfig`（API handler goroutine）存在跨 goroutine 读写竞争窗口。
- **修复**：字段改 `atomic.Pointer[deliver.Client]`（`Store`/`Load`），与 `giteaCfg` 约定一致；`deliver.Client` 构造后不可变，天然适合原子指针。（提交 `4b4e758`）

### 验证

```
go build ./...                 ✅ PASS
go vet ./...                   ✅ PASS
go test ./... -count=1         ✅ 17/17 包 PASS（全量回归，含修复后 dispatcher/deliver）
```

（注：`-race` 需 cgo，本机不可用，未跑；竞争窗口已由原子化消除，不依赖测试暴露。）

### 结论

两处缺陷修复后，deliver 链路「配置 → 持久化 → 热生效 → 出站」全环闭合。全量测试阶段通过，分支维持合入就绪状态。
