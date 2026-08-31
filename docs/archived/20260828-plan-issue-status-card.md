# Plan: Issue 进度评论去噪 —— 状态卡方案

> 日期：2026-08-28
> 状态：调研定稿（本次不落地代码）
> 样本：生产实例 `http://182.92.129.124:3000/jeeinn/rust-study/issues/5`（Gitea **1.27.2**，已实测 API 验证）

---

## 0. TL;DR

| 问题 | 结论 |
|------|------|
| **Q1** 「jeeinn 引用合并请求 将关闭此工单」怎么来的？ | **Gitea 原生系统事件**，不是 Matea 发的评论。Matea 建 PR 时无条件追加 `Fixes #N`（`write_pr.go:42-46`），且 PR 用 **admin token（jeeinn）** 创建 → 事件署名 jeeinn。「将关闭」是**将来时预告**；issue 真被关闭是 PR 合并时 Gitea 的 auto-close 触发（`write_pr.go` 的 `Fixes` 联动），全程无需手动处理。 |
| **Q2** 「已开始处理」重复成噪音，怎么办？ | 终态编辑方案**不可靠**（本例 task #13 卡住 → 编辑永不发生，噪音永久残留，实证见 §2.1）。推荐 **方案 B：任务启动即建状态卡，全程 PATCH 同一张卡**，1 任务 = 1 评论。 |
| **能否创建/编辑？** | ✅ 能。Gitea 1.27.2 已确认支持 `PATCH /repos/{o}/{r}/issues/comments/{id}`（编辑）、`DELETE`（删除）、`POST .../issues/{index}/comments`（创建）。状态卡本质是 markdown 评论，创建无障碍。 |

---

## 1. 问题一：为什么会出现「引用合并请求 将关闭此工单」

### 1.1 实测证据（生产实例 API）

issue #5 时间线（17 个事件）关键片段：

```
comment          | analyze001     | 2026-08-25 16:45:23   # 🔄 已开始处理（task #2）
comment          | analyze001     | 2026-08-25 16:47:45   # 🤖 AI 分析正文
comment          | analyze001     | 2026-08-25 16:47:45   # ✅ 分析完成（task #2）
comment          | jeeinn         | 2026-08-25 16:53:23   # 用户：去开发 @coder007
comment          | coder007       | 2026-08-26 16:07:29   # ❌ 任务执行失败（context deadline）
comment          | jeeinn         | 2026-08-26 18:31:04   # 用户：去开发 @code-opencode
comment          | code-opencode  | 2026-08-27 13:27:56   # 🔄 已开始处理（task #12）
pull_ref         | jeeinn         | 2026-08-27 13:31:59   # ← 问题中的事件
comment          | code-opencode  | 2026-08-27 13:31:59   # ✅ PR created: .../pulls/6
comment          | code-opencode  | 2026-08-27 13:31:59   # ✅ PR 已创建：...
assignees        | jeeinn         | 2026-08-28 12:24:17
comment          | code-review    | 2026-08-28 12:25:16   # 🔄 已开始处理（task #13）
close            | jeeinn         | 2026-08-28 13:41:55   # ← issue 被关闭
```

其他实测数据：

- PR #6：`author = jeeinn`，`merged = True`，`merged_at = 2026-08-28 13:41:55`，body 末尾实测为 `*Task ID: 12*\n\nFixes #5`
- issue #5：`state = closed`，`closed_at = 2026-08-28 13:41:55`（**与 PR merged_at 完全同刻**）

### 1.2 因果链

```
write_pr.go:42-46   Matea 建 PR 时无条件追加 "\n\nFixes #%d"（只要 task.IssueID > 0）
        ↓
write_pr.go:20      用 adminClient（jeeinn 的 token）创建 PR → PR author = jeeinn
        ↓
Gitea 原生          PR body 含 "Fixes #5" → issue 时间线生成 pull_ref 系统事件
                    UI 文案：「jeeinn 引用合并请求 将关闭此工单」（将来时 = 预告）
        ↓
（任意人合并 PR #6，08-28 13:41:55）
        ↓
Gitea 原生          merged 时按 "Fixes #5" 自动 close issue #5（无需任何人点「关闭工单」）
```

### 1.3 结论与可选调整

**这不是 bug，是 Matea 主动选择 + Gitea 原生联动的叠加结果。** 三点澄清：

1. 「引用合并请求 将关闭此工单」**不是评论**（不在 10 条 comment 列表里），是 Gitea 的 `pull_ref` 系统事件，PR 创建瞬间生成。
2. 署名 **jeeinn** 是因为 Matea 用 admin token 建 PR（`write_pr.go:20` 的 `adminClient`），Gitea 按 PR 作者归属事件。
3. 「将关闭」是将来时**预告**；issue 真正被关闭发生在 PR 合并时刻——用户「没手动处理」正是因为这条链路全自动。

**若想去掉这个自动联动**，可选项（需你拍板，本次不改）：

| 选项 | 做法 | 影响 |
|------|------|------|
| 保持现状 | `Fixes #N` | PR-issue 强关联，合并即自动关闭（当前行为） |
| **降级为仅引用 ✅ 已选** | 改为 `Refs #N` | 仍有关联与时间线记录，**不再自动关闭**，需人工关闭 |
| 完全不追加 | 去掉 `issueLink` | 无关联，issue 需人工关闭 |
| 可配置 | 新增 `pr.issue_link_mode: fixes\|ref\|none` | 最灵活（可选做，非必须） |

#### `Refs #N` 的确切行为（Gitea 官方文档确认）

Gitea 默认关键词（`docs.gitea.com` Configuration Cheat Sheet）：

```
CLOSE_KEYWORDS  = close, closes, closed, fix, fixes, fixed, resolve, resolves, resolved
REOPEN_KEYWORDS = reopen, reopens, reopened
```

**`refs` 不在 CLOSE_KEYWORDS 中**，因此降级后：

| 行为 | `Fixes #N` | `Refs #N` |
|------|-----------|----------|
| 时间线生成 cross-reference 关联 | ✅ | ✅（仅显示「引用了此工单」） |
| 显示「将关闭此工单」预告 | ✅ | ❌ |
| PR 合并时自动关闭 issue | ✅ | ❌ |
| issue 关闭方式 | 自动 | **需人工关闭** |

**结论：降级为 `Refs #N` 后，PR 关闭（合并）不会关联关闭 issue，需要人工关闭。** 这正是期望行为——把「关闭工单」的决定权交还给人，而不是由一次 PR 合并自动决定。

> 补充：文档还说明「可操作引用」被接受需满足其一——评论者有关闭权限 / 引用在提交消息中 / 引用作为 PR 描述的一部分（此情况下需合并者也有权限）。当前 Matea 用 admin token 建 PR 且 jeeinn 合并，两者都有权限，所以自动关闭生效。改用 `Refs` 后该判断不再触发。

---

## 2. 问题二：「已开始处理」评论噪音

### 2.1 实测现状（issue #5）

「🔄 已开始处理」共 **3 条**：

| comment ID | Agent | Task | 时间 | 终态 |
|-----------|-------|------|------|------|
| 157 | analyze001 | #2 | 08-25 16:45 | ✅（159 分析完成） |
| 181 | code-opencode | #12 | 08-27 13:27 | ✅（185 PR 已创建） |
| 188 | code-review | #13 | 08-28 12:25 | **❌ 至今无终态**（已运行 >2h） |

即：**每个任务 = 「开始」+「终态」至少 2 条评论**，多轮迭代下线性堆积。

> ⚠️ **task #13 是本次调研最关键的实证**：它 08-28 12:25 开始后一直未产生终态评论（今天 14:47 仍未结束）。这直接否定了「终态时再编辑」的思路——**任务永远到不了终态时，编辑永远不会发生，那条「已开始处理」就是永久噪音。**

### 2.2 能力调研：能否创建 / 编辑？

实测生产实例 swagger（`http://182.92.129.124:3000/swagger.v1.json`，version **1.27.2**）：

```
/repos/{owner}/{repo}/issues/{index}/comments        -> [GET, POST]          创建 ✅
/repos/{owner}/{repo}/issues/{index}/comments/{id}   -> [DELETE, PATCH]      编辑 ✅
/repos/{owner}/{repo}/issues/comments/{id}           -> [GET, DELETE, PATCH] 编辑 ✅
```

**结论：能创建，也能编辑。状态卡方案在 Gitea 侧无障碍。**

补充：Gitea 默认 markdown 渲染会 sanitize raw HTML，**状态卡应使用纯 markdown（emoji / 表格 / 引用块），不要依赖 `<details>`、`<sub>` 等 HTML 标签**。

### 2.3 Matea 现状缺口

| 项 | 现状 | 位置 |
|---|------|------|
| Gitea 客户端 | 只有 `IssueComment`（创建，**丢弃响应体**）、`IssueComments`（列表）、`PRComment`；**无编辑 / 删除方法** | `internal/gitea/issue.go:9`、`pr.go:74/164` |
| 评论 ID | `postGateComment` **不保存**返回的 comment ID | `internal/dispatcher/comments.go:14-28` |
| Task 结构 | **无**存 comment ID 的字段 | `internal/store/task.go:21-41` |
| 评论标记 | `FormatAgentComment` 前缀 `<!-- matea-agent -->`，`IsAgentComment` 按前缀识别（防 webhook 自触发） | `internal/workflow/gate_l1.go:89-96` |
| 终态评论 | L3 模板（`policy.go:323/325`）+ 失败评论（`executor.go:835`） | — |

> ⚠️ 状态卡 **必须以 `<!-- matea-agent -->` 开头**（由 `FormatAgentComment` 保证），否则会被 `IsAgentComment` 判为用户评论，进而触发新的 webhook 任务（自触发风暴）。

### 2.4 方案对比

#### 方案 A：终态时把「已开始处理」编辑为状态卡 ❌ **不推荐**

启动时照旧发「已开始处理」，任务终态时定位该评论并 PATCH 成状态卡。

| 维度 | 评价 |
|------|------|
| 可靠性 | ❌ **不可靠**。task #13 实证：任务卡住/超时/重启未达终态 → 编辑永不发生 → 噪音永久残留 |
| 中间态 | ❌ 长任务运行期间（数十分钟）页面上始终是「已开始处理」噪音 |
| ID 定位 | ⚠️ 同样需要存 ID 或按 marker 反查，与方案 B 改动量相当 |
| 收益 | 仅「终态达成时」减少一条评论 |

**结论：方案 A 把可见性押在「任务一定能跑到终态」上，而失败/超时/卡死恰恰最需要状态可见——这是方向性错误。**

#### 方案 B：任务启动即建状态卡，全程 PATCH 同一张卡 ✅ **推荐**

任务启动时创建一张带唯一 marker 的状态卡，后续所有状态变化（进度 / 完成 / 失败）**PATCH 同一张卡**。

| 维度 | 评价 |
|------|------|
| 噪音 | ✅ 1 任务 = 1 条评论，天然不堆积 |
| 卡死场景 | ✅ 任务卡住也只留一张「🔄 处理中」卡，且可带已运行时长，信息完整 |
| 失败场景 | ✅ 失败即把该卡 PATCH 成 ❌ 卡片，无需新建 |
| 改动量 | 中等（见 §2.6），与方案 A 接近 |

### 2.5 方案 B 设计

#### 生命周期

```
任务入队（pipeline.go:243 处，原「已开始处理」）
  └─ 创建状态卡（POST）→ 解析响应拿 comment ID → 存 task.status_comment_id
        ↓
运行中（可选：心跳/阶段变化时）
  └─ PATCH 同一张卡（更新运行时长、当前阶段）
        ↓
终态（成功：L3 policy / 失败：executor.go:835）
  └─ PATCH 同一张卡为 ✅ / ❌ 终态卡片（不再新建评论）
```

#### 卡片内容（纯 markdown 示例）

处理中：

```markdown
<!-- matea-agent -->
<!-- matea-status-card:task-13 -->

## 🤖 code-review · 代码审查

| | |
|---|---|
| **状态** | 🔄 处理中 |
| **任务** | #13 · 已运行 3m12s |
| **触发** | @code-review |

> Matea 状态卡 · 随任务进展自动更新
```

完成 / 失败（同卡 PATCH）：

```markdown
| **状态** | ✅ 完成 · 耗时 4m20s |      ← 成功
| **状态** | ❌ 失败 · context deadline exceeded |  ← 失败
```

#### ID 定位策略（双保险）

1. **主**：创建时解析响应取 `id`，持久化到 `task.status_comment_id`。
2. **兜底**：ID 缺失（旧任务 / 重启恢复 / 建卡失败重试）时，调 `IssueComments` 列表，按 marker `<!-- matea-status-card:task-{ID} -->` 反查定位 → 保证 Matea 重启 re-attach 后仍能更新同一张卡，不产生重复卡片。

> 注意：marker 必须放在 `FormatAgentComment` 之后（即第二行），保证 `IsAgentComment` 前缀判断仍为 true。

### 2.6 改动清单（落地时）

| # | 文件 | 改动 |
|---|------|------|
| 1 | `internal/gitea/issue.go` | 新增 `EditIssueComment(owner, repo string, commentID int, body string) error`（`PATCH /repos/{o}/{r}/issues/comments/{id}`）；可选 `DeleteIssueComment` |
| 2 | `internal/gitea/issue.go` | 让创建评论能返回 ID：新增 `CreateIssueComment(...) (*IssueComment, error)`（保留旧 `IssueComment` 签名以兼容既有调用点，内部复用） |
| 3 | `internal/store/task.go` + 迁移 | `Task` 新增 `StatusCommentID int64`；迁移在 `store.Open` 自动执行（项目既有机制） |
| 4 | `internal/dispatcher/pipeline.go:243` | 「已开始处理」改为 **建状态卡 + 存 ID**（复用 `postGateComment` 通道或新 `postStatusCard`） |
| 5 | `internal/workflow/policy.go:323/325`、`internal/dispatcher/executor.go:835` | 终态/失败改为 **PATCH 该卡**，而不是新建评论 |
| 6 | 兜底查找 | 新增 `findStatusCard(owner, repo, issueID, taskID)`：用 `IssueComments` 按 marker 反查 |
| 7 | 状态卡模板 | 新增渲染函数（纯 markdown，状态/耗时/agent/任务ID），保证以 `<!-- matea-agent -->` 开头 |

### 2.7 风险与注意事项

- **权限**：编辑评论需与创建者一致（或用 admin）。当前评论由**各 agent 自己的 token** 发出，建卡与编辑应**使用同一 agent token**，避免 agent A 建卡、agent B 编辑导致 403。
- **并发**：同一 task 只更新自己的卡；不同 task 各一张卡，互不干扰。
- **卡片数量**：多任务并存时仍会有 N 张卡（每任务一张），但比现状「每任务 2 条」少一半，且终态卡片信息完整自洽。
- **进阶（可选，不在本次范围）**：若希望**整个 issue 只有一张卡**（跨任务共享、覆盖式更新），需按 issue 而非 task 做幂等；状态合并逻辑更复杂（多任务并行时谁覆盖谁），建议作为后续议题。
- **旧评论**：无 marker 的历史评论不受影响，也不会被误编辑。

---

## 3. 附带发现（P2，未列入本次改动）

1. **PR 链接写死 `localhost`**：comment 184 / 185 中 PR 链接为 `http://localhost:3000/jeeinn/rust-study/pulls/6`，生产环境不可点击。`write_pr.go:62` 注释已说明要用 `#N` 原生引用规避 ROOT_URL 问题，但 hub 返回的 `agentResult` 正文里仍带完整 URL（hub 侧拼接）。建议后续统一为 `#N` 相对引用。
2. **issue 已关闭但任务仍在跑**：issue #5 于 08-28 13:41:55 被 PR 合并自动关闭，而 task #13（code-review）12:25 启动后至今无终态。建议在 issue 关闭事件上做一次「在跑任务」的收敛/提示（避免关单后仍产出评论）。

---

## 4. 附带发现（P2，未列入本次改动）

1. **PR 链接写死 `localhost`**：comment 184 / 185 中 PR 链接为 `http://localhost:3000/jeeinn/rust-study/pulls/6`，生产环境不可点击。`write_pr.go:62` 注释已说明要用 `#N` 原生引用规避 ROOT_URL 问题，但 hub 返回的 `agentResult` 正文里仍带完整 URL（hub 侧拼接）。建议后续统一为 `#N` 相对引用。
2. **issue 已关闭但任务仍在跑**：issue #5 于 08-28 13:41:55 被 PR 合并自动关闭，而 task #13（code-review）12:25 启动后至今无终态。建议在 issue 关闭事件上做一次「在跑任务」的收敛/提示（避免关单后仍产出评论）。

---

## 5. Q1 补充：PR 作者应改为 Agent 账号（非 admin）

### 5.1 根因

**不是权限限制，是代码写死了用 admin token。** 三处建 PR 调用点均显式使用 `GetAdminGiteaClient()`：

| 调用位置 | 代码 |
|---------|------|
| `write_workspace.go:267` | `adminClient := factory.giteaFactory.GetAdminGiteaClient()` → builtin 路径 finalizeWriteChanges |
| `write_workspace.go:335` | 同上（fallback 路径） |
| `workspace_transport.go:284` | 同上（git_sync Approve 路径） |

函数签名本身就叫 `adminClient`（`write_pr.go:20`: `func FinalizeWriteTaskPR(adminClient *gitea.Client, ...)`），是历史命名。

### 5.2 改动可行性：✅ 可行，改动小

- `GiteaClientFactory` 接口已有 `GetGiteaClient(token string)` 方法（`runners.go:39`）
- `postGateComment` 已成功用 agent token 发评论（`comments.go:23`），证明 agent token 有完整 API 权限
- Agent 账号是 Setup 向导注册的正规 Gitea 用户，有仓库 write 权限，**建 PR 不需要 admin**

### 5.3 改动清单（✅ 已确认执行）

**原则：哪个 agent 负责的任务，就用那个 agent 的 token 执行 Gitea 操作。**

| # | 文件 | 改动 |
|---|------|------|
| 1 | `internal/agents/write_pr.go:20` | 参数名 `adminClient` → `client`（语义修正） |
| 2 | `internal/agents/write_workspace.go:267,335` | `GetAdminGiteaClient()` → `GetGiteaClient(agent.GiteaToken)`（builtin 写任务） |
| 3 | `internal/agents/workspace_transport.go:284` | 同上（git_sync Approve；agent 信息从 `task.AgentID` 反查 `store.Agent`） |

**保留 admin 的操作**（与具体任务无关的平台管理动作）：
- deploy key 签发（`NewGiteaDeployKeyIssuer`）
- orphaned deploy key 清扫（`SweepOrphanedDeployKeys`）

改后效果：
- PR author = **code-opencode / coder007 / code-review**（对应执行任务的 agent 账号）
- 「引用合并请求 将关闭此工单」事件署名也变为对应 agent（更符合直觉）
- Admin token 仅保留给真正需要管理员权限的操作（deploy key 签发、orphaned key 清扫等）

### 5.4 注意事项

- Git_sync 路径（`workspace_transport.go:284`）的 Approve 阶段可能拿不到原始 agent token（Matea 自身做校验+开 PR）。此路径可继续用 admin（因为 Approve 本就是 Matea 的管理动作），或从 `task.AgentID` 反查 agent 的 GiteaToken。
- 测试中 mock 工厂不受影响（mock 返回同一 client 实例）。

---

## 6. 状态卡外观澄清（附截图对照）

### 6.1 截图中红框内容 ≠ 我们能创建的东西

用户截图中红框圈出的条目：

```
📄 jeeinn 于昨天推送 1 个提交
⊙  🍀 添加多线程学习模块（thread_study）
👁  🧡 jeeinn 于2小时前请求 code-review 评审
```

这些是 **Gitea 原生系统时间线事件**（timeline event type）：`push_ref`、`cross_referenced`、`review_requested`。它们是 Gitea 内部生成的特殊事件类型，**不是评论，且 API 不开放自定义 timeline event 类型**。我们无法通过 API 创建这类左侧带图标的时间线条目。

### 6.2 我们的状态卡实际形态：markdown 评论

我们的状态卡是一条 **可编辑的 markdown 评论**（comment），渲染在时间线下方（与现有 `code-review 评论于 2小时前` / `AI Agent Response` 同级）。它不会出现在时间线侧栏里，而是作为一条**紧凑的卡片式评论**显示在评论区。

### 6.3 卡片内容示例（纯 markdown）

**处理中**（创建时写入，可心跳更新运行时长）：

```markdown
<!-- matea-agent -->
<!-- matea-status-card:task-13 -->

### 🤖 code-review · 代码审查

| 项目 | 内容 |
|------|------|
| **状态** | 🔄 处理中 |
| **开始于** | 2026-08-28 12:25:16 |
| **任务** | #13 |
| **触发** | @code-review |

> Matea 状态卡 · 随任务进展自动更新
```

> 注：**不加心跳**（决策 3）。卡片写入绝对开始时间戳，用户结合 Gitea 自带的「评论于 X 前」即可判断运行时长，零后台成本。

**终态 — 成功**（PATCH 同一张卡）：

```markdown
| **状态** | ✅ 完成 · 耗时 42m15s |
```
（下方追加 AI 回复正文）

**终态 — 失败**（PATCH 同一张卡）：

```markdown
| **状态** | ❌ 失败 · context deadline exceeded |
```
（下方追加错误详情）

### 6.4 渲染预期

在 Gitea issue 页面中，这条评论会渲染为：
- 一个带 emoji 标题的区块
- 一张信息表格（状态 / 任务 / 触发者 / 耗时）
- 终态时表格状态列变色（✅ 绿意 / ❌ 红色）
- 整体比现在的「🔄 已开始处理」+「✅ 分析完成」两条分开的评论更紧凑、更自洽

---

## 7. 已确认决策（2026-08-28 拍板）

| # | 决策项 | 结论 |
|---|--------|------|
| 1 | **Issue 链接关键词** | 降级为 **`Refs #N`**（不再自动关闭 issue，需人工关闭）。`pr.issue_link_mode` 可配置作为可选增强，非必须 |
| 2 | **状态卡方案** | **采用方案 B**：启动即建状态卡 + 全程 PATCH 同一张卡（1 任务 = 1 评论）。不做「整个 issue 一张卡」的进阶形态 |
| 3 | **卡片粒度** | **仅「开始 / 终态」两次更新，不加心跳**（理由见 §7.1） |
| 4 | **PR 作者** | **改为执行任务的对应 agent 账号**（原则：哪个 agent 负责的任务，就用那个 agent 操作） |

### 7.1 决策 3 说明：为什么不加心跳

加心跳（每 N 分钟 PATCH 一次运行时长）**收益低、复杂度高**：

| 维度 | 分析 |
|------|------|
| 信息冗余 | Gitea 评论本身就显示相对时间（「评论于 2小时前」），运行时长无需我们算 |
| 实现复杂度 | 需引入定时器 / goroutine 生命周期管理，且与任务取消、重启 re-attach 的时序耦合 |
| API 压力 | 并发任务多时产生大量无意义 PATCH，有触发 Gitea 速率限制的风险 |
| 失败模式 | 心跳失败要不要重试？任务卡死时心跳是否继续？都会引入新的边界情况 |

**替代方案（零成本达到同等效果）**：状态卡中写入**开始时间的绝对时间戳**（如 `2026-08-28 15:20:31 开始`），用户一眼可判断已运行时长，且不需要任何后台机制。

### 7.2 决策 4 说明：PR 作者归属原则

> **原则：哪个 agent 负责的任务，就用那个 agent 的 token 执行 Gitea 操作。**

具体落地：

| 路径 | 处理方式 |
|------|---------|
| builtin 写任务（`write_workspace.go:267/335`） | 用**执行该任务的 agent** 的 `GiteaToken` |
| git_sync Approve（`workspace_transport.go:284`） | 用**执行该任务的 agent** 的 `GiteaToken`（从 `task.AgentID` 反查） |
| deploy key 签发 / orphaned key 清扫 | **保留 admin**（Matea 自身的管理动作，与任务执行者无关） |

即：admin token 只保留给**与具体任务无关的平台管理动作**；凡是代表某个 agent 执行业务动作的（建 PR、发评论），一律用该 agent 自己的身份，这样时间线归属清晰、符合直觉。

### 7.3 落地顺序建议

1. **P0-1**：PR 作者改为 agent（决策 4）——改动最小（3 处调用点），收益立竿见影
2. **P0-2**：`Fixes #N` → `Refs #N`（决策 1）——1 行改动
3. **P1**：状态卡方案 B（决策 2 + 3）——改动较大，见 §2.6 清单

前两项可合并为一个小改动单独上线，状态卡作为独立迭代。

---

## 8. 实施记录（2026-08-28，已落地）

> 分支：`feat/issue-status-card`。⚠️ 本机沙箱对 `.git/` 的写入不持久（提交会回滚，且曾导致工作区被 `git checkout -f` 大面积删除），**改动留在工作区，需人工在 IDE / 终端提交**。

### 8.1 落地清单

| # | 决策 | 文件 | 内容 |
|---|------|------|------|
| 1 | P0-1 PR 归属 agent | `internal/agents/write_pr.go` | 参数 `adminClient` → `client`；新增 `resolveTaskGiteaClient(gf, agent)`：优先 `GetGiteaClient(agent.GiteaToken)`，agent/token 缺失时降级 admin 并 `[WARN]` |
| 2 | 同上 | `internal/agents/write_workspace.go:267,335` | 两处 `GetAdminGiteaClient()` → `resolveTaskGiteaClient(factory.giteaFactory, agent)` |
| 3 | 同上 | `internal/agents/workspace_transport.go:284` | git_sync Approve：**校验仍用 admin**（`fetchDraft` 拉证据是 Matea 平台动作），**开 PR 改用 agent token** |
| 4 | P0-2 `Refs #N` | `internal/agents/write_pr.go:42-46` | `Fixes #%d` → `Refs #%d`（附 CLOSE_KEYWORDS 说明注释） |
| 5 | P1-1 评论 API | `internal/gitea/issue.go` | 新增 `CreateIssueComment`（返回带 ID 的评论）+ `EditIssueComment`（`PATCH /issues/comments/{id}`）；旧 `IssueComment` 保留 |
| 6 | P1-2 持久化 | `internal/store/task.go`、`sqlite.go` | `Task.StatusCommentID` + 迁移列 + `UpdateTaskStatusCommentID`；`taskColumns`/`taskScanFields` 同步 |
| 7 | P1-3 卡片渲染 | `internal/workflow/status_card.go`（新增） | `AgentCommentMarker` 常量、`StatusCardMarker`、`StatusCard`、`RenderStatusCard`（纯 markdown）、`RoleLabel` |
| 8 | P1-3 卡片生命周期 | `internal/dispatcher/status_card.go`（新增） | `updateStatusCard`：存 ID → PATCH；失败 → marker 反查 → PATCH；都没有 → 创建。附 `postStatusCard` / `finishStatusCard` / `failStatusCard` / `findStatusCard` |
| 9 | P1-3 接入点 | `internal/dispatcher/pipeline.go:242` | 「🔄 已开始处理」→ `d.postStatusCard(...)`（建卡） |
| 10 | P1-3 接入点 | `internal/dispatcher/comments.go` | L3 通知改为 `completeStatusCard`（PATCH 卡片，guidance 作 Detail） |
| 11 | P1-3 接入点 | `internal/dispatcher/executor.go` | 失败写回改为 `failStatusCard`（PATCH 卡片带错误原因） |

### 8.2 相对原设计的三点偏离（有意为之）

1. **L3 通知折叠进卡片，而非独立评论**。原 §2.6-5 只说「终态改为 PATCH 该卡」，实现上把 L3 文案作为卡片的 `Detail` 渲染在状态表下方——卡片同时承载「结果」与「下一步建议」，比两条评论更自洽。通知开关关闭时仍会 PATCH 卡片为 ✅（只是不带引导文案），避免卡片永远停在「处理中」。
2. **AI 结果正文保持独立评论**。`Result.Content`（`formatComment`）是内容而非进度噪音，塞进卡片会让卡片变成正文容器，语义混乱，故不动。
3. **失败信息有兜底**。`updateStatusCard` 返回 error；卡片写失败时（`executor.go`）回退发传统的 `formatFailureComment` 评论。理由：失败原因属于「用户绝不能错过」的信息，卡片是美化手段，不能因它故障而丢失可见性。反之建卡失败**不回退**发「已开始处理」——那正是要消除的噪音。

### 8.3 未实现

- **心跳**（决策 3）：不实现。卡片写绝对开始时间戳，结合 Gitea 自带的「评论于 X 前」即可判断运行时长，零后台成本。
- `pr.issue_link_mode` 可配置项、`DeleteIssueComment`：均非必须，未做。

### 8.4 测试

| 包 | 文件 | 覆盖 |
|---|------|------|
| `internal/agents` | `write_pr_test.go`（新增） | agent token 优先 / 空 token 与 nil agent 降级 admin / nil factory / token 真到达 HTTP 层；PR body 用 `Refs #5` 且不含 `Fixes`/`Closes`；无 issue 时不产出链接 |
| `internal/gitea` | `issue_comment_test.go`（新增） | 创建返回 ID、PATCH 端点与方法正确、403 错误向上传播 |
| `internal/store` | `task_status_comment_test.go`（新增） | 默认 0、往返读写、任务间隔离、重启后仍可读 |
| `internal/workflow` | `status_card_test.go`（新增） | running/success/failed 三种渲染、marker 位置（第 1、2 行）、超长原因截断、可选行省略、`RoleLabel` |
| `internal/dispatcher` | `status_card_test.go`（新增） | **1 任务=1 评论**（第二次只 PATCH 不新建）、ID 丢失后按 marker 恢复、卡片被删后重建、Gitea 全失败时返回 error、无 token 时不发帖、time/trigger 辅助 |
