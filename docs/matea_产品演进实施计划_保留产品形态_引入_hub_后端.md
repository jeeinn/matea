# Matea 产品演进实施计划（精简版）

> 版本：2026-08-03 v5
> 本计划 **supersede** `matea_产品形态定位_397f2c49.plan.md` 中「Matea 降级为库、用户默认装 Hub 适配包」的路径。
> 原则：Matea 仍是可独立运行的 Gitea 协作者；不立刻大规模重构。

---

## 一、一句话定位

**Matea = Gitea 工作流编排器，可接入任意 Agent 中枢（OpenCode / Hermes / OpenClaw / 自定义）作为思考与执行后端；通过标准化 deliver 回传到外部渠道或 Hub。**

---

## 二、产品本质

| 是什么 | 不是什么 |
|--------|----------|
| 针对 Gitea 的 **Prompt + 触发 + 写回** 编排 | 通用 IM Gateway |
| **Gitea 工作流**（Assign/@、门禁、Session、PR 写回） | 完整 Coding IDE / Agent 框架 |
| **MCP Server**：外部系统 ↔ Gitea 读写 | 必须自研飞书/Slack SDK |
| 默认 **builtin** 小循环 | 强迫用户先装 Hermes |
| **Hub 可插拔**：OpenCode / Hermes / OpenClaw 统一作为 Agent 后端 | 只绑定单一中枢 |

核心竞争力：**@ 职责分明的 Agent 就能干活**；接入 Hub 后，**同一 repo 上的分析/审查/编码共享记忆与自进化**。

---

## 三、架构

```text
┌──────────────────────────────────────────────────────────┐
│  Matea                                                    │
│  ├─ Prompt 层（Matea 持有：模板、role、Gitea 语境）         │
│  ├─ Gitea 工作流（触发、门禁、写回、Session）               │
│  ├─ 上下文打包（issue/PR/diff/评论 → TaskContext）         │
│  ├─ 执行：builtin（默认）| hub-*（可选，全 role）           │
│  ├─ 编码落地：sandbox + git（code 任务默认 Matea）          │
│  └─ MCP Server / deliver webhook（Phase 2：外部系统回传）   │
└──────────────────────────────────────────────────────────┘
         ↑ 可选 MCP 客户端 / 标准化 deliver           ↑ 可选 Agent 后端
    Hermes / 其它外部渠道                    hub-opencode / hub-hermes / hub-openclaw
```

两条独立可选线：

1. **MCP / deliver 线**：外部系统（IM、CI 等）触发/收事件，由 Matea 标准化输出，Hub/bridge 负责实际渠道投递。
2. **Hub 后端线**：把 **推理 + 记忆 + 自进化** 外包给任意 Hub；**三个 Agent 均可** 配置任意 backend。

---

## 四、Agent 模型：固定职责，@ 即触发

| Gitea 用户 | role | 典型 task_type |
|------------|------|----------------|
| `@matea` | analyze | analyze_issue, reply_comment |
| `@matea-coder` | coder | solve_issue, fix_bug |
| `@matea-review` | review | review_pr |

- 仅 **`role`**，不做多 capabilities。
- **每个 Agent 独立配置 `backend`**：`builtin`（默认）、`hub-opencode`、`hub-hermes`、`hub-openclaw`、`hub-api`。
- 同一 repo 上推荐统一 backend，避免记忆断层。

---

## 五、Hub 后端抽象：统一入口，职责分明

### 5.1 统一抽象：`HubBackend`

OpenCode / Hermes / OpenClaw / 自定义 HTTP 中枢**统一视为可插拔 Agent 后端**，不再区分「coder worker」与「Agent 大脑」。

```go
package agents

type HubBackend interface {
    Name() string

    // 执行一次任务
    Execute(ctx context.Context, task *TaskContext) (*BackendResult, error)

    // 后端能力声明
    Capabilities() HubCapabilities

    // 健康检查
    HealthCheck(ctx context.Context) error
}

type HubCapabilities struct {
    SupportsToolUse         bool // 是否支持多轮 tool-use
    SupportsMemory          bool // 是否支持跨 session 记忆
    SupportsSkillEvolution  bool // 是否支持 Skill/E-A-A-S 自进化
    SupportsMCPClient       bool // 是否能作为 MCP 客户端调用 Matea 暴露的工具
    HasIMChannels           bool // 是否自带 IM 渠道（影响 deliver 默认策略）
    HandlesGit              bool // 是否自己处理 git/PR（默认 false）
}
```

每个 Hub 实现按自身能力声明；Matea 只按契约调用，不假设具体能力。

### 5.2 职责切分

| 层 | Matea 做 | Hub 做 |
|----|----------|--------|
| 触发 | webhook / MCP / Assign/@ / deliver webhook | — |
| 上下文 | **拉取** issue、评论、PR diff、历史 session | 消费 TaskContext |
| Prompt | **持有** system_prompt / user_template（Gitea 专有） | 叠加记忆、Skill、E-A-A-S |
| 推理 | — | LLM 多轮、tool-use |
| 记忆 | Session ID、correlation 键 | 项目记忆、Skill 自进化 |
| 写回 Gitea | **发** 评论 / 建 PR | 返回 BackendResult，不直接写 Gitea |
| Git | **sandbox** clone/commit/push（code 默认） | 不碰仓库（除非显式开启 HandlesGit） |
| 渠道回传 | 输出标准化 deliver 事件 | 自带渠道的 Hub 负责投递；无渠道时走 `deliver.webhook_url` |

**原则：Matea 永远握 Gitea 写回权与 workflow 门禁；Hub 是「可换的脑子」，不是第二个 Gateway。**

### 5.3 各 Hub 的差异定位

| Hub | 典型定位 | Matea 中的用途 |
|-----|----------|----------------|
| **builtin** | Matea 内置默认 | 不依赖外部 Hub，5 分钟可用 |
| **hub-opencode** | Coding Agent / 自进化编码中枢 | 全 role backend；IM 渠道当前缺失，可配 deliver webhook |
| **hub-hermes** | Agent 中枢 + 飞书/企微渠道 | 全 role backend；自带 IM，deliver 最自然 |
| **hub-openclaw** | 预留 | 待定 |
| **hub-api** | 通用 HTTP/MCP 中枢 | 用户自定义 |

OpenCode 与 Hermes 在接口上都是 `HubBackend`，都能覆盖 analyze/review/code/reply；差异在于：
- **能力成熟度**：memory / skill / MCP / IM 各 Hub 实现不同。
- **渠道覆盖**：Hermes 自带飞书/企微；OpenCode 无 IM，需要 `deliver.webhook_url` 桥接。

### 5.4 按 task_type 的执行模式

| task_type | Hub 模式 | Sandbox | 说明 |
|-----------|----------|---------|------|
| analyze_issue | single-shot 或短多轮 | 不需要 | Matea 打包 issue+评论 → Hub 分析 → 评论写回 |
| review_pr | single-shot | 不需要 | Matea 拉 diff → Hub 审查 → 评论/行评写回 |
| reply_comment | 短多轮 | 不需要 | 带 thread 历史 + Hub session 记忆 |
| solve_issue / fix_bug | 长多轮 + tool-use | 需要（默认 Matea 提供） | Hub 通过 Matea 暴露的工具改沙箱；git 由 Matea 执行 |

### 5.5 记忆关联键（跨 role 共享）

Matea 在调用 Hub 时附带稳定 metadata，供 Hub 记忆索引：

```text
matea.repo      = owner/repo          # 仓库级记忆（风格、架构约定）
matea.issue     = owner/repo#12       # Issue 级记忆（需求、讨论结论）
matea.pr        = owner/repo!45       # PR 级记忆（审查意见）
matea.session   = <matea_session_id>  # 单次会话多轮
matea.role      = analyze|coder|review
matea.thread    = <im_thread_id>      # IM 关联（可选）
```

- **Matea Session**（SQLite）：workflow 门禁、同 issue 锁、任务队列——**短期、结构化**
- **Hub Memory**：跨 session、跨 role、E-A-A-S——**长期、语义化**
- Matea **不复制** Hub 记忆存储；只传键，让 Hub 自己 recall

### 5.6 Prompt 谁说了算？

**Matea Prompt 层仍是源真相**（Web UI 可配的 system_prompt / user_template）。

Hub 侧可选配合同名 Skill（如 `matea-analyze` / `matea-review` / `matea-dev`），用于：
- 给 Hub E-A-A-S 一个可进化的 Skill 载体
- 与 Matea 模板内容初始对齐；进化后的 Skill 通过配置同步策略（Phase 2 手动 export/import，不做自动双向写）

调用时：`TaskContext.SystemPrompt` = Matea 渲染结果 + Hub 自动 recall 的记忆片段。

---
matea.role      = analyze|coder|review
matea.thread    = <im_thread_id>     # IM 关联（可选）
```

- **Matea Session**（SQLite）：workflow 门禁、同 issue 锁、任务队列——**短期、结构化**
- **Hub Memory**：跨 session、跨 role、E-A-A-S——**长期、语义化**
- Matea **不复制** Hub 记忆存储；只传键，让 Hub 自己 recall

### 5.6 Prompt 谁说了算？

**Matea Prompt 层仍是源真相**（Web UI 可配的 system_prompt / user_template）。

Hub 侧可选配合同名 Skill（`matea-analyze` / `matea-review` / `matea-dev`），用于：
- 给 Hub E-A-A-S 一个可进化的 Skill 载体
- 与 Matea 模板内容初始对齐；进化后的 Skill 通过配置同步策略（Phase 2 手动 export/import，不做自动双向写）

调用时：`TaskContext.SystemPrompt` = Matea 渲染结果 + Hub 自动 recall 的记忆片段。

---

## 六、HubBackend 接入：统一接口，分步实现

### 6.1 统一入口：Runner 层 backend 分流

现有四个 Runner 保留；每个 `Run()` 开头：

```text
if agent.Backend starts with "hub-" → HubBackend.Execute(TaskContext)
else                                  → 现有 builtin 逻辑
```

每个 backend 一个实现：`hub-opencode` / `hub-hermes` / `hub-openclaw` / `hub-api`。不合并 Runner，只抽 `HubBackend.Execute`，内部按 `task.TaskType` 分支。改动面可控。

### 6.2 TaskContext（Matea 打包给 Hub）

```go
type TaskContext struct {
    TaskType   string // analyze_issue | review_pr | reply_comment | solve_issue | fix_bug
    Role       string
    Backend    string // builtin | hub-opencode | hub-hermes | ...
    Repo       string
    IssueID    int64
    PRID       int64

    // Matea 预取的 Gitea 上下文（Hub 不直连 Gitea API）
    IssueTitle, IssueBody string
    Comments              []CommentSnapshot
    Diff                  string   // review 专用
    BaseBranch            string

    // Matea Prompt 层输出
    SystemPrompt, UserPrompt string

    // 记忆关联
    MemoryKeys map[string]string

    // 仅 code 任务
    SandboxPath string
    Tools       []ToolDef       // read_file / write_file / run_command … 指向 Matea 沙箱

    // 渠道回传
    Channel, ThreadID string
}
```

### 6.3 Hub 调用方式（推荐分阶段）

| 阶段 | 方式 | 适用 |
|------|------|------|
| **2a** | Hub HTTP/API：提交 prompt + metadata，收文本结果 | analyze、review、reply |
| **2b** | Hub + **Matea MCP tools**（沙箱工具注册到 Hub session） | code、fix |

2a 先跑通记忆链路；2b 再开编码多轮。Analyze/Review 不需要 Hub 直接读 Gitea——**减少集成面、保证 Matea 审计一致**。

### 6.4 BackendResult 与写回

```go
type BackendResult struct {
    Summary           string
    GiteaActions      []GiteaAction  // comment | create_pr | …
    Deliver           *DeliverRequest  // 若需要回传到 IM/外部系统
    ExternallyHandled bool             // 默认 false；Hub 已自己处理 git/PR 时为 true
}
```

- analyze/review/reply：通常 `GiteaActions = [{Kind: "comment", Content: ...}]`
- code：Hub 改完沙箱后 `Summary` + Matea `finalizeWriteChanges` 做 git/PR（复用现有 write path）

### 6.5 推荐 Phase 2 实施顺序

1. **analyze + review → hub-hermes**（验证记忆键、Prompt 传递、评论写回）
2. **analyze + review → hub-opencode**（验证同一接口对多 Hub 的适配性）
3. **reply_comment**（验证多轮 + session）
4. **solve_issue / fix_bug**（沙箱工具 + git 写回）
5. **MCP + deliver 回传**（与 §七 并行或略后）

### 6.6 配置示例

#### 全 role Hermes（自带 IM）

```yaml
agents:
  defaults:
    backend: hub-hermes

  - name: matea
    role: analyze
    backend: hub-hermes
    hub_hermes:
      url: http://127.0.0.1:8081
      skill: matea-analyze
      api_key: "${HERMES_API_KEY}"
  - name: matea-coder
    role: coder
    backend: hub-hermes
    hub_hermes:
      url: http://127.0.0.1:8081
      skill: matea-dev
      api_key: "${HERMES_API_KEY}"
  - name: matea-review
    role: review
    backend: hub-hermes
    hub_hermes:
      url: http://127.0.0.1:8081
      skill: matea-review
      api_key: "${HERMES_API_KEY}"
```

#### 全 role OpenCode（IM 缺失，用 deliver webhook）

```yaml
agents:
  defaults:
    backend: hub-opencode

  - name: matea
    role: analyze
    backend: hub-opencode
    hub_opencode:
      url: http://127.0.0.1:3000
      api_key: "${OPENCODE_API_KEY}"

deliver:
  webhook_url: "${MATEA_DELIVER_WEBHOOK_URL}"  # 用户自建 IM bridge 或 企业微信/钉钉机器人
```

#### 混合示例（不推荐长期，记忆会断层）

```yaml
  - name: matea
    backend: hub-hermes
  - name: matea-coder
    backend: hub-opencode
```

---

## 七、外部触发与回传：MCP + Deliver Webhook

### 7.1 核心原则

- **Matea 不自研飞书/钉钉/Slack SDK**。
- **Matea 暴露 MCP Server** 给支持的 Hub（如 Hermes），供其调用 Gitea 工具/触发工作流。
- **Matea 输出标准化 deliver 事件**：任务完成后按 `channel` + `thread_id` 推到配置的 endpoint。
- **deliver 目的地**可以是：
  - Hub 自己的接收端（如 Hermes 的渠道网关）
  - 用户自建的 IM bridge
  - 企业微信/钉钉/飞书机器人 webhook URL

### 7.2 MCP Server（Hub 作为客户端）

Matea 暴露的 MCP 工具示例：

| MCP 工具 | 作用 |
|---|---|
| `matea_create_issue` | 在 Gitea 创建 Issue |
| `matea_assign_agent` | Assign Agent，触发工作流 |
| `matea_comment_on_issue` | 在 Gitea 评论 |
| `matea_get_issue_status` | 查询 Issue/PR/工作流状态 |
| `matea_list_open_issues` | 列出待处理 Issue |
| `matea_reset_session` | 重置某 Issue 会话 |

MCP 入站鉴权：

```yaml
mcp:
  enabled: true
  listen: "127.0.0.1:8082"
  auth:
    type: api_key        # api_key | mtls
    api_key: "${MATEA_MCP_API_KEY}"
```

### 7.3 Deliver Webhook（标准化回传）

```yaml
deliver:
  enabled: false
  webhook_url: "${MATEA_DELIVER_WEBHOOK_URL}"  # 可选；Hub 自带渠道时可以不配
```

事件示例：

```http
POST {MATEA_DELIVER_WEBHOOK_URL}
{
  "event": "task_completed",
  "channel": "feishu",
  "thread_id": "feishu-thread-123",
  "repo": "org/repo",
  "issue_id": 12,
  "pr_id": 123,
  "action": "comment",
  "content": "已分析完成..."
}
```

**IM 渠道由每个 Hub 或用户自配 bridge 解决；Matea 只输出标准化事件。**

---

## 八、LLM 配置边界

| backend | LLM 配置归属 | 说明 |
|---|---|---|
| `builtin` | **Matea `llm.providers`** | 走内置 AgentLoop |
| `hub-opencode` | **OpenCode 自己配置** | Matea 只配 endpoint + api_key |
| `hub-hermes` | **Hermes 自己配置** | Matea 只配 url + skill + api_key |
| `hub-openclaw` / `hub-api` | **Hub 自己配置** | 类似 |

### UI 动态表单

Agent 编辑页根据 `backend` 动态显示字段：

- `builtin`：Provider / Model / Temperature / System Prompt / Loop Config
- `hub-hermes`：URL / Skill / API Key / Memory Keys（高级）
- `hub-opencode`：URL / API Key / Workspace Mode

**关键：不要让 builtin 的 LLM 字段和 hub 的 URL 字段同时全部显示。**

系统配置页保留 `llm.providers`，但明确提示：

> “LLM Providers 仅用于 builtin backend 的 Agent。使用 Hub backend 的 Agent 由 Hub 自行管理 LLM。”

---

## 九、接口抽象

### Phase 1

- 定义 `HubBackend` 接口 + `TaskContext` / `BackendResult` 结构（**覆盖全 task_type 类型定义，但只实现 builtin**）
- `internal/ingress/gitea` 整理 → `Intent`
- 四个 Runner 内部预留 `if strings.HasPrefix(agent.Backend, "hub-")` 分支（Phase 1 不实现 hub 体）

### Phase 2

- 实现 `backends/hermes` 与 `backends/opencode`，按 §6.5 顺序接入全 role
- MCP Server + deliver webhook

### 保留组件

- `internal/llm` Registry：**仅 builtin 使用**，不砍
- `EnsureGiteaAccount`：**默认开启，可配置关闭**

---

## 十、用户体验

### 默认（builtin）

下载 → 配 Gitea + LLM → Gitea @ 三 Agent

### 进阶 A — 全 role Hermes

1. 部署 Hermes
2. 三 Agent 设 `backend: hub-hermes`
3. 可选：Matea MCP 接入 Hermes，实现 IM 触发/回传
4. 同一 repo：分析沉淀 → 审查引用 → 编码遵循——记忆贯通

### 进阶 B — 全 role OpenCode

1. 部署 OpenCode
2. 三 Agent 设 `backend: hub-opencode`
3. 配置 `deliver.webhook_url` 回传到自建 IM bridge
4. OpenCode 无自带 IM 时，Gitea 仍是主要交互界面

### 进阶 C — 混合

不推荐长期，会导致记忆断层。

---

## 十一、实施阶段

### Phase 1（1–2 月）

- 三 Agent 模板 + `role`
- `HubBackend` / `TaskContext` / `BackendResult` **类型定义**（含全 task_type）
- `ingress/gitea` → `Intent`
- builtin 封装；Runner 预留 `hub-*` 分支
- LLM 配置边界 + UI 动态表单设计
- 隐藏 workflow stage

**不做**：具体 hub 实现、MCP、deliver

### Phase 2（2–3 月）

- `backends/hermes`：**analyze → review → reply → code**（§6.5 顺序）
- `backends/opencode`：验证同一 `HubBackend` 接口对多 Hub 的适配性
- 记忆键 + Matea Prompt 传递
- MCP Server + deliver webhook
- 验收：同 repo 上 analyze 结论影响后续 review/coder 行为（Hub 记忆可见）

### Phase 3+

REST/CLI、策略配置化、拆包评估、`matea gateway serve`

---

## 十二、已冻结决策

| # | 决策 | 结论 |
|---|------|------|
| 1 | capabilities | **仅 role**，三固定 Agent |
| 2 | 产品本质 | **Gitea 工作流编排器 + 可插拔 Agent 中枢后端** |
| 3 | Hub scope | **OpenCode / Hermes / OpenClaw 统一抽象为 `HubBackend`**，均可覆盖 analyze/review/code/reply |
| 4 | 职责切分 | Matea：上下文+Prompt+写回+git；Hub：推理+记忆+自进化 |
| 5 | Hub 读 Gitea | **默认否**；Matea 预打包 TaskContext |
| 6 | code git | **Matea sandbox**（默认）；Hub 不碰仓库，除非显式 `HandlesGit=true` |
| 7 | IM 渠道 | **Hub 自带或用户自配 bridge**；Matea 通过 MCP / deliver webhook 输出标准化事件 |
| 8 | LLM 配置边界 | **builtin 用 Matea `llm.providers`；Hub backend 由 Hub 自己管理 LLM** |
| 9 | LLM / builtin 去留 | **不砍 `internal/llm` 和 builtin**，保留为默认与 fallback |
| 10 | gateway 形态 | **同二进制 + 子命令** `matea gateway serve`；远期可拆 |
| 11 | Phase 2 顺序 | analyze/review 先于 code；Hermes 与 OpenCode 并行验证接口通用性 |

---

## 十三、下一步

1. v5 审阅通过 → 冻结 Phase 1
2. Phase 1 产出：`HubBackend` / `TaskContext` / `BackendResult` / `Intent` 接口草案
3. Phase 2 详设：`HubHermes.Execute` + `HubOpenCode.Execute` 分支 + 记忆键 + 2a/2b 调用协议
4. `internal/ingress/gitea` 整理（不改业务逻辑）
5. 更新 README / AGENTS.md 产品叙事
