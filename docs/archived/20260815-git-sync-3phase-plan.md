# git_sync 落地调研与三阶段拆解（v3.1：A0 前置验证 + 共存窗口 + B2 拆细）

> 日期：2026-08-17（v3.1，基于用户 v3 评审微调）  
> 决策基调（用户拍板）：保留 `builtin` 内建完整性不动；hub-* 路径大胆重构；先打通关键路径、把可见难题调研清楚，其余逐步完善；拆为三阶段。  
> 凭据模型（v3 纠正、v3.1 沿用）：**凭据交给 Hub-* 持有并使用**——Matea 在 `Prepare` 签发限定前缀 read-write deploy key 注入 `GitSyncInfo` 交给 Hub；Hub 持凭据 clone/编辑/commit/push 草稿分支 `matea/hub-{taskID}`；Matea 只 fetch + 三要素校验 + 开 PR（审批）。绝不给 Hub admin token。  
> OpenCode 与 Hermes **路径对齐**：两者纯作"远端 Hub"对待，**OpenCode 未必同机 sidecar，也可能非同机部署**；差异只在运行位置，同一 Hub 自 push 契约。  
> 本轮评审微调（v3 → v3.1）：① 阶段 A 之前加 **A0 前置验证**（OpenCode git 能力 spike + deploy key API spike + 决策点）；② 阶段 A 内部 **保留 shared_path/git_sync 共存窗口**（A1-A4 不收紧 `IsWorkspaceTransportValid`，A5 双路径验证后才删）；③ **base_head 漂移默认 fail+告警**（不自动 rebase）；④ **CreatePR helper 提前提取**（A1）；⑤ **SQLite 迁移提前到 A2**；⑥ **B2 拆为 3 子项**。  
> 配套评估：[20260815-git-sync-evaluation.md](20260815-git-sync-evaluation.md)；任务落地：[TASKS.md](../TASKS.md) Phase 2.6（本文件与之同步修订）。

---

## 一、模型演化（v2 → v3 → v3.1）

- **v2（已推翻）**：错把"Hub 提案 / Matea 审批"改成"Matea 代 push、Hub 永不持写凭据"，并废除方案 A/B 二分。→ 偏离信任模型，把工作区归属与 git 操作强行拉回 Matea，反而复杂。
- **v3（用户纠正）**：回归"Hub 持凭据自 push 草稿分支、Matea fetch 审批开 PR"；OpenCode 与 Hermes 对齐同一契约；shared_path 干净删除、OpenCode 当 pilot。
- **v3.1（本轮评审微调）**：在 v3 基础上补齐"可落地性护栏"——
  - 加 **A0 前置验证**：OpenCode git 能力、deploy key API 两项 spike，先证伪再开工，避免阶段 A 在未知路径上硬扛；
  - **共存窗口**：A1-A4 不提前收紧 `IsWorkspaceTransportValid`，`shared_path` 与 `git_sync` 并存到双路径验证通过（A5 才删），消除"先删后建"的不可用窗口；
  - **提前提取 CreatePR helper**（A1）与 **提前写 SQLite 迁移**（A2），让 A3/B 测试不必绕开新列；
  - **B2 拆 3 子项**（schema / prepareWriteWorkspace / Hub 侧 LastHead 续作），避免低估 Session 解耦工作量；
  - **base_head 漂移默认 fail+告警**，阶段 A/B 不引入自动 rebase。

> 一句话：v3.1 = v3 的正确信任模型 + 一套"先证伪、留退路、早夯实"的工程护栏。

---

## 二、当前代码流的精确阻断点（基于源码，含 v3.1 更新）

### B1（关键路径）— hub 写任务当前不走 `runViaHub`
- `runner_write.go:150-154`：hub-opencode 写任务走 `CodingBackend.Run` 路径（**NOT runViaHub**），依赖 `X-Opencode-Directory` 本地目录（shared_path）。
- `hub_run.go` 的 `runViaHub` / `mapHubResult`（L268-304）**只处理读/回复**（→ `Result{Action:"comment"}`），对 `GiteaActions`、`ExternallyHandled` 一律忽略/告警，**完全没有写返回通道**；计划中新增的 `BackendResult.GitSync` 在代码中尚不存在。
- ⇒ 改造点：`runViaHub` 必须区分读/写；写任务返回 `GitSync` 后，由 `WorkspaceTransport.Approve` 完成 fetch + 三要素校验 + 开 PR。

### B2（OpenCode 接入 git_sync）— v3.1 强调：先做能力 spike ⚠️
- OpenCode 当前靠 `CodingBackend.Run` + shared_path 本地目录；`opencode_http.go:107-200,502` 的 `X-Opencode-Directory` 当前是"Matea 准备的本地目录"，git_sync 下语义变为"OpenCode 自己的工作区"，**需 spike 验证 OpenCode 能否在任意 `--dir` 下完成带 token 的 `git clone`/`commit`/`push`、是否支持外部 git 凭据注入**。
- **部署形态提醒**：OpenCode 未必同机，可能与 Hermes 一样是非同机部署；对齐的含义是"两者都按远端 Hub 的 GitSyncInfo 契约自 push"，不应硬编码 sidecar 假设。
- ⇒ **A0.1 spike 必须先于 A4**；若 OpenCode 不支持，按 A0.3 决策（移除 / 改 patch 回传 / 延期），**不让它阻塞整个 git_sync 落地**。

### B3（凭据签发/回收）— 交给 Hub 使用，Matea 管控生命周期
- Gitea `POST /api/v1/repos/{owner}/{repo}/keys` 可创建 deploy key（`read_only` 布尔），可程序化创建与回收。
- **关键限制**：Gitea 部署密钥是 **repo 级**（read-only / read-write），**无"按分支"粒度**。"限定分支前缀写"无法在凭据层强制，必须由 Matea 三要素校验（分支名 `matea/hub-{taskID}` + `hub_handles` 所有权登记）在应用层强制。
- v3.1 模型：Matea 为每任务签发 **read-write deploy key**（任务级、随 `hub_handle` 生命周期撤销），注入 `GitSyncInfo` 交给 Hub；Hub 用其 clone + push 草稿分支；**绝不给 Hub admin token**。
- ⇒ **A0.2 spike 先于 A6**：确认本机 Gitea 版本支持、响应格式、权限要求、key title 格式（`matea-hub-task-{taskID}`）、回收失败重试/告警策略。当前代码**无调用 Gitea Key API 的能力**，这是阶段 A 的前置依赖。

### B4（审批/开 PR）— 提前提取 CreatePR helper ✅
- `write_workspace.go` 的 `finalizeWriteChanges` 内含"分支已提交未 push → 补 push + 开 PR"，其中开 PR 逻辑是 `runner_write.go:267` 的**未导出** `finalizeWriteTaskPR`。git_sync 下提交/推送已由 Hub 完成，Matea 的 `Approve` 只需 fetch + 三要素校验 + 开 PR。
- ⇒ **A1 把 `finalizeWriteTaskPR` 提取为 `internal/gitea`（或 `internal/agents`）内的 exported helper**，处理"PR 已存在则更新评论"，供 `Approve` 复用；builtin 路径仍调用同一 helper，零侵入。
- 复杂度集中在三要素校验 + PR 创建；原 finalize 的 re-commit/re-push 逻辑在 hub 路径不再需要。

### B5（三要素校验 + base_head 漂移）— 漂移默认 fail+告警
- "起点 == base_head" 在并发合入其他 PR 时会漂移。v3.1 明确：**阶段 A/B 默认策略 = fail + 告警，不自动 rebase**（避免在审批侧引入自动 rebase 的复杂性与风险）；rebase 策略留待后续评估。

### B6（Session 解耦）— 拆 3 子项，工作量不低估
- `agent_sessions.WorkspacePath` 在 git_sync 模式下要置空、改 `Branch`/`LastHead`；`prepareWriteWorkspace` 的 session 续作逻辑（`write_workspace.go:76-84,118-139`）强依赖 `WorkspacePath`。
- v3.1 拆为：**B2.1** schema 迁移（`WorkspacePath`→`Branch`+`LastHead`+`Memory`）；**B2.2** `prepareWriteWorkspace` session 分支改造（基于 `LastHead` 起新草稿分支）；**B2.3** Hub 侧 LastHead 续作契约与测试（OpenCode/Hermes 下次任务基于 `LastHead` 起新分支，并注入 `memories` 表记忆到 prompt）。

### B2'（shared_path 真实依赖面 — 共存窗口的依据）
- `shared_path` 实际被以下代码引用，删除必须**全量替换**：
  - `internal/config/schema.go`：常量 `WorkspaceTransportSharedPath` + `WorkspaceTransportMCP` + `ValidWorkspaceTransports()` + 注释。
  - `internal/config/config.go`：`ApplyBackendDefaults` 默认 shared_path（L306-309）；`ValidateBackendWorkspaceTransport` 只允许 shared_path（L320-324）。
  - `internal/config/workspace_transport_test.go`：大量断言 shared_path/mcp 合法（L10-118）——**A1-A4 共存期先改为兼容两 transport，A5 才收敛**。
  - `internal/agents/opencode_hubbackend_test.go`：`WorkspaceTransport: config.WorkspaceTransportSharedPath`（L535, L564）。
  - `internal/agents/backends/hermes/e2e_test.go`：`WorkspaceTransport: config.WorkspaceTransportSharedPath`（L129）——证明 Hermes 写路径同样依赖 shared_path。
  - `internal/agents/runner_write.go`：`CodingBackend.Run` 的 hub-opencode 写路径（L150-210）依赖 shared_path 工作区。
- `builtin` 写任务（`CodingBackend.Run` 本地 AgentLoop + `finalizeWriteChanges`）**完全不碰 `WorkspaceTransport`**，保底不变。

---

## 三、builtin 不受影响的设计边界
- `builtin` 写任务全程不碰 `WorkspaceTransport`，git_sync 改造零侵入。
- 即便 hub 路径在阶段 A/B 引入回归，builtin 仍可作为保底写路径（项目"明确不做"第 5 条已固化：不强制用户先装 Hub）。

---

## 四、开源社区解法（已验证）
| 难点 | 社区现状 | 结论 |
|---|---|---|
| Agent 改代码后推分支开 PR 的模型 | OpenHands / Copilot coding agent / Devin 均为"隔离沙箱 Agent 改代码 → 推 draft 分支 → 开 PR → 人工/编排器合并"。 | git_sync 的"Hub 提案 / Matea 审批"是行业主流范式，无概念风险。 |
| 最小权限 git 凭据 | Gitea Deploy Keys API 可程序化创建/回收 repo 级 read-only/read-write 密钥。 | 任务级凭据可落地；限制是 repo 级无 per-branch 粒度，分支限制须 Matea 侧三要素强制。v3.1 回归"Matea 签发限定前缀 read-write deploy key 交给 Hub 使用"，A0.2 先 spike 证伪。 |
| OpenCode 接入 git_sync | OpenCode 可在任意 `--dir` 工作、原生 git；但是否支持外部 git 凭据注入 / 非同机自 push 待证。 | v3.1：**A0.1 先做 OpenCode git 能力 spike**；通过则当 pilot，失败则按 A0.3 让 Hermes 当 pilot 或移除 OpenCode backend，不硬扛。 |

---

## 五、统一 git_sync 模型定义（v3.1 核心）
```
                  ┌─────────────────────┐
  hub submit  ──► │ WorkspaceTransport   │
  (OpenCode/      │                     │  ① Prepare：
   Hermes,        │  Prepare            │     - 签发 deploy key(read-write, 限定前缀)
   位置透明)       │  Approve            │     - 生成分支 matea/hub-{taskID}
                  │  Cleanup            │     - 记录 base_head 锚点
                  └─────────┬───────────┘     - 构造 GitSyncInfo(clone_url+凭据+draft_branch)
                            │ GitSyncInfo
                            ▼
                  ┌─────────────────────┐
                  │ Hub（位置透明）       │  clone → 编辑 → commit → push 草稿分支
                  │ OpenCode / Hermes   │  （持凭据，自 push；差异仅运行位置）
                  └─────────┬───────────┘
                            │ GitSyncResult{DraftBranch, DraftHEAD}
                  ┌─────────▼───────────┐
                  │ WorkspaceTransport   │  ② Approve：
                  │  Approve            │     - fetch 草稿分支
                  │                     │     - 三要素校验(分支独占/起点锚定/footer)
                  │                     │     - 复用 CreatePR helper 开 PR（不代 push、不重提交）
                  └─────────┬───────────┘
                            │
                  ┌─────────▼───────────┐
                  │ Cleanup             │  ③ 撤销 deploy key
                  └─────────────────────┘
```
- **`WorkspaceTransport` 接口**：`Prepare`（签发凭据 + 生成分支 + 构造 `GitSyncInfo`）、`Approve`（fetch + 三要素校验 + 开 PR，复用 exported CreatePR helper）、`Cleanup`（撤销凭据）。
- **`GitSyncInfo`**：`CloneURL`(含凭据)、`DraftBranch`、`BaseBranch`、`BaseHEAD`、`CommitAuthor`、`RequiredFooter`、`HubPush:true`。
- **`GitSyncResult`**：`DraftBranch`、`DraftHEAD`（由 Hub 在 push 后回填）。
- **Hub 位置透明**：OpenCode 与 Hermes 同契约，均"持凭据自 push 草稿分支"；是否为同机 sidecar 不影响 Matea 侧逻辑。
- **三要素校验**：分支名独占（`hub_handles` 登记）+ 起点锚定（`BaseHEAD`，漂移默认 fail+告警）+ footer `matea-task-id` 签名。
- **共存窗口**：A1-A4 期间 `IsWorkspaceTransportValid` 同时接受 `shared_path` 与 `git_sync`；A5（gating: B1 验收后）才收敛为仅 `git_sync`。

---

## 六、三阶段拆解（v3.1 可落地执行）
> 原则：builtin 不动；git_sync 为 hub 写唯一目标 transport；OpenCode 与 Hermes 对齐同一 Hub 自 push 契约；凭据由 Hub 持有、Matea 签发与撤销；A0 先证伪；共存窗口保底；OpenCode 当 pilot（A0.1 通过前提下）。

### 阶段 A0：前置验证（spike，不写业务代码）
- **A0.1 OpenCode git 能力 spike（~2 天）**
  验证：HTTP API 把 `clone_url`+deploy key 传给 OpenCode；OpenCode 在 `X-Opencode-Directory` 能否 `git clone <带token URL>` / `git commit` / `git push` 草稿分支；是否支持外部 git 凭据注入。
  回退预案：失败 → A0.3 决策（移除 OpenCode backend / 改走 patch 回传 / 延期）；**不让 OpenCode 适配失败阻塞整个 git_sync 落地**。
- **A0.2 Gitea deploy key API spike（~1 天）**
  admin token 调 `POST /api/v1/repos/{owner}/{repo}/keys` 创建、调删除接口回收；确认本机 Gitea 版本支持、响应格式、权限要求；key title 格式 `matea-hub-task-{taskID}`；定义回收失败重试/告警策略。
- **A0.3 决策点：OpenCode 是否作为阶段 A pilot**
  A0.1 通过 → OpenCode 当 pilot；A0.1 失败 → **让 Hermes 当 pilot**，OpenCode backend 本阶段移除或延后，不阻塞。

### 阶段 A：git_sync 关键路径打通（OpenCode pilot + 删 shared_path，含共存窗口）
目标：在 OpenCode 写链路上让 git_sync 从 Submit 到出 PR 全程跑通（OpenCode 持凭据自 push）；**shared_path 在 A5（gating: B1 验收后）才删**，A1-A4 保留共存窗口。

- **A1** 抽象 `WorkspaceTransport` 接口（`Prepare`/`Approve`/`Cleanup`）+ `gitSyncTransport` 实现 + **提前提取 CreatePR helper**。
  `Prepare`=签发 deploy key + 生成分支 + 构造 `GitSyncInfo`；`Approve`=fetch + 三要素校验 + 开 PR；`Cleanup`=撤销凭据。定义 `GitSyncInfo`/`GitSyncResult`/三要素。
  **共存窗口**：新增 `WorkspaceTransportGitSync` 常量，`IsWorkspaceTransportValid` **暂时同时接受 `shared_path` 与 `git_sync`**（不提前收紧）。
  **提前提取**：把 `runner_write.go:267` 未导出 `finalizeWriteTaskPR` 提取为 `internal/gitea`（或 `internal/agents`）内 exported helper，处理"PR 已存在则更新评论"，供 `Approve` 与 builtin 复用。
- **A2** 增量扩展 `TaskContext`/`BackendResult`/`HubHandle` + **提前写 SQLite 迁移**。
  加 `GitSyncInfo`/`GitSyncResult`/`DraftBranch`/`BaseHEAD`；新增 `BackendResult.GitSync` 字段；向后兼容；**`hub_handles` 加列走 `ALTER` + 默认值迁移在此阶段落地（不拖到 C3，避免 A/B 测试绕开新列）**。
- **A3** 改造 `runViaHub` 区分读/写：写任务 `mapHubResult` 返回 `GitSync` → 由 `WorkspaceTransport.Approve` 复用 A1 提取的 exported CreatePR helper 完成 fetch + 三要素校验 + 开 PR（提交/推送已由 Hub 完成，不再走 `finalizeWriteChanges` 的 commit/push 段）。
- **A4** OpenCode 写路径从 `CodingBackend.Run`(shared_path) 切到 `runViaHub` 写通道（**前提：A0.1 通过**）：`Prepare` 签发 deploy key 并注入 `GitSyncInfo`；OpenCode 在 `X-Opencode-Directory` 工作区 **clone → 编辑 → commit → push 草稿分支**（持凭据自 push，不走 patch 回传），回传 `GitSyncResult{DraftBranch, DraftHEAD}`。
- **A5 干净删除 `shared_path`（gating：B1 验收后才执行）**：删 `CodingBackend.Run` 中 shared_path 依赖 + transport 常量；此时才把 `IsWorkspaceTransportValid`/`ValidWorkspaceTransports` 收敛为只接受 `git_sync`；重写 `workspace_transport_test.go` 与 `opencode_hubbackend_test.go`/`hermes/e2e_test.go`（这些测试在 A1-A4 共存期已先改为兼容两 transport）。
- **A6** Gitea deploy key 程序化创建/回收（基于 A0.2）：repo 级 read-write（限定前缀由三要素应用层强制）；凭据注入 `GitSyncInfo`，随 `hub_handle` 删除/超时撤销；回收失败按 A0.2 策略重试/告警；**绝不把 Matea admin token 交给 Hub**。
- **A7** 测试：fake OpenCode + fake Gitea；对抗（错分支/错起点/缺 footer/无改动/假完成）全拒；单测（分支生成/三要素/`Approve`）；E2E（OpenCode → solve_issue → Hub 自 push → PR）。builtin 全量不受影响。
- **A8 阶段 A 验收**：一条 OpenCode 写任务经 git_sync 端到端出 PR（OpenCode 自 push、Matea 开 PR）；`go test ./...` 与 builtin 全量用例 PASS；shared_path 仍保留（A5 后置），git_sync 路径可运行。

### 阶段 B：Hermes 远端接入 + Session/安全收口
- **B1** Hermes backend 对齐 OpenCode 同一 Hub 自 push 契约：`buildRunRequest` 注入 `GitSyncInfo`（凭据 + clone_url + draft_branch）；Hermes 远端 clone/编辑/commit/push 草稿分支、回传 `GitSyncResult`；走 `runViaHub` 写通道、由 `Approve` 收口（无 patch 回传特例，路径已对齐）。**B1 验收后触发 A5 删除 shared_path。**
- **B2.1** `agent_sessions` schema 迁移：`WorkspacePath` → `Branch`+`LastHead`+`Memory`（DDL + 迁移 + 默认值）。
- **B2.2** `prepareWriteWorkspace` session 分支改造：续作逻辑从依赖 `WorkspacePath` 改为基于 `LastHead` 起新草稿分支（`write_workspace.go:76-84,118-139`）。
- **B2.3** Hub 侧 LastHead 续作契约与测试：OpenCode/Hermes 下次任务如何基于 `LastHead` 起新草稿分支；确保 `memories` 表在续作时注入 prompt。
- **B3** diff 白名单（`allowed`/`denied_paths`）**默认开基础校验**；越权 diff 落库审计。
- **B4** 凭证最小权限复核 + 生命周期 hook（deploy key 随 `hub_handle` 删除/失效回收，与 A6 衔接）。
- **B5** E2E（真实 OpenCode + 真实 Gitea 沙箱）+ 对抗测试强化：验证 OpenCode 与 Hermes **两条对齐的** git_sync 路径；Hub 越权分支全拒并标记失败；**base_head 漂移默认 fail + 告警（不自动 rebase）**。

### 阶段 C：清理、文档与发布
- **C1** 删除 `mcp` transport（**仅** workspace transport 用途）；保留 `internal/agent/tools_mcp.go` 的 agent MCP tools 能力，手术式删除；并收敛 `IsWorkspaceTransportValid`/`ValidWorkspaceTransports` 的不一致（前者已拒 `mcp`、后者仍列它）。
- **C2** 文档收尾：`HUB-BACKENDS.md`（信任模型/权限模板/接入契约，明确"Hub 持凭据自 push、Matea 审批开 PR"）；修 CHANGELOG（"Phase 2 未合入 master"已过时，PR #28 已合）；`remote-hub-deployment-flow.md` 标注 git_sync 为当前唯一 transport、mcp/shared_path 已删。
- **C3** SQLite 迁移收尾校验：确认 `hub_handles` 加列迁移在 A2 已落地；此处仅补旧 `config.yaml` 迁移示例（删 transport 后）。
- **C4** 性能预算：`Approve` 往返延迟 SLO + 大仓/多文件场景压测用例。
- **C5** 阶段 C 验收 + 发版：配置仅 `git_sync` 一种 transport；全量测试 PASS；文档齐备；发版（新 tag）。

---

## 七、与 v1 / v2 / v3 / 评估的对照
| 来源 | 处理 |
|---|---|
| OpenCode 是否走 git_sync | v1 方案 A/B 二分后置；v2 统一路径当 pilot；**v3/v3.1 与 Hermes 对齐同一 Hub 自 push 契约（位置透明）** |
| 凭据归属 | v1 方案 B 给 Hub；v2 Matea 代 push；**v3/v3.1 交给 Hub-* 持有并使用（Matea 签发限定前缀 read-write deploy key）** |
| Hub 是否自 push | v1 方案 B 是；v2 否；**v3/v3.1 是（提案=push 草稿分支）** |
| Matea 角色 | v1 审批+重提交；v2 代提交+推送+开 PR；**v3/v3.1 审批：fetch + 三要素校验 + 开 PR（不代 push）** |
| 删除 shared_path | v1 deprecated 共存；v2/v3 同分支干净删；**v3.1 加共存窗口：A1-A4 不收紧、A5 双路径验证后删** |
| 前置验证 | 无；**v3.1 加 A0（OpenCode git spike + deploy key API spike + 决策点）** |
| SQLite 迁移 | 拖到 C3；**v3.1 提前到 A2** |
| CreatePR helper | 隐含；**v3.1 提前到 A1 提取为 exported** |
| Session 解耦 | B2 单 checkbox；**v3.1 拆 B2.1/B2.2/B2.3** |
| base_head 漂移 | 待定义；**v3.1 默认 fail+告警，不自动 rebase** |

---

## 八、结论性判断
- **方向正确、地基已备**：Phase 2 异步契约、`hub_handles` 持久化/重接、OpenCode HTTP backend 都已落地。v3.1 在 v3 正确信任模型上，补了一套"先证伪、留退路、早夯实"的工程护栏，可落地性显著提升。
- **最大风险已被护栏覆盖**：OpenCode git 适配能力（P0）由 A0.1 spike 先证伪，失败则 Hermes 当 pilot（A0.3），不阻塞；阶段 A 内部不可用窗口（P0）由共存窗口消除（A1-A4 不收紧校验，A5 双路径后删）。
- **阶段 A 仍是最硬骨头**：OpenCode 写路径切 `runViaHub` + deploy key 签发注入 + 共存期测试改造，是工作量与风险集中处；但 A0 已把未知变量前置排除。
- **仍待验证的真风险**：base_head 漂移（已定 fail+告警）、Session 解耦（已拆细）、远端 Hermes push 正确性（B1）——在 B 阶段收口，不在 A 阻塞。
- **下一步建议**：从 **A0.1 + A0.2**（两项 spike）开工，拿到结论后再进 A1（接口 + CreatePR helper 提取 + 共存窗口）。
