# 服务器端 Agent 运行时设计（Server Runtime Design）v2

> 状态：设计草案 v2
> 目标：为 Gitea Agent Gateway 的服务器端 Agent 执行提供可复现、可并发、可持久化记忆、可插拔扩展的运行时。
> 部署目标：Linux（可与 Gitea 同机或独立部署）。定位：个人/小团队自用，最终开源。
> 前身：[20260714-server-runtime-design.md](./20260714-server-runtime-design.md)（v1，已归档）  
> 状态：已归档；现行决策见 [server-runtime-design-v4.md](20260714-server-runtime-design-v4.md)。  
> 说明：v2 在 v1 基础上重构分层边界、结构化 EnvSpec、补齐 Agent 生命周期 / Services / 记忆策略 / 可观测性 / 插件机制。

---

## 0. 变更记录（v1 → v2）

| 类别 | v1 | v2 |
|------|----|----|
| 分层 | Provider 与 WorkspaceManager 职责重叠 | 六包独立，Source/Scheduler/WorkspaceManager/Provider/Agent/Memory 各管一事 |
| EnvSpec | `map[string]string` 无法表达版本范围/来源/可选 | `[]Tool` 结构体，支持 Version/Source/Optional |
| Agent | 仅 `RunCommand` 短命令 | 补齐 `StartAgent/PollAgent/CancelAgent` 长进程生命周期 |
| Services | 仅 type+version | 补齐 Port/持久化/健康检查/初始化，明确 Host vs Docker 差异 |
| 记忆 | 只定义数据模型 | 补齐检索/淘汰/冲突策略 |
| 可观测 | 无 | 状态机 + metrics + 日志 + 重试告警 |
| 扩展 | 仅 Provider | 新增 Source 抽象 / Agent 抽象 / MCP 插件机制 |
| 风险 | 未标注 | 显式标注 4 个风险点及缓解措施 |

---

## 1. 背景与问题

当前 sandbox 仅做目录隔离 + 命令白名单，Agent clone 到的是**裸源码**，没有编译/运行环境（`go mod download`、`npm install`、数据库等均缺失），Agent 无法真正验证代码。同时缺少并发调度、多分支隔离、环境一致性保障和跨任务记忆。

行业现状：没有开源项目把「AI Coding Agent + 可复现开发环境 + 服务化部署」三者打通。这是本项目的机会点。

---

## 2. 架构总览与分层

运行时由六个**相互独立、接口驱动**的包组成，每个包只管一件事：

```
┌─────────────────────────────────────────────────────────────┐
│  Source（事件来源）                                          │
│   Gitea / GitHub / GitLab / CLI → Issue/PR/Event           │
└───────────────────────────┬─────────────────────────────────┘
                            ↓ WatchEvents / FetchIssue
┌─────────────────────────────────────────────────────────────┐
│  Scheduler（调度）                                           │
│   三级信号量 + 分支锁 → 决定任务何时、在哪运行               │
└───────────────────────────┬─────────────────────────────────┘
                            ↓ Acquire
┌─────────────────────────────────────────────────────────────┐
│  WorkspaceManager（git 层，所有 Provider 共用）              │
│   bare mirror + worktree 生命周期 + LRU GC + 重启恢复        │
└───────────────────────────┬─────────────────────────────────┘
                            ↓ SetupWorkspace
┌─────────────────────────────────────────────────────────────┐
│  Provider（执行层）                                          │
│   Host / Docker / Firecracker 后端，统一封装环境准备与命令执行│
└───────────────────────────┬─────────────────────────────────┘
                            ↓ StartAgent
┌─────────────────────────────────────────────────────────────┐
│  Agent（智能体）                                            │
│   Claude Code / OpenCode / Custom → 在 Workspace 内执行      │
└───────────────────────────┬─────────────────────────────────┘
                            ↓ Retrieve / Store
┌─────────────────────────────────────────────────────────────┐
│  Memory（记忆）                                             │
│   L1 会话 / L2 项目 / L3 身份，容器外持久化                 │
└─────────────────────────────────────────────────────────────┘
```

**设计约束**：所有跨包交互走 interface，禁止包之间直接依赖具体实现。今天写 `HostProvider` 也按抽象接口写，不因"简单"而偷懒写成具体函数。

---

## 3. 核心设计哲学：临时层 vs 持久层

```
┌─────────────────────────────────────────────────┐
│ 宿主机（持久层，永不随任务销毁）                    │
│  repos/     bare mirror（对象库共享）              │
│  memory/    Agent 记忆 SQLite（每项目一个）        │
│  cache/     依赖缓存（go mod / npm / cargo）        │
│  images/    环境镜像缓存（内容寻址）                │
└─────────────────────────────────────────────────┘
            ↓ 挂载（bind mount / volume）
┌─────────────────────────────────────────────────┐
│ 容器/沙箱（临时层，任务结束即销毁）                 │
│  worktree/  该任务的工作目录                        │
│  Agent 进程  跑完就退                               │
└─────────────────────────────────────────────────┘
```

四大难点的解法：
- **worktree** → 解决多分支 / 多任务 / 磁盘压力
- **内容寻址镜像** → 解决环境一致性
- **容器外分层记忆** → 解决记忆持久化
- **三级信号量 + LRU** → 解决并发与资源

**SQLite 持久化强制约束**：所有 SQLite 数据库（`gateway.db` / `memory/{repo}.db`）必须启用 WAL 模式 + `busy_timeout`，并使用连接池（写操作串行化）。多 task 并发写时，WAL 避免读写互斥导致的锁等待。

---

## 4. 存储布局

```
/var/lib/gateway/
├── repos/{repo_hash}/          # bare mirror（对象库，多分支共享）
├── worktrees/{session_id}/     # 任务工作目录（临时，用完删/重启恢复）
│   └── repo/
├── images/                     # 环境镜像（内容寻址，复用）
├── memory/{repo_hash}.db       # 项目记忆（持久，挂载进容器，WAL 模式）
├── cache/                      # 依赖缓存（跨项目共享）
│   ├── go-mod/
│   ├── npm/
│   └── cargo/
└── gateway.db                  # 主库（session/task/agent，WAL 模式）
```

`repo_hash = sha256(repo_full_name)` 前 16 位。

---

## 5. 并发与多分支：git worktree

### 5.1 为什么用 worktree

每个项目只有**一份 bare 对象库**（`repos/{repo_hash}/`），每个任务从 mirror 开一个 **worktree**（工作目录），共享对象库。不要每个任务 `git clone` 一次（磁盘炸、速度慢）。

```bash
# 项目首次：建 bare mirror
git clone --mirror https://gitea/user/dbx repos/{repo_hash}

# 每个任务：从 mirror 开 worktree（秒级，几乎不占额外磁盘）
git -C repos/{repo_hash} fetch --all --prune
git -C repos/{repo_hash} worktree add ../../worktrees/{session_id}/repo ai/issue-42

# 任务结束：清理
git -C repos/{repo_hash} worktree remove ../../worktrees/{session_id}/repo
```

### 5.2 worktree 天然解决的问题

| 场景 | 处理方式 |
|------|---------|
| 同项目**不同分支**并发 | 各开一个 worktree，共享对象库，完全并行 |
| 同项目**同一分支**并发 | git worktree 强制一个分支只能 checkout 一处 → 天然串行 |
| 多项目 | 各自 bare mirror，互不干扰 |
| 磁盘 | 对象库只存一份，worktree 只有工作树，省 90%+ 空间 |

### 5.3 三级信号量调度 + 分支锁

```go
type Scheduler struct {
    globalSem   chan struct{}            // 全局最大并发（防 OOM）
    perProject  map[string]chan struct{} // 每项目并发上限（防单项目吃满）
    branchLock  sync.Map                 // (repo, branch) → 互斥锁
}
```

- 全局：`min(CPU核数, 内存/单任务预估)`，默认 4
- 每项目：默认 2，防止一个大项目饿死其他
- **分支锁说明**：git worktree 本身已阻止同一分支 checkout 两处（`worktree add` 会失败）。`branchLock` 是**预防性**措施——在 `WorkspaceManager.Acquire` 之前先 try-acquire 锁，避免两个并发任务竞争同一分支导致其中一个 `worktree add` 失败、需要重试。锁在 `Release` 时释放。

---

## 6. 多项目：内容寻址 + LRU 回收

多项目的真正压力是磁盘增长。三策略：

1. bare mirror 定期 `fetch` 更新，不重复 clone
2. worktree 用完即 `worktree remove`
3. bare mirror 按 LRU 回收：超阈值删最久没用的项目

```yaml
storage:
  max_disk: "50GB"
  repo_retention: "30d"            # 30天没任务的项目 mirror 删除
  worktree_cleanup: "on_complete"  # 任务完成即清理 worktree
  cache_shared: true               # 依赖缓存跨项目共享
```

依赖缓存**跨项目共享**：所有 Go 项目共用 `go mod cache`，所有 Node 项目共用 npm cache。第二个 Go 项目的 `go mod download` 基本命中缓存。

---

## 7. 环境一致性：环境即代码 + 内容寻址镜像

### 7.1 环境清单来自项目本身

```
仓库根/
├── .gateway/env.yaml          ← 优先
└── .devcontainer/             ← 或复用社区标准（次选）
    └── devcontainer.json
```

无 manifest 时回退到 `DefaultSpec`（base + git + 常用工具链）。

### 7.2 内容寻址：一致性的根

```
env.yaml → sha256(env.yaml + base_image) → 镜像 tag
                    ↓
         images/ 里有这个 tag 吗？
         ├── 有 → 直接用（同 manifest = 同镜像 = 环境绝对相同）
         └── 无 → docker build → 打 tag → 缓存
```

```go
func (b *Builder) Resolve(spec *EnvSpec) (imageTag string) {
    hash := sha256(spec.Canonical())   // manifest 规范化后哈希
    tag := fmt.Sprintf("gateway-env:%s", hash[:16])
    if b.imageExists(tag) {
        return tag                     // 一致性来源：哈希相同 = 环境绝对相同
    }
    b.build(spec, tag)                 // 首次构建，之后全部复用
    return tag
}
```

**一致性由哈希保证**：只要 `env.yaml` 不变，任何机器、任何时间构建出的镜像都相同。CI 和 Agent 用同一份 manifest → 环境同构 → 消灭「在我机器上能跑」。

### 7.3 容器启动挂载

```bash
docker run \
  -v worktrees/{session}/repo:/workspace \
  -v cache/go-mod:/go/pkg/mod \          # 共享依赖缓存
  -v memory/{repo_hash}.db:/memory/agent.db \  # 挂记忆
  gateway-env:{hash}
```

### 7.4 降级：host 模式

无 Docker 时 `provider: host`，直接用宿主工具链 + `go mod download` 前置，牺牲隔离换简单。**详见第 16 节风险点 2**。

---

## 8. 运行时组件与接口

### 8.1 Source 抽象（事件来源，不止 Gitea）

```go
// internal/source/source.go
type Source interface {
    Name() string
    FetchIssue(ctx context.Context, repo string, id int) (*Issue, error)
    CreatePR(ctx context.Context, repo string, spec *PRSpec) (*PR, error)
    PostComment(ctx context.Context, repo, target string, body string) error
    WatchEvents(ctx context.Context) (<-chan SourceEvent, error)
    Close() error
}

// 实现：GiteaSource（已有）/ GitHubSource / GitLabSource / CLISource
```

### 8.2 Scheduler 接口

```go
// internal/runtime/scheduler.go
type Scheduler interface {
    Submit(ctx context.Context, task *store.Task) error
    // 内部使用三级信号量 + 分支锁决定执行时机与位置
}
```

### 8.3 WorkspaceManager（git 层，所有 Provider 共用）

```go
// internal/runtime/workspace.go
type WorkspaceManager interface {
    Acquire(ctx context.Context, session, repoURL, branch string) (*Workspace, error)  // worktree add
    Release(ws *Workspace) error                                       // worktree remove
    GC(ctx context.Context) error                                      // LRU 回收
    Recover() error                                                    // 重启后清理残留 worktree
}

type Workspace struct {
    ID        string    // sessionID
    Path      string    // 工作目录绝对路径
    RepoPath  string    // bare mirror 路径
    Branch    string
    EnvSpec   *EnvSpec
    CreatedAt time.Time
}
```

**清理可靠性（风险点 3）**：任务处理函数必须用 `defer wsMgr.Release(ws)`；Gateway 启动时调用 `Recover()` 扫描 `worktrees/` 目录，移除孤儿 worktree（其对应 task 已不存在或已结束）；周期性 GC 兜底。

### 8.4 Provider 模式（执行层，统一后端封装）

```go
// internal/runtime/provider.go
type Provider interface {
    // 创建并准备工作空间（调用 WorkspaceManager.Acquire + InstallDeps）
    SetupWorkspace(ctx context.Context, sessionID, repoURL, branch string, spec *EnvSpec) (*Workspace, error)

    // 安装依赖（go mod download / npm install 等，按 EnvSpec 决定）
    InstallDeps(ctx context.Context, ws *Workspace, spec *EnvSpec) error

    // 在 workspace 内启动 Agent 长进程
    StartAgent(ctx context.Context, ws *Workspace, agent Agent, prompt string, opts AgentOptions) (AgentHandle, error)

    // 在工作空间内执行短命令
    RunCommand(ctx context.Context, ws *Workspace, cmd *Command) (*Result, error)

    // 轮询 Agent 状态
    PollAgent(ctx context.Context, h AgentHandle) (AgentStatus, error)

    // 取消 Agent（超时 / 用户中止）
    CancelAgent(ctx context.Context, h AgentHandle) error

    // 清理工作空间
    CleanupWorkspace(ws *Workspace) error

    // 健康检查
    HealthCheck(ctx context.Context) error
}

type Command struct {
    Cmd     string
    Args    []string
    Env     map[string]string
    Dir     string  // 默认 ws.Path
    Timeout time.Duration
}

type Result struct {
    Stdout   string
    Stderr   string
    ExitCode int
    Duration time.Duration
}

type AgentHandle interface {
    ID() string
}

type AgentStatus struct {
    State     string  // running | done | failed | timeout
    Progress  string  // 最近输出摘要
    ExitCode  int
}
```

**后端实现优先级**：

| Provider | 复杂度 | 隔离级别 | 何时实现 |
|----------|-------|---------|---------|
| `HostProvider` | 最低 | 无（与 Gateway 同权限） | **Phase 1** |
| `DockerProvider` | 中 | 命名空间+Cgroups | **Phase 2** |
| `FirecrackerProvider` | 高 | KVM 硬件虚拟化 | Future（不可信代码需求时） |

Host 用 `exec.Command`，Docker 用 `docker exec`，Firecracker 用 VM 内 SSH。统一抽象后，后端切换零成本。

### 8.5 Agent 抽象（智能体，不止一个）

```go
// internal/agent/agent.go
type Agent interface {
    Name() string
    // 在指定 workspace 内启动，返回可轮询的 handle
    Start(ctx context.Context, ws *runtime.Workspace, prompt string, opts AgentOptions) (runtime.AgentHandle, error)
}

type AgentOptions struct {
    Model     string
    MaxTokens int
    MaxTurns  int
    Tools     []string  // 启用的 MCP server 名称（见第 13 节）
    SkipPerms bool
}

// 实现：ClaudeCodeAgent / OpenCodeAgent / CustomScriptAgent
```

### 8.6 EnvSpec 结构化

```go
// internal/env/spec.go
type Tool struct {
    Name     string  // go, node, python, rust
    Version  string  // "1.26" | ">=1.21" | "latest"
    Source   string  // apt | pip | brew | asdf | go-install
    Optional bool
}

type ServiceSpec struct {
    Type        string            // mysql | postgres | redis | mongodb
    Version     string
    Port        int               // 容器内端口
    HostPort    int               // 宿主映射（0=随机）
    Persistent  bool              // 数据是否持久化
    Volume      string            // 持久化挂载路径（相对 base_dir）
    HealthCheck string            // 就绪检查命令
    InitScript  string            // 初始化（建库/建表/seed）
    Env         map[string]string
}

type EnvSpec struct {
    Name     string
    Base     string            // ubuntu:22.04
    Tools    []Tool            // 合并原 tools + system，用 Source 区分 apt/pip
    Packages []string          // 系统级依赖（apt install）
    Services []ServiceSpec
    Env      map[string]string
}

func LoadEnvSpec(repoDir string) (*EnvSpec, error)  // .gateway/env.yaml → devcontainer → default
func (s *EnvSpec) Canonical() []byte                // 规范化用于哈希

// internal/env/builder.go
type Builder interface {
    Resolve(spec *EnvSpec) (imageTag string, err error)  // 内容寻址，命中即复用
}
```

---

## 9. Agent 生命周期管理

Agent 是**长进程**（10-30min），不能当作一次性命令。状态机贯穿全链路：

```
SetupWorkspace → InstallDeps → StartAgent → PollAgent(loop)
                                          ↓ DONE / 超时 / 失败
                                       CancelAgent(如需) → CleanupWorkspace
```

```go
// 典型执行流程（executor.go）
func (e *Executor) RunTask(ctx context.Context, task *store.Task) error {
    ws, err := e.provider.SetupWorkspace(ctx, task.SessionID, task.RepoURL, task.Branch, task.EnvSpec)
    if err != nil { return err }
    defer e.wsMgr.Release(ws)  // 确保清理（风险点 3）

    if err := e.provider.InstallDeps(ctx, ws, task.EnvSpec); err != nil {
        return fmt.Errorf("install deps: %w", err)
    }

    handle, err := e.provider.StartAgent(ctx, ws, e.agent, task.Prompt, task.AgentOpts)
    if err != nil { return err }

    ticker := time.NewTicker(15 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return e.provider.CancelAgent(ctx, handle)
        case <-ticker.C:
            st, _ := e.provider.PollAgent(ctx, handle)
            if st.State == "done" || st.State == "failed" {
                return e.finalize(ctx, ws, task, st)
            }
            e.reportProgress(task, st.Progress)  // → Source.PostComment
        }
    }
}
```

**关键能力**：
- 超时取消：`ctx` 带 deadline，`CancelAgent` 强制终止
- 进度反馈：每 15s `PollAgent` → Source 评论到 Issue
- 用户中止：外部 cancel → `CancelAgent`

---

## 10. Services 完整定义

Services 在 Host 与 Docker 模式下处理方式不同：

| 维度 | Docker 模式 | Host 模式 |
|------|------------|----------|
| 启动 | sidecar 容器，网络互通 | 要求项目 dev 模式用 SQLite/内存，不依赖外部服务 |
| 持久化 | Volume 挂载，可选 | N/A（推荐 SQLite） |
| 健康检查 | `HealthCheck` 命令轮询容器 | N/A |
| 端口 | `HostPort` 映射到宿主 | 直接用宿主端口 |

**推荐策略**：优先要求项目的 dev/test 模式支持 SQLite 或内存数据库，避免有状态服务依赖。确需 MySQL/Redis 时，Docker 模式用 sidecar，Host 模式不保证可用。

```yaml
# env.yaml 中的 services 示例
services:
  - type: mysql
    version: "8.0"
    port: 3306
    host_port: 3306
    persistent: true
    volume: "mysql/dbx"          # 相对 base_dir
    health_check: "mysqladmin ping -h localhost"
    init_script: "CREATE DATABASE IF NOT EXISTS dbx_test;"
    env:
      MYSQL_ROOT_PASSWORD: "test"
```

---

## 11. Agent 记忆持久化：三层 + 容器外存储

**记忆绝对不放容器内**（容器销毁即丢失）。放宿主 SQLite，挂载进容器。

```
┌────────────────────────────────────────────────┐
│ L1 会话记忆（Session Memory）                     │
│   位置: agent_sessions 表（已有）                  │
│   内容: 当前 Issue 的多轮对话历史                  │
│   生命周期: Session TTL（默认168h）后归档          │
├────────────────────────────────────────────────┤
│ L2 项目记忆（Project Memory）★ 核心增量           │
│   位置: memory/{repo_hash}.db（每项目独立 SQLite）│
│   内容: 代码地图、模块职责、过往决策、踩过的坑     │
│   生命周期: 跟随项目，永久（除非项目删除）         │
│   注入: 新任务开始时检索相关记忆 → 塞进 prompt     │
├────────────────────────────────────────────────┤
│ L3 Agent 身份（Persona）                          │
│   位置: prompt_history 表（已有）                  │
│   内容: system prompt、人格、工作风格              │
│   生命周期: 版本化，永久                           │
└────────────────────────────────────────────────┘
```

### 11.1 L2 项目记忆数据模型

```sql
-- memory/{repo_hash}.db（WAL 模式）
CREATE TABLE memories (
    id          INTEGER PRIMARY KEY,
    type        TEXT,      -- code_map | decision | pitfall | convention
    scope       TEXT,      -- 文件路径 / 模块名 / 全局
    content     TEXT,      -- 记忆内容
    embedding   BLOB,      -- 可选：向量，用于语义检索
    task_id     INTEGER,   -- 来源任务
    created_at  TIMESTAMP,
    weight      REAL,      -- 重要度，用于淘汰
    superseded  BOOLEAN DEFAULT 0  -- 冲突失效标记
);
CREATE INDEX idx_mem_type ON memories(type);
CREATE INDEX idx_mem_scope ON memories(scope);
```

### 11.2 检索策略（Retrieval）

```go
type RetrieveQuery struct {
    Scope     string        // 文件路径/模块名，精确匹配优先
    Keywords  []string     // 关键词 LIKE 匹配
    Embedding []float32    // 可选：向量相似度（embedding=true 时生效）
    Limit     int
    Threshold float32
}

// 混合检索：scope 精确 > 关键词 > 向量（如启用）
// 返回按相关度 + weight 加权排序
func (m *ProjectMemory) Retrieve(ctx, repo string, q RetrieveQuery) ([]Memory, error)
```

### 11.3 淘汰策略（Eviction）

```yaml
memory:
  eviction:
    strategy: weight-decay   # weight-decay | lru
    max_entries: 10000       # 超过则触发淘汰
    decay_rate: 0.95         # 每次访问/写入后旧记忆 weight *= decay_rate
```

- `weight-decay`：每次新记忆写入或旧记忆命中时，其他记忆 weight 衰减；低于阈值的淘汰
- `lru`：按 `created_at`/最后访问时间淘汰最久未用的

### 11.4 冲突处理（Conflict）

当 Agent 产出与已有记忆 `scope` 相同但内容冲突的记忆时：

```go
func (m *ProjectMemory) Store(ctx, repo string, mem Memory) error {
    // 查找同 scope 同 type 的旧记忆
    if old := m.findConflict(repo, mem); old != nil {
        if mem.Supersedes(old) {
            m.markSuperseded(old.ID)  // 旧记忆标记 superseded=1，不直接删除（保留历史）
        }
    }
    return m.insert(mem)
}
```

旧记忆保留但标记失效，便于审计"为什么改了方案"。

### 11.5 记忆工作流

```
新任务进来
    ↓
Retrieve(repo, {scope: 当前模块, keywords: issue 关键词})
    ↓
"你之前修过这个模块，用的是 X 方案，注意 Y 坑" → 注入 prompt
    ↓
Agent 执行
    ↓
任务结束：让 Agent 输出「本次学到什么」（结构化 JSON）
    ↓
Store(repo, memories) → 冲突检测 → 写入/失效旧记忆
```

效果：Agent 有了跨任务的项目认知，从「每次都是新人」变「熟练工」。

---

## 12. 可观测性设计

### 12.1 任务状态机

```go
type TaskStatus string
const (
    TaskPending   TaskStatus = "pending"     // 已接收，等待调度
    TaskScheduled TaskStatus = "scheduled"   // 已分配 workspace
    TaskRunning   TaskStatus = "running"     // Agent 执行中
    TaskSuccess   TaskStatus = "success"     // 完成，PR 已建
    TaskFailed    TaskStatus = "failed"      // 执行失败
    TaskReviewing TaskStatus = "reviewing"   // 等待人工审核
)
```

每个阶段流转都落库（`tasks` 表 `status` 字段 + `updated_at`），这是未来多机扩展和运维的基础。

### 12.2 Metrics（Prometheus 风格）

```
gateway_tasks_total{status}              # 各状态任务计数
gateway_task_duration_seconds            # 任务耗时直方图
gateway_workspace_active                 # 当前活跃 workspace 数
gateway_provider_errors_total{provider}  # 各 Provider 错误数
gateway_memory_entries{repo}             # 各项目记忆条目数
```

### 12.3 日志

结构化 JSON，每个 task 绑定 `trace_id`，贯穿 Source → Scheduler → Provider → Agent → Memory。Agent 的原始输出单独落盘（`logs/{session_id}.log`），便于事后复盘。

### 12.4 失败重试与告警

- **重试**：Agent 执行失败（非逻辑错误）指数退避重试，最多 3 次
- **告警**：窗口内失败率超阈值 / Provider 健康检查连续失败 → 通知（日志 + 可选 webhook）
- **幂等**：同一 Issue 重复 webhook 不重复执行（去重表，见 modules 7/8）

---

## 13. 插件机制（MCP / Skills）

Agent 工具集不应硬编码。通过 MCP（Model Context Protocol）server 动态加载扩展能力：

```go
// internal/plugin/mcp.go
type MCPClient interface {
    ListTools() ([]Tool, error)
    CallTool(name string, args map[string]any) (*ToolResult, error)
}

type MCPRegistry struct {
    servers map[string]MCPClient  // 从配置加载
}

// AgentOptions.Tools 指定启用的 MCP server 名称
// Provider.StartAgent 时将对应 MCP 配置注入 Agent 启动参数
```

```yaml
plugins:
  mcp_servers:
    - name: filesystem
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
    - name: postgres
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-postgres", "${DATABASE_URL}"]
```

---

## 14. 配置 schema（v2）

```yaml
runtime:
  provider: host              # host | docker | firecracker
  base_dir: "/var/lib/gateway"
  concurrency:
    global: 4
    per_project: 2
    per_provider:             # 不同 Provider 不同上限
      host: 2
      docker: 4
      firecracker: 8
  storage:
    max_disk: "50GB"
    repo_retention: "30d"
    worktree_cleanup: "on_complete"
    cache_shared: true
  image:                      # 仅 provider=docker 时生效
    default_base: "ubuntu:22.04"
    prewarm: 0                # 预热镜像数（>0 时启用池）
  resource_quota:             # 每 task 资源限制
    cpu: "2"
    memory: "4Gi"
    timeout: "30m"

sources:
  - type: gitea
    url: "https://gitea.example.com"
    token: "${GITEA_TOKEN}"
  # - type: github
  #   token: "${GITHUB_TOKEN}"

agents:
  - name: claude-code
    type: claude-code
    model: "claude-opus-4"
    options:
      skip_perms: true
  - name: opencode
    type: opencode
    model: "deepseek-chat"

memory:
  project_memory: true
  embedding: false
  eviction:
    strategy: weight-decay
    max_entries: 10000
    decay_rate: 0.95

plugins:
  mcp_servers:
    - name: filesystem
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"]
```

---

## 15. 实施步骤

**Phase 1（核心价值）**
- `internal/runtime/workspace.go`: bare mirror + worktree 管理 + `Recover()`
- `internal/runtime/scheduler.go`: 三级信号量 + 分支锁
- `internal/runtime/host.go`: `HostProvider` 实现（SetupWorkspace/InstallDeps/StartAgent/RunCommand/CancelAgent）
- 接入现有 `runWriteTask`，替换裸 clone

**Phase 2（环境一致）**
- `internal/env/spec.go` + `builder.go`: manifest 加载 + 内容寻址镜像构建
- `internal/runtime/docker.go`: `DockerProvider` + 缓存挂载
- Services sidecar 支持

**Phase 3（记忆增量）**
- `internal/memory/project.go`: L2 项目记忆 SQLite + 混合检索 + 淘汰 + 冲突
- Agent Loop 结束钩子：产出「学到什么」结构化 JSON

**Phase 4（扩展与打磨）**
- `internal/source/`: GitHub/GitLab/CLI Source 实现
- `internal/agent/`: OpenCode/Custom Agent 实现
- `internal/plugin/`: MCP Registry
- LRU 磁盘回收、镜像预热池、devcontainer 兼容、可观测性接入

---

## 16. 风险与未决事项

### 风险点（已识别，需缓解）

| # | 风险 | 缓解措施 |
|---|------|---------|
| 1 | **SQLite 写锁**：多 task 并发写 `gateway.db` + `memory/{repo}.db` | 强制 WAL 模式 + `busy_timeout` + 连接池（写串行化） |
| 2 | **Host 模式安全性**：Agent 与 Gateway 同权限跑在宿主，prompt injection 成功即宿主级风险 | 配置显式标注"untrusted-unsafe"；非自用场景强制 Docker 模式；Agent 启动参数限制文件系统访问范围 |
| 3 | **worktree 清理可靠性**：Agent 崩溃/超时时 worktree 残留 | `defer Release` + 启动 `Recover()` 扫描孤儿 + 周期 GC |
| 4 | **单点故障**：Gateway 挂了全停，在飞任务丢失 | 记忆持久化使重启可恢复状态；在飞任务标记 failed 可重试；未来多实例共享存储 |

### 未决事项

- 服务的 Docker 特权问题（docker-in-docker vs 挂 docker.sock vs rootless）
- 有状态服务（MySQL/Redis）最省事方案：优先要求 dev 模式用 SQLite/内存
- 向量检索是否引入（L2 记忆的 embedding），还是先用关键词/scope 足够
- 多机横向扩展（未来）：Scheduler 与 WorkspaceManager 是否需要分布式化
- FirecrackerProvider 的 VM 内 Agent 启动通道（SSH vs agent socket vs vsock）

---

## 17. 接口速查

| 包 | 接口 | 核心方法 |
|----|------|---------|
| source | `Source` | FetchIssue / CreatePR / PostComment / WatchEvents |
| runtime | `Scheduler` | Submit |
| runtime | `WorkspaceManager` | Acquire / Release / GC / Recover |
| runtime | `Provider` | SetupWorkspace / InstallDeps / StartAgent / RunCommand / PollAgent / CancelAgent / CleanupWorkspace / HealthCheck |
| agent | `Agent` | Name / Start |
| env | `Builder` | Resolve |
| memory | `ProjectMemory` | Retrieve / Store / Invalidate / Evict |
| plugin | `MCPRegistry` | ListTools / CallTool |
