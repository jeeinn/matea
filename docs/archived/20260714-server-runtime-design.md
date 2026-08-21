# 服务器端 Agent 运行时设计（Server Runtime Design）

> 状态：已归档（原设计草案 v0.1 / v1）  
> 现行决策见 [server-runtime-design-v4.md](20260714-server-runtime-design-v4.md)。  
> 目标：为 Gitea Agent Gateway 的服务器端 Agent 执行提供可复现、可并发、可持久化记忆的运行时。  
> 部署目标：Linux（可与 Gitea 同机或独立部署）。定位：个人/小团队自用，最终开源。

## 1. 背景与问题

当前 sandbox 仅做目录隔离 + 命令白名单，Agent clone 到的是**裸源码**，没有编译/运行环境（`go mod download`、`npm install`、数据库等均缺失），Agent 无法真正验证代码。同时缺少并发调度、多分支隔离、环境一致性保障和跨任务记忆。

行业现状：没有开源项目把「AI Coding Agent + 可复现开发环境 + 服务化部署」三者打通。这是本项目的机会点。

## 2. 核心设计哲学

**严格区分「临时层」与「持久层」**：

```
┌─────────────────────────────────────────────────┐
│ 宿主机（持久层，永不随任务销毁）                    │
│  repos/     bare mirror（对象库共享）              │
│  memory/    Agent 记忆 SQLite（每项目一个）         │
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

## 3. 存储布局

```
/var/lib/gateway/
├── repos/{repo_hash}/          # bare mirror（对象库，多分支共享）
├── worktrees/{session_id}/     # 任务工作目录（临时，用完删）
│   └── repo/
├── images/                     # 环境镜像（内容寻址，复用）
├── memory/{repo_hash}.db       # 项目记忆（持久，挂进容器）
├── cache/                      # 依赖缓存（跨项目共享）
│   ├── go-mod/
│   ├── npm/
│   └── cargo/
└── gateway.db                  # 主库（session/task/agent，已有）
```

`repo_hash = sha256(repo_full_name)` 前 16 位。

## 4. 并发与多分支：git worktree

### 4.1 为什么用 worktree

不要每个任务 `git clone` 一次（磁盘炸、速度慢）。改用 `git clone --mirror` + `git worktree`：

- 每个项目只有**一份 bare 对象库**（`repos/{repo_hash}/`）
- 每个任务从 mirror 开一个 **worktree**（工作目录），共享对象库

```bash
# 项目首次：建 bare mirror
git clone --mirror https://gitea/user/dbx repos/{repo_hash}

# 每个任务：从 mirror 开 worktree（秒级，几乎不占额外磁盘）
git -C repos/{repo_hash} fetch --all --prune
git -C repos/{repo_hash} worktree add ../../worktrees/{session_id}/repo ai/issue-42

# 任务结束：清理
git -C repos/{repo_hash} worktree remove ../../worktrees/{session_id}/repo
```

### 4.2 worktree 天然解决的问题

| 场景 | 处理方式 |
|------|---------|
| 同项目**不同分支**并发 | 各开一个 worktree，共享对象库，完全并行 |
| 同项目**同一分支**并发 | git worktree 强制一个分支只能 checkout 一处 → 天然串行锁 |
| 多项目 | 各自 bare mirror，互不干扰 |
| 磁盘 | 对象库只存一份，worktree 只有工作树，省 90%+ 空间 |

### 4.3 三级信号量调度

```go
type Scheduler struct {
    globalSem   chan struct{}            // 全局最大并发（防 OOM）
    perProject  map[string]chan struct{} // 每项目并发上限（防单项目吃满）
    branchLock  sync.Map                 // (repo, branch) → 互斥锁
}
```

- 全局：`min(CPU核数, 内存/单任务预估)`，默认 4
- 每项目：默认 2，防止一个大项目饿死其他
- 分支锁：`(repo,branch)` 粒度，同分支任务排队，不同分支放行

## 5. 多项目：内容寻址 + LRU 回收

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

## 6. 环境一致性：环境即代码 + 内容寻址镜像

### 6.1 环境清单来自项目本身

```
仓库根/
├── .gateway/env.yaml          ← 优先
└── .devcontainer/             ← 或复用社区标准（次选）
    └── devcontainer.json
```

`.gateway/env.yaml` 示例：

```yaml
name: dbx-dev
base: ubuntu:22.04
tools:
  - go: "1.26"
  - node: "22"
  - python: "3.12"
system:
  - libsqlite3-dev
  - clang
services:
  - type: mysql
    version: "8.0"
  - type: redis
    version: "7"
env:
  DATABASE_URL: "mysql://root@localhost:3306/dbx_test"
  REDIS_URL: "redis://localhost:6379"
```

无 manifest 时回退到 `DefaultSpec`（base + git + 常用工具链）。

### 6.2 内容寻址：一致性的根

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

### 6.3 容器启动挂载

```bash
docker run \
  -v worktrees/{session}/repo:/workspace \
  -v cache/go-mod:/go/pkg/mod \          # 共享依赖缓存
  -v memory/{repo_hash}.db:/memory/agent.db \  # 挂记忆
  gateway-env:{hash}
```

### 6.4 降级：host 模式

无 Docker 时 `mode: host`，直接用宿主工具链 + `go mod download` 前置，牺牲隔离换简单。配置项切换。

## 7. Agent 记忆持久化：三层 + 容器外存储

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

### 7.1 L2 项目记忆数据模型

```sql
-- memory/{repo_hash}.db
CREATE TABLE memories (
    id          INTEGER PRIMARY KEY,
    type        TEXT,      -- code_map | decision | pitfall | convention
    scope       TEXT,      -- 文件路径 / 模块名 / 全局
    content     TEXT,      -- 记忆内容
    embedding   BLOB,      -- 可选：向量，用于语义检索
    task_id     INTEGER,   -- 来源任务
    created_at  TIMESTAMP,
    weight      REAL       -- 重要度，用于淘汰
);
CREATE INDEX idx_mem_type ON memories(type);
CREATE INDEX idx_mem_scope ON memories(scope);
```

### 7.2 记忆工作流

```
新任务进来
    ↓
从 memory/{repo}.db 检索相关记忆（按 scope/关键词/向量）
    ↓
"你之前修过这个模块，用的是 X 方案，注意 Y 坑" → 注入 prompt
    ↓
Agent 执行
    ↓
任务结束：让 Agent 输出「本次学到什么」→ 写回 memory DB
```

效果：Agent 有了跨任务的项目认知，从「每次都是新人」变「熟练工」。

### 7.3 持久化保障

memory DB 在宿主，容器仅挂载读写。容器崩溃、重建、升级，记忆都在。备份 = 拷贝 SQLite 文件。

## 8. 运行时组件与接口

### 8.1 Provider 模式（借鉴 DevPod）

运行时后端通过 `Provider` 接口抽象，实现宿主机 / Docker / Firecracker 的统一封装：

```go
// internal/runtime/provider.go
// 核心抽象：每个运行时后端实现此接口
type Provider interface {
    // 创建并准备一个工作空间（worktree + 环境）
    SetupWorkspace(ctx context.Context, sessionID, repoURL, branch string, spec *EnvSpec) (*Workspace, error)
    
    // 在工作空间内执行命令
    RunCommand(ctx context.Context, ws *Workspace, cmd *Command) (*Result, error)
    
    // 清理工作空间
    CleanupWorkspace(ws *Workspace) error
    
    // 健康检查
    HealthCheck(ctx context.Context) error
}

type Workspace struct {
    ID         string    // sessionID
    Path       string    // 工作目录绝对路径
    RepoPath   string    // bare mirror 路径（如果有）
    Branch     string
    EnvSpec    *EnvSpec
    CreatedAt  time.Time
}

type Command struct {
    Cmd   string
    Args  []string
    Env   map[string]string
    Dir   string  // 工作目录，默认 ws.Path
    Timeout time.Duration
}

type Result struct {
    Stdout     string
    Stderr     string
    ExitCode   int
    Duration   time.Duration
}
```

**后端实现优先级**：

| Provider | 复杂度 | 隔离级别 | 何时实现 |
|----------|-------|---------|---------|
| `HostProvider` | 最低 | 无 | **Phase 1**（今明两天） |
| `DockerProvider` | 中 | 命名空间+Cgroups | **Phase 2**（1-2周） |
| `FirecrackerProvider` | 高 | KVM 硬件虚拟化 | Future（有不可信代码需求时） |

**Provider 选择由配置决定**：

```yaml
runtime:
  provider: host      # 先用 host 模式跑通
  # provider: docker  # Phase 2 切换
  # provider: firecracker  # 未来可选
```

### 8.2 其他核心接口

```go
// internal/runtime/scheduler.go
type Scheduler interface {
    Submit(ctx context.Context, task *store.Task) error
}

// internal/runtime/workspace.go
// WorkspaceManager 内部被 Provider 调用，管理 worktree 生命周期
type WorkspaceManager interface {
    Acquire(session string, repo, branch string) (*Workspace, error)  // worktree add
    Release(ws *Workspace) error                                       // worktree remove
    GC() error                                                         // LRU 回收
}

// internal/env/spec.go
type EnvSpec struct {
    Name     string
    Base     string
    Tools    map[string]string
    System   []string
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

// internal/memory/project.go
type ProjectMemory interface {
    Retrieve(scope, query string, limit int) ([]Memory, error)  // 任务前注入
    Store(m Memory) error                                        // 任务后写回
}
```

## 9. 配置 schema（新增段）

```yaml
runtime:
  provider: host              # host | docker | firecracker
  base_dir: "/var/lib/gateway"
  concurrency:
    global: 4
    per_project: 2
  storage:
    max_disk: "50GB"
    repo_retention: "30d"
    cache_shared: true
  image:                      # 仅 provider=docker 时生效
    default_base: "ubuntu:22.04"
    prewarm: 0                # 预热镜像数（>0 时启用池）
memory:
  project_memory: true        # 启用 L2 项目记忆
  embedding: false            # 是否启用向量检索
```

## 10. 实施步骤

**Phase 1（核心价值，1-2 周）**
- `internal/runtime/workspace.go`: bare mirror + worktree 管理
- `internal/runtime/scheduler.go`: 三级信号量调度
- 接入现有 `runWriteTask`，替换裸 clone

**Phase 2（环境一致，1-2 周）**
- `internal/env/spec.go` + `builder.go`: manifest 加载 + 内容寻址镜像构建
- `internal/runtime/runner.go`: docker 容器执行 + 缓存挂载
- host 模式降级

**Phase 3（记忆增量，1 周）**
- `internal/memory/project.go`: L2 项目记忆 SQLite + 检索注入 + 写回
- Agent Loop 结束钩子：产出「学到什么」

**Phase 4（打磨）**
- LRU 磁盘回收、镜像预热池、devcontainer 兼容、可观测性（指标/日志）

## 11. 未决事项

- 服务的 Docker 特权问题（docker-in-docker vs 挂 docker.sock vs rootless）
- 有状态服务（MySQL/Redis）如何最省事：优先要求项目 dev 模式用 SQLite/内存替代
- 向量检索是否值得引入（L2 记忆的 embedding），还是先用关键词/scope 足够
- 多机横向扩展（未来）：调度器与 workspace 是否需要分布式化
