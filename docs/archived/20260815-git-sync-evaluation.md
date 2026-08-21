# git_sync 方案调研与评估报告

> 调研日期：2026-08-15  
> 基线：上次发布 `v0.11.4`(2026-07-31，tag `7b939a9`) → 当前 `master`(`9ddd6ef`，与 `origin/master` 同步)  
> 调研对象：自 v0.11.4 以来的 master 代码变更；项目文档中关于 "git sync" 的方案描述、设计、风险与可落地性。

---

## 一、现状基线

| 项 | 状态 |
|---|---|
| 上次发布 | `v0.11.4`（2026-07-31） |
| 当前分支 | `master` @ `9ddd6ef`，与 `origin/master` 同步（**注意**：历史记忆记的 `phase1/agent-simplification` 已过时，实际已回到 master） |
| 自 v0.11.4 以来 | **86 个提交**，98 个代码文件变更，**+10503 / -779**；文档 **+4495 / -121** |
| 工作区暂存 | 两份**新**文档尚未提交：`docs/20260814-hub-git-sync-plan.md`、`docs/remote-hub-deployment-flow.md` |
| `git_sync` 代码实现 | **零**：`internal/` 与 `main.go` 中无任何 `git_sync`/`GitSync*` 引用 |
| 当前可用 hub 写 transport | 仅 `shared_path`（`IsWorkspaceTransportValid` 显式拒绝 `mcp` 与未知值） |
| 团队规模 | **单维护者**：`jeeinn` 与 `一颗红心` 同为 `thinkwei2012@gmail.com` |

---

## 二、master 自 v0.11.4 以来的代码变更

主导主题：**Phase 2 Hub 后端生态**（PR #28 `phase2/hub-ecosystem` 已合入 master，尽管 CHANGELOG 的 [Unreleased] 仍写"未合入 master"，属**文档过时**）。

按职责归类：

| 方向 | 关键变更 | 说明 |
|---|---|---|
| Hub 后端抽象与落地 | `harness.go`、`hub_backend.go`、`hub_run.go`、`backends/hermes/`、`opencode_http.go` | `HubBackend` 异步 Submit/Poll/Cancel 契约；Hermes（Runs API）与 OpenCode（sidecar HTTP）两类后端落地 |
| 可靠性地基 | `store/hub_handle.go`(新增 +172)、`dispatcher/executor.go`(+98)、`dispatcher/reattach_test.go` | `hub_handles` 表持久化 Handle + Executor 重启拾取 + `IdempotencyKey` 幂等去重 + stale scanner 排除 hub 任务 |
| deliver 出站扇出 | `internal/deliver/`(新增 +151/+127) | hub 返回的 `DeliverRequest` 以 JSON POST 出站，5xx/网络错误按 `max_retries` 退避 |
| workflow v2 重写 | `resolver.go`(±182)、`tasktype_table.go`(新增 +100)、`slash_command_test.go`(+212)、`context_rollback_test.go`(+179) | Event Resolver 重构、任务类型表、斜杠命令、WorkflowContext 回滚 |
| ingress 重构 | `internal/{webhook => ingress/gitea}/`、`intent.go`(新增 +63) | webhook 包改名迁入 `ingress/gitea`，新增意图识别 |
| 配置 | `config/schema.go`(+113)、`workspace_transport_test.go`(新增)、`normalize_backend_test.go` | 新增 `workspace_transport`、`hub.*` 配置段与归一化 |
| Session/记忆 | `store/memory.go`(+65) | `memories` 表（repo/issue 级 key/value）落地跨任务记忆 |
| Web UI | `Agents.vue`(+97)、`SystemConfig.vue`(+49)、`WorkflowDetail.vue`(+127) | hub backend 选择、deliver 配置、工作流详情 |
| 测试 | `mock_hub_test.go`(+282)、`mock_hub_scenarios_test.go`(+163)、`e2e-mock-hermes.go`/`e2e-deliver-sink.go` | fake remote hub + mock Hermes + deliver sink |

**关键判断**：当前 master（未发布）已为"远端 Hub"打下了**脚手架**——`hub_handles` 持久化、重启重接、幂等、deliver、Hermes/OpenCode 后端。这正是 `git_sync` 计划要叠加的基础层。两份关于 git_sync 的新文档属于 **Phase 2 之后的下一步方向**，尚未进入代码或 TASKS 清单。

---

## 三、git_sync 方案提取（文档来源）

文档存在**三段演进**，彼此并不完全一致：

1. `docs/20260805-Phase2-plan.md`：引入 Hub 后端 + 三种 transport（`shared_path`/`git_sync`/`mcp`）。
2. `docs/remote-hub-deployment-flow.md`（本次暂存新文档）：把三种 transport 并列描述为"可部署"，`git_sync` 标为"推荐默认"，含完整数据流、边界表、状态机。
3. `docs/20260814-hub-git-sync-plan.md`（本次暂存新文档）：**决策收敛——只保留 `git_sync`，删除 `shared_path` 与 `mcp`**。

> 文档与代码不一致点：`remote-hub-deployment-flow.md` 把 `git_sync`/`mcp` 写成"可用"，但代码中 `git_sync` 完全不存在、`mcp` 被校验拒绝，唯一可用的是 `shared_path`。阅读时须将这两份新文档视为**规划态**而非现状。

### 3.1 设计目标
- 让远端 Hub（Hermes / OpenCode / 任意未来 Hub）可部署在独立主机、容器或跨网络。
- Matea 保留 Gitea 写回权、工作流闸门、审计、会话管理。
- 新 Hub 接入成本低：只需实现 `HubBackend` + 声明支持 `git_sync`。

### 3.2 实现机制
- **核心原则**：Git 是唯一事实来源；Hub 是"提案者"，Matea 是"审批者"；最小权限。
- **`GitSyncInfo`**：只读 clone URL（含短期 token）、`DraftBranch = matea/hub-{taskID}`、`BaseBranch`/`BaseHEAD` 锚点、`CommitAuthor`、`RequiredFooter = matea-task-id:{taskID}`、`AllowHubPush`。
- **数据流**：`Prepare`(生成分支/锚点/只读 URL) → `Submit` → Hub clone/改/commit/push 到 draft 分支 → Matea `SyncBack`(`fetch` + **校验三要素** + apply) → `verify_commands` + `independent_checker` → `finalizeWriteChanges`(Matea 身份重提交 + 开 PR)。
- **校验三要素**：① 分支名 `matea/hub-{taskID}` 独占；② 起点 == `base_head`；③ commit footer 含 `matea-task-id:{taskID}`。任一不满足即 fail。
- **`WorkspaceTransport` 接口**（`Prepare`/`SyncBack`/`Cleanup`），`gitSyncTransport` 为唯一实现。
- **Session 解耦**：从"工作区路径"改为"git 分支 + 记忆"，复用现有 `memories` 表。
- **结构扩展**：`BackendResult.GitSync`、`HubHandle.DraftBranch/BaseHEAD`（在现有 `hub_handles` 表上加列）。

### 3.3 适用场景
- 远端/跨网络 Hub 自管沙箱编码（Hub 不碰 Matea 本地文件系统）。
- 高信任边界：即使 Hub 被攻破，Matea 的校验/门禁仍可阻止污染主分支。
- 对比：`shared_path` 仅适合同机 OpenCode 过渡；`mcp` 留给不允许 Hub 碰 Git 的高隔离场景（本方案决定删除）。

---

## 四、评估

### 4.1 技术架构契合度 —— 较好（扩展增量、低耦合）
- **复用已落地地基**：`GitSyncInfo`/`GitSyncResult`/`HubHandle` 扩展均为**增量字段**，建立在已合入的 `hub_handles` 持久化、幂等、`runViaHub` 之上，无需推翻重建。
- **校验设计稳健**：三要素 + 现有 `verify_commands`/`independent_checker`/`git.HasChanges()` 形成纵深，与"Hub 提案 / Matea 审批"原则一致。
- **Session 解耦合理**：贴合已存在的 `memories` 表，方向正确。

**但存在硬伤**：
- 计划要求**删除 `shared_path` 与 `mcp`，只留 `git_sync`**。当前代码中 `shared_path` 是**唯一可用的 hub 写 transport**，`git_sync` 尚未动工。若按 Phase 1(删)→Phase 2(建) 的顺序，会出现**"无可用 hub 写路径"的真空窗口**。
- **OpenCode backend 深度耦合 `shared_path`**：`opencode_http.go` 通过 `WorkDir`/`X-Opencode-Directory` 传本地路径（L107–200、L262、L288–296、L502）。删除 `shared_path` 等价于让 OpenCode 必须支持"git clone + 带短期 token push"，而计划仅以"同步改造 OpenCode 侧或移除 OpenCode backend"一笔带过——**这是整条链路中最未被验证的关键路径**，OpenCode sidecar 能否原生完成远程 clone/push 存疑。

### 4.2 团队协作流程 —— 约束低，但连续性风险高
- 实测为**单维护者**（两个署名同一邮箱），无多人评审闸，"大刀阔斧重构"在"早期无真实用户"前提下可被接受。
- 但 **bus-factor = 1**：大规模重构的回滚只能靠自己，必须依赖项目已有的强测试文化（mock_hub / e2e）兜底。计划已含测试计划，方向正确。

### 4.3 工程约束 —— 基本契合
- 单二进制 + SQLite + 少依赖（`modernc.org/sqlite`，无 CGO）。`git_sync` 复用现有 `sandbox/git` 的 git CLI 封装，**不引入重依赖**，符合项目刻意少依赖的取向。
- 凭据策略（短期只读 token / deploy key、环境变量注入、任务结束撤销）与现有"绝不提交 token"的安全基线一致。但**任务级 token 的签发与回收需要 Gitea Token API 自动化，当前代码尚无此能力**，是前置依赖。
- 跨网络要求 Hub 可达 Gitea，属部署拓扑问题，需在部署文档中明确。

### 4.4 实施路径清晰度 —— 部分清晰，存在时序缺陷
- **优点**：Phase 1–5 分周估算、明确文件清单、测试计划齐备。
- **缺陷 1（时序）**：Phase 1 先删唯一可用 transport，Phase 2 才建替代，**制造不可用窗口**。应改为**先建后删、共存过渡**。
- **缺陷 2（关键路径未决）**：OpenCode 的 git_sync 适配被轻描淡写，而它正是删除 `shared_path` 的前提。实施前必须给出明确裁决（改造 OpenCode 还是本阶段移除 OpenCode backend）。
- **缺陷 3（未入任务清单）**：`docs/TASKS.md` 中**无任何 git_sync 条目**（grep 为空），未落实项目"测试未过不勾选"的铁律。

### 4.5 验收标准可验证性 —— 较好，缺性能预算
- 单元（分支生成、三要素、SyncBack）+ 集成（fake hub 对抗：错分支/错起点/缺 footer/无改动）+ E2E（solve_issue→PR）映射清晰、可自动化，与三要素校验一一对应。
- **缺口**：未给 `fetch + verify + re-apply` 往返的**性能/延迟 SLO**。大仓或大量文件场景下，每写任务增加一次网络 fetch + 本地重放，可能显著拖慢；验收应纳入性能预算与压测场景。

### 4.6 回滚与兼容策略 —— 薄弱（最需补强）
- 计划**未显式给出回滚路径**。
- **破坏性配置变更**：删除 `WorkspaceTransportSharedPath`/`MCP` 常量并收紧 `IsWorkspaceTransportValid`，会使任何设置了 `workspace_transport` 的 `config.yaml` 失效（哪怕当前无真实用户，也应避免静默拒绝，给出明确校验报错）。
- **SQLite 迁移缺失**：`HubHandle` 加 `draft_branch`/`base_head` 列需 `ALTER TABLE`（现有 `hub_handles` 行无此列），计划未提迁移步骤与降级路径。
- **建议**：以"新增 git_sync 并与 shared_path 共存、shared_path 标记 deprecated 一个发布周期、下一周期再删"的方式实施，可获得天然回滚与兼容窗口。

---

## 五、结论性判断

**方向正确，地基已备，但当前形态更接近"架构决策记录(ADR)"而非"可立即开工的实施计划"。**

- ✅ 价值点：Git 唯一事实来源、Hub 提案 / Matea 审批、最小权限——信任模型清晰；且能复用已合入 master 的 Phase 2 脚手架，扩展为增量、低耦合。
- ⚠️ 三处实质缺陷使其不宜按原文直接执行：
  1. **删除唯一可用 transport 的时序**会制造不可用窗口；
  2. **OpenCode 的 git_sync 适配**是未验证的关键路径，被一笔带过；
  3. **缺少回滚/兼容策略**（breaking config + SQLite 迁移未规划）。
- 工程上**可行**，但需先把上述三点补齐为硬约束，再开工。

---

## 六、改进建议（落地清单）

1. **重排实施顺序**：先实现 `git_sync` + OpenCode 的 git_sync 适配，再进入"删除 shared_path/mcp"阶段；保留一段**共存期**。
2. **先裁决 OpenCode**：要么验证 OpenCode sidecar 能带短期 token 完成 clone/push（含失败降级 single-shot LLM），要么**本阶段直接移除 OpenCode backend**，仅保留 Hermes 走 git_sync。决策前不要动 `shared_path`。
3. **兼容与回滚**：`shared_path` 先标 deprecated（配置校验给明确警告而非静默拒绝），一个发布周期后再删；`HubHandle` 加列走 `ALTER TABLE` + 默认值并写迁移说明；提供配置回退示例。
4. **落入 TASKS.md**：把 Phase 1–5 拆成可勾选项，遵循项目"build → 测试 PASS → 勾选"的铁律。
5. **凭证自动化前置**：明确 Gitea 短期 token / deploy-key 的签发与回收实现（调用 Token API 还是预置 key），这是 git_sync 能跑起来的前置依赖，需先排期。
6. **验收补性能预算**：定义 `SyncBack` 往返延迟 SLO，并对大仓/多文件场景做压测用例。
7. **修文档矛盾**：CHANGELOG 的"Phase 2 未合入 master"已过时（PR #28 已合）；`remote-hub-deployment-flow.md` 须把未实现的 `git_sync`/`mcp` 显式标注为"规划态"。
8. **默认开基础 diff 审计**：计划把 diff 白名单列为"可选"，建议**默认校验 Hub 改动是否越出目标路径**，并将越权 diff 落库审计，防 Hub 越权改非目标文件。

---

## 附：验证用的关键证据

- `git diff --stat v0.11.4..HEAD`：86 提交 / 98 文件 / +10503 -779（代码）；文档 +4495 -121。
- `internal/config/schema.go` L311–324：`WorkspaceTransportSharedPath` 为唯一实现；`IsWorkspaceTransportValid` 拒绝 `mcp` 与未知值（L329–333）。
- `internal/agents/opencode_http.go` L107–200、L262、L288–296、L502：`WorkDir` / `X-Opencode-Directory` 本地路径耦合（shared_path）。
- `internal/store/hub_handle.go`：现有 `hub_handles` 表无 `draft_branch`/`base_head` 列。
- `grep -rln "git_sync"` 于 `internal/` 与 `main.go`：**无结果**（零实现）。
- `git shortlog -sne v0.11.4..HEAD`：仅 `jeeinn` 与 `一颗红心`（同邮箱 `thinkwei2012@gmail.com`）→ 单维护者。
- `docs/TASKS.md` 中 grep `git_sync`：**无条目**（未进入任务清单）。
