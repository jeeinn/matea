# Phase 2 演进方向：可吸纳与收束

> 日期：2026-08-05 ｜ 状态：**调研完成 · 待拍板**
> 输入：Matea 当前实现 + yc-software/qm 架构调研 + Hermes 官方 API 核实
> 本文件面向下一版 Phase 2 plan 提供决策依据，**只规划、不改码**。

---

## 0. 调研溯源

### 0.1 yc-software/qm 仓库现状

通过 GitHub API + npm registry 核验：

| 维度 | 结论 |
|------|------|
| 公开仓库结构 | `src/` 目录存在，含 `harness/` 子目录；但 GitHub API 连接不稳定，无法逐文件读取核心接口代码 |
| 包发布形态 | `@yc-software/qm` npm 包仅发布编译后 `dist/`，TypeScript 源码未随 npm 公开 |
| package.json 依赖 | 同时依赖 `@anthropic-ai/claude-agent-sdk`、`@openai/codex`、`opencode-ai`、`@earendil-works/pi-coding-agent`——四种 agent harness 并存 |
| README 架构描述 | "Every substrate (harness, session store, sandbox, memory) sits behind an interface, so production implementations swap in via one wiring file" |
| 架构图 | "Every turn runs through a central core, which can use a variety of models and harnesses to generate the response" |

**结论**：qm 的「大脑可插拔」架构意图在 README 和 package.json 中有充分证据，但具体 TypeScript 接口代码（`Harness`、`HarnessTurnController`、`createHarness-router.ts` 等）在当前网络环境下无法逐文件核验。**架构方向可信，接口细节应视为概念参考而非实现模板。**

### 0.2 Matea 当前实现（Phase 1 收官）

| 组件 | 状态 | 关键位置 |
|------|------|----------|
| HubBackend 接口 | ✅ 已定义 | `internal/agents/hub_backend.go` |
| BuiltinHubBackend | ✅ 已封装 AgentLoop | `internal/agents/builtin_hub_backend.go` |
| OpenCodeHTTPBackend | ✅ 已对接 HubBackend | `internal/agents/opencode_http.go` |
| Runner × 5 | ✅ 含 hub-* 分流分支 | `internal/agents/runner_*.go` |
| TaskContext / Handle | ✅ 含 MemoryKeys 预留 | `internal/agents/hub_backend.go` |
| MockHub 测试地基 | ✅ 7 测试通过 | `tests/integration/` |

---

## 1. 可吸纳：qm 验证的架构方向

### 1.1 「Harness 统一抽象」—— 升级为 Matea 的演进目标

qm 验证的核心理念：**把「执行内核」抽象成统一接口，不同实现通过 wiring 注册**。

这与 Matea 的 `HubBackend` 异曲同工：

| qm 意图 | Matea 现状 | 映射 |
|---------|-----------|------|
| `Harness` 统一接口 | `HubBackend` (Submit/Poll/Cancel) | **概念同构**——都是「执行内核可插拔」 |
| `controlTransport` 区分 in-process/http/api | builtin=进程内；hub-*=HTTP | **已自然对齐** |
| wiring `Map<HarnessId,Harness>` | `backends` 命名后端 map | **机制等价** |
| tools 由 core 拥有 | Matea 是唯一 Gitea 写方 | **不变量完全一致** |

**拍板建议**：

> **D10「Harness 统一抽象」**：在 Phase 2 开始时，将 `HubBackend` 接口重命名为 `Harness`（语义不变，仅改名对齐行业术语）。
> 
> 理由：
> 1. 「Harness」比「Backend」更精确——它描述的是「执行引擎」而非「远端服务」
> 2. 与业界术语对齐（qm、OpenAI Codex SDK、Claude Agent SDK 均用 Harness/Kernel 描述可插拔执行内核）
> 3. 零成本——纯改名 + 注释更新，不涉及行为变更

### 1.2 「ToolBox 由 core 拥有」—— 强化 Matea 的工具抽象

qm 的关键设计动作：**harness 不实现任何业务工具，只调用 core 提供的 ToolContext**。

这正是 Matea 已有的设计，但可以更强：

```go
// ToolBox 是 Matea 拥有的所有业务工具的抽象
// builtin harness 直接调用 Go 函数（进程内零开销）
// 远程 harness（OpenCode/Hermes）通过 MCP Server 暴露
type ToolBox interface {
    // Gitea 读写（Matea 是唯一写方）
    ReadIssue(ctx, repo string, issueID int64) (*gitea.Issue, error)
    ReadPRDiff(ctx, repo string, prID int64) (string, error)
    PostComment(ctx, repo string, issueID int64, body string) (int64, error)
    CreatePR(ctx, repo string, opts CreatePROpts) (*gitea.PullRequest, error)
    
    // 沙箱操作（code 任务专用）
    SandboxExec(ctx, taskID string, cmd string, args ...string) (*sandbox.ExecResult, error)
    SandboxReadFile(ctx, taskID string, path string) ([]byte, error)
    SandboxWriteFile(ctx, taskID string, path string, content []byte) error
    SandboxSearch(ctx, taskID string, pattern string) ([]sandbox.SearchResult, error)
    SandboxGit(ctx, taskID string, args ...string) (*sandbox.ExecResult, error)
}
```

**拍板建议**：

> **D11「ToolBox 接口抽取」**：在 Harness 接口中引入 `ToolBox` 作为唯一工具访问面。
>
> 理由：
> 1. 让 MCP Server 从 D4 的「可选」升级为「远程 harness 的工具桥」——价值明确
> 2. builtin harness 直接实现 ToolBox（Go 函数调用）
> 3. 远程 harness 的 ToolBox 实现 = MCP Client 调用 Matea 的 MCP Server
> 4. 保持「Matea 是唯一 Gitea 写方」不变量——所有写操作经 ToolBox 网关

### 1.3 「Harness 接口签名演进」—— 适配 Matea 的异步语义

qm 的 `runTurn` 是同步 await 语义（每轮即时响应）。Matea 的 HubBackend 是异步 Submit/Poll（长任务可能跑数十分钟）。

**Matea 不应照搬 qm 的同步签名，而应保持异步优势**：

```go
// 当前 HubBackend（Phase 1.2.1）
type HubBackend interface {
    Name() string
    Submit(ctx context.Context, task *TaskContext) (*Handle, error)
    Poll(ctx context.Context, h *Handle) (*BackendResult, State, error)
    Cancel(ctx context.Context, h *Handle) error
    Capabilities() HubCapabilities
    HealthCheck(ctx context.Context) error
}

// 演进后的 Harness（Phase 2 D10）
type Harness interface {
    Name() string
    
    // Run = Submit 的语义升级版：执行一个任务
    // 对同步 harness（builtin/OpenCode）：内部 Run → Handle(State=done)
    // 对异步 harness（Hermes）：内部 Run → Handle(State=pending, 落库) → Poll 至终态
    Run(ctx context.Context, input *TurnInput) (*TurnResult, *Handle, error)
    
    // Poll 保留：异步句柄的进度查询
    Poll(ctx context.Context, h *Handle) (*TurnResult, State, error)
    
    Cancel(ctx context.Context, h *Handle) error
    Capabilities() HarnessCapabilities
    HealthCheck(ctx context.Context) error
}

type TurnInput struct {
    Task  TaskContext      // issue/PR 内容、role、history（= qm 的 input+history+systemPrompt）
    Tools ToolBox          // Matea 拥有的工具集（= qm 的 ToolContext）
    Model string           // 可选覆盖（hub-* 通常忽略，Hub 自管 LLM）
}

type TurnResult struct {
    Reply        string
    GiteaActions []GiteaAction
    Deliver      *DeliverRequest
    Usage        *TokenUsage  // 可选：token 消耗统计
}
```

**与 qm 的关键差异**：
- qm 返回同步结果；Matea 返回 `*Handle`（异步持久化）
- qm 的 ToolContext 是「给 harness 的工具列表」；Matea 的 ToolBox 是「harness 可调用的工具接口」
- **这是对的**：qm 是对话平台（每轮即时响应），Matea 是工作流引擎（任务可能跑数十分钟）

---

## 2. 应收束：qm 不适合 Matea 的部分

### 2.1 「每轮动态选 harness」—— 过度设计

qm 的 `createHarnessRouter` 支持**每轮**动态切换 harness + model（`resolve(input) => { harnessId, modelId }`）。

**对 Matea 不适用**：

| 维度 | qm | Matea |
|------|-----|-------|
| 选择粒度 | 每轮 | 每任务 |
| 切换场景 | 同一对话中换「大脑」 | 一个 Issue 不会中途换 backend |
| 状态迁移 | 无状态（每轮独立） | 有状态（session、工作区、Handle） |

**收束建议**：保持「agent 配置绑 backend，任务生命周期内不变」。fallback 在 harness 内部实现（OpenCode 失败回落 builtin），不需要 router 层动态切换。

### 2.2 「harness×model 双轴校验矩阵」—— 不适用

qm 有 `modelSupportedByHarness(modelId, harnessId)` 运行时校验——因为 qm 的 harness 和 model 是两个独立维度的轴。

**对 Matea 不必要**：
- Matea 的 model 配置归属于 `llm.providers`（仅 builtin 使用）
- hub-* backend 的 LLM 由 Hub 自己管理（Phase 1.4 已冻结）
- **harness 和 model 在 Matea 不是正交轴**

**收束建议**：不引入 model×harness 校验矩阵。model 字段仅出现在 builtin 的 TurnInput 中，hub-* 忽略。

### 2.3 「多人多租户」—— 超出 Matea 范围

qm 的核心场景是「Slack + Web 的多人共享 agent 平台」，需要 per-scope sandbox、memory、permissions、web apps、crons。

**Matea 不需要**：
- Matea 是单实例 Gitea 协作者，无多租户需求
- Matea 的触发源是 Gitea webhook，不是 Slack
- Matea 的交互界面是 Gitea Issue/PR 评论，不是 Web UI

**收束建议**：qm 的多租户、Slack 集成、Web UI、crons 等能力**不在 Matea 范围内**，不要漂移。

---

## 3. 综合演进方向

### 3.1 Phase 2 地基：D10 + D11（Harness 归一 + ToolBox）

```
[第 1 周] D10: HubBackend → Harness 改名 + 接口签名演进
          D11: ToolBox 接口抽取（builtin 直连 Go 函数；远程经 MCP）
```

**改动范围**：
- `internal/agents/hub_backend.go`：`HubBackend` → `Harness`，`Submit` → `Run`
- `internal/agents/builtin_hub_backend.go`：`BuiltinHubBackend` → `BuiltinHarness`
- `internal/agents/opencode_http.go`：`OpenCodeHTTPBackend` → `OpenCodeHarness`
- 新增 `internal/agents/toolbox.go`：ToolBox 接口 + 两个实现（builtinToolbox / mcpToolbox）

**工作量**：~0.5 人日（改名 + 接口抽取 + 既有测试零修改通过）

### 3.2 Phase 2 主线：保持原决策（D1-D9 + 收束）

基于 Hermes 官方 API 核实和上次评审，主线不变：

| 任务 | 范围 | 状态 |
|------|------|------|
| 2.1 hub-hermes | 官方 Runs API (`POST /v1/runs` + `GET /v1/runs/{run_id}` Poll)，Bearer 鉴权，session_id 续接 | 已拍板 |
| 2.2 hub-opencode 全 role | D7 三刀（analyze→review→reply），第一刀最早可验证 | 已拍板 |
| 2.3 deliver | **仅出站扇出**（Hermes Poll / OpenCode 同步均不推完成事件） | 已拍板 |
| 2.4 MCP Server | **降级可选**（最小 4 工具）；若做则作为远程 harness 的工具桥 | 已拍板 |
| 2.5 渠道验收 | mock Hub，不依赖真实飞书 | 已拍板 |

### 3.3 新增：ToolBox 驱动 MCP Server 实现

D11（ToolBox）让 MCP Server 的价值从「可选」升级为「远程 harness 的工具桥」：

```
�─────────────────────────────────────────────────────────────┐
│  Matea                                                      │
│  ├─ Harness Registry (builtin / opencode / hermes)          │
│  ├─ ToolBox (所有业务工具)                                   │
│  │   ├─ builtinToolbox: 直接调用 Go 函数                     │
│  │   └─ mcpToolbox:    MCP Client → Matea MCP Server        │
│  ├─ MCP Server (暴露 ToolBox 为 matea_* tools)              │
│  └─ deliver outbound (POST webhook_url)                     │
└─────────────────────────────────────────────────────────────┘
                              ↑ MCP 工具调用
                    远程 Hermes / 其他 Agent 中枢
```

**这意味着**：
1. 若 Phase 2 实现 MCP Server，则远程 harness（不只是 Hermes，未来任何 MCP Client）都能通过 ToolBox 操作 Matea 的沙箱和 Gitea
2. 不做 MCP Server 也不阻塞主线——Hermes 走 HTTP Runs API，OpenCode 走自有 SDK

### 3.4 最终实施顺序（融合所有调研结论）

```
[必修] D10+D11: Harness 归一 + ToolBox 接口（~0.5 人日）
  → D7 第一刀: hub-opencode analyze 带工作区（~0.5-1 人日，最早可验证里程碑）
    → D5: deliver 出站模块 + webhook_url（~0.5 人日）
      → D8+2.1: Hermes 适配器（Runs API + Poll + Handle 落库，~2-3 人日）
        → 2.4: 渠道验收（mock Hub，~1-2 人日）

[可选] D4: MCP Server 4 工具 + API Key（~1-2 人日，作为远程 harness 工具桥）
[可选] D7 第二三刀: review/reply 全 role（第一刀反馈后再定）
[Phase 3] 远程 harness 经 MCP 操作 Matea 沙箱（需 MCP Server 落地）
```

---

## 4. 决策点清单（新增 D10-D11）

### D10 ｜ HubBackend → Harness 改名与接口演进

**选项**：
- **A（推荐）改名 + 签名演进**：`HubBackend` → `Harness`，`Submit` → `Run`，返回 `(TurnResult, *Handle)` 元组
- B 保持 HubBackend 不变

**✅ 推荐 A**。理由：与业界术语对齐（qm、Codex SDK、Claude Agent SDK 均用 Harness）；`Run` 比 `Submit` 更准确（Submit 暗示「排队」，Run 暗示「执行」）；返回元组让同步/异步实现统一。

**影响**：`internal/agents/hub_backend.go` 及两个实现文件 + 五个 Runner 文件的类型引用。既有测试零修改（接口行为不变）。

**待拍板**：是否同意 HubBackend → Harness 改名 + Submit → Run？

---

### D11 ｜ ToolBox 接口是否抽取

**选项**：
- **A（推荐）抽取 ToolBox 接口**：作为 Harness 的唯一工具访问面，builtin 直连 / 远程经 MCP
- B 保持现状（builtin 直接调 Gitea/sandbox，hub-* 走 Submit 自带工具）

**✅ 推荐 A**。理由：让 MCP Server 成为远程 harness 的工具桥（而非独立可选功能）；强化「Matea 是唯一 Gitea 写方」不变量（所有写操作经 ToolBox 网关）。

**影响**：新增 `internal/agents/toolbox.go`（~50 行接口 + builtin 实现）；MCP Server 的 ToolBox 实现 = mcpToolbox（~100 行）。

**待拍板**：是否同意抽取 ToolBox 接口？

---

## 5. 风险与注意事项

| 风险 | 说明 | 缓解 |
|------|------|------|
| Harness 改名范围 | 涉及 5 个 Runner + 2 实现文件 + 注册表 | 纯改名 + 签名调整，行为不变；全量测试零回退通过 |
| MCP Server 优先级 | D11 让 MCP 从「可选」变「推荐」但不阻塞 | Phase 2 最小交付不含 MCP；但 ToolBox 接口预留了 MCP 实现位置 |
| qm 代码不可直接复用 | qm 是 TypeScript + 同步语义；Matea 是 Go + 异步 | 只借鉴架构理念（统一接口 + core 拥工具 + wiring），不照搬代码 |
| ToolBox 性能 | MCP 调用增加远程 harness 的延迟 | builtin 直连无延迟；MCP 仅用于远程 harness（本身有网络开销） |
| 产品边界 | qm 的多租户/Slack/Web 不是 Matea 目标 | 保持 Gitea-first；不引入多租户、不研 IM SDK |

---

## 6. 总结：一句话方向

> **Matea Phase 2 = Harness 归一（改名 HubBackend + 引入 ToolBox）+ Hermes/OpenCode 双 Hub 验证 + deliver 出站 + 渠道验收（mock）**
>
> 吸纳 qm 的「统一执行内核抽象 + core 拥有工具」理念，
> 收束 qm 的「每轮动态选择 / 多租户 / model 校验」等不适配能力，
> 保持 Matea 的异步任务语义和 Gitea-first 产品边界。
