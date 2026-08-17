# 远端 Hub 大脑部署方案 — 数据流与场景说明

> 说明：本文档描述 Matea 支持「远端 / 非同机部署 Hub 大脑」的三种 transport 模式、数据流转，以及各场景需要注意的问题和解决方案。

---

## 一、三种 Transport 总览

```mermaid
flowchart TB
    subgraph Matea["Matea Gateway（编排+写回）"]
        W[Webhook 接收]
        R[Router 选 backend]
        P[prepareWriteWorkspace<br/>本地 clone / 建分支]
        D[Dispatcher / Executor]
        F[finalizeWriteChanges<br/>commit / push / PR]
    end

    subgraph Hub["远端 Hub 大脑"]
        H1["shared_path（OpenCode 过渡）"]
        H2["git_sync（推荐默认）"]
        H3["mcp（隔离增强）"]
    end

    subgraph Git["Gitea / Git"]
        G[(origin repo)]
    end

    W --> R --> D
    D --> P

    P -->|本地目录路径| H1
    H1 -->|改本地文件| P
    P --> F

    P -->|TaskContext + GitSyncInfo| H2
    H2 -->|clone / 改 / commit / push| G
    G -->|fetch 拉回| P
    P --> F

    P -->|TaskContext + ToolAccessGrant| H3
    H3 -->|MCP 调用 read/write/run_command| P
    P --> F

    F -->|post comment / create PR| G
```

### 三种模式取舍

| Transport | 数据怎么到 Hub | 代码是否离开 Matea | 对 Hub 的部署要求 | 适用场景 |
|---|---|---|---|---|
| `shared_path` | 传本地绝对路径 | 否（同一文件系统） | 必须与 Matea 同机/共享卷 | OpenCode 同机过渡 |
| `git_sync` ⭐ | TaskContext 传 clone URL + branch | 是（Hub 自管沙箱） | 能访问 Gitea 即可 | **推荐默认** |
| `mcp` | TaskContext 传 MCP endpoint + token | 否（文件级 RPC） | 必须是 MCP client | 高隔离/跨组织 |

---

## 二、git_sync 详细数据流（推荐默认）

```mermaid
sequenceDiagram
    autonumber
    participant G as Gitea
    participant M as Matea Gateway
    participant DB as SQLite
    participant H as 远端 Hub
    participant O as origin/Gitea

    G->>M: webhook (issue/PR event)
    M->>M: Router 匹配 agent → backend=hub-hermes<br/>transport=git_sync
    M->>DB: create task (pending)
    M->>M: prepareWriteWorkspace()
    Note over M: 本地 clone repo、创建分支<br/>ai/dev/issue-123<br/>（本地工作区只用于最终 finalize）

    M->>H: Submit(TaskContext + GitSyncInfo)
    Note over M,H: GitSyncInfo 含：<br/>clone_url（带短期 token）<br/>branch_name / base_branch<br/>commit_user / commit_email

    H->>DB: Matea 保存 HubHandle<br/>(backend, remote_id, idempotency_key)

    loop Poll every 2s
        M->>H: Poll(handle)
        H-->>M: State=running
    end

    H->>O: git clone
    H->>O: git checkout -b ai/dev/issue-123
    Note over H: Hub 自管 AgentLoop<br/>改代码、commit
    H->>O: git push origin ai/dev/issue-123

    M->>H: Poll(handle)
    H-->>M: State=done + summary

    M->>M: SyncBack()
    Note over M: git fetch origin ai/dev/issue-123<br/>git checkout ai/dev/issue-123<br/>此时本地 git status 干净<br/>（改动已在 commit 中）

    M->>M: finalizeWriteChanges()
    Note over M: git.HasChanges() == false<br/>走 push(no-op) → create PR 分支

    M->>O: create PR ai/dev/issue-123 → base
    M->>G: post comment / PR body
    M->>DB: update task done
```

### git_sync 关键边界说明

| 步骤 | 谁负责 | 注意问题 | 解决方案 |
|---|---|---|---|
| 1. 本地 clone / 建分支 | Matea | 远端 Hub 也需要知道分支名 | `TaskContext.GitSyncInfo.BranchName` |
| 2. 给 Hub 传 git 凭据 | Matea | Hub 拿到 token 有泄露风险 | 短期 token / deploy key / SSH key<br/>任务结束后撤销 |
| 3. Hub clone & push | Hub | Hub 必须能访问 Gitea | 网络白名单 / VPN / TLS |
| 4. Matea fetch 拉回 | Matea | 分支可能被覆盖/冲突 | Hub 用 force-with-lease<br/>Matea 校验 remote head |
| 5. finalize 创建 PR | Matea | Hub 没 push 或 push 失败 | Poll 阶段必须确认 done 前 push 成功 |
| 6. 任务重试 | Matea | 重复 Submit 会创建多个远程 run | `IdempotencyKey` + `HubHandle` 持久化 |

---

## 三、shared_path 数据流（OpenCode 同机过渡）

```mermaid
sequenceDiagram
    autonumber
    participant G as Gitea
    participant M as Matea Gateway
    participant DB as SQLite
    participant S as OpenCode Sidecar
    participant FS as 共享文件系统

    G->>M: webhook
    M->>M: backend=hub-opencode<br/>transport=shared_path
    M->>M: prepareWriteWorkspace()
    M->>FS: 本地 clone 到 /opt/matea/workspace/task_123

    M->>S: POST /session?directory=/opt/matea/workspace/task_123
    Note over M,S: sidecar 与 Matea 同机或共享卷

    S->>FS: 直接读写文件
    S-->>M: summary

    M->>M: finalizeWriteChanges()
    Note over M: git add / commit / push / PR
    M->>G: post comment / create PR
```

### shared_path 注意事项

| 问题 | 解决方案 |
|---|---|
| 远端 sidecar 无法访问 Matea 本地路径 | 必须同机或 NFS/分布式卷<br/>或迁移到 git_sync |
| 任务中断后 sidecar 改了文件但没 push | 非 session 任务直接失败（当前代码已如此处理）<br/>session 任务复用同一目录可恢复 |
| 多个 sidecar 实例共享状态 | 按 task_id 隔离目录 |

---

## 四、mcp 数据流（隔离增强）

```mermaid
sequenceDiagram
    autonumber
    participant G as Gitea
    participant M as Matea Gateway
    participant DB as SQLite
    participant H as 远端 Hub
    participant MCP as Matea MCP Server

    G->>M: webhook
    M->>M: backend=hub-mcp<br/>transport=mcp
    M->>M: prepareWriteWorkspace()
    M->>M: 启动/复用 MCP Server

    M->>H: Submit(TaskContext + ToolAccessGrant)
    Note over M,H: ToolAccessGrant 含：<br/>MCP endpoint<br/>短期 token（5-15min）<br/>AllowedTools 白名单

    loop AgentLoop round
        H->>MCP: read_file / write_file / run_command
        MCP->>M: 本地沙箱执行
        MCP-->>H: tool result
    end

    H-->>M: State=done + summary

    M->>M: finalizeWriteChanges()
    M->>G: post comment / create PR
```

### MCP 注意事项

| 问题 | 解决方案 |
|---|---|
| 每个 tool call 跨网，延迟高 | 仅用于高隔离场景，不作为默认 |
| MCP session 需要稳定双向连接 | HTTP SSE / WebSocket / Streamable HTTP |
| Hub 必须是 MCP client | 新 Hub 接入需要写 MCP adapter |
| 工具调用可能被滥用 | `AllowedTools` 白名单 + 短期 token + 审计 |

---

## 五、三种写任务场景对比

```mermaid
flowchart LR
    A[写任务到达] --> B{选择 transport}

    B -->|shared_path| C[OpenCode 同机]
    C --> C1[问题：必须共享文件系统]
    C1 --> C2[方案：仅作过渡]

    B -->|git_sync| D[远端 Hub 自管沙箱]
    D --> D1[问题：Hub 需要 git 凭据]
    D1 --> D2[方案：短期 token / deploy key]
    D --> D3[问题：分支冲突]
    D3 --> D4[方案：force-with-lease + 唯一分支名]

    B -->|mcp| E[远端大脑 + 本地工具]
    E --> E1[问题：延迟高]
    E1 --> E2[方案：仅高隔离场景]
```

---

## 六、核心状态机

```mermaid
stateDiagram-v2
    [*] --> Pending: webhook → task enqueue
    Pending --> Preparing: Executor 取出任务
    Preparing --> Submitted: prepareWriteWorkspace 完成
    Submitted --> Running: Hub Submit 成功
    Running --> Running: Poll 等待
    Running --> SyncBack: Hub done
    SyncBack --> Finalizing: git fetch / checkout
    Finalizing --> Done: PR created / comment posted
    Finalizing --> Failed: push / PR 失败
    Running --> Failed: Hub 失败
    Running --> Canceled: 任务取消

    Submitted --> Submitted: Matea 重启后<br/>从 HubHandle 恢复
```

---

## 七、核心改造文件索引

| 文件 | 改造内容 |
|---|---|
| `internal/agents/workspace_transport.go`（新增） | `WorkspaceTransport` 接口及三种实现 |
| `internal/agents/hub_backend.go` | `TaskContext` 增加 `GitSyncInfo`、`ToolAccessGrant` 扩展 |
| `internal/agents/runner_write.go` | 写任务按 transport 分发，git_sync 路径加 `SyncBack` |
| `internal/agents/hub_run.go` | `runViaHub` 支持写任务分支进入 finalize |
| `internal/agents/write_workspace.go` | `prepareWriteWorkspace` 按 transport 分发 |
| `internal/agents/backends/hermes/hermes.go` | Hermes 接收 `GitSyncInfo`，自管 clone/push |
| `internal/agents/opencode_http.go` | 保留 shared_path，新增 git_sync 选项 |
| `internal/config/schema.go` | `workspace_transport` 允许 `git_sync`、`mcp` |

---

## 八、一句话总结

> **Git 是 Matea 与远端 Hub 之间的唯一事实来源**。推荐默认走 `git_sync`：Matea 负责 clone/建分支/最终 PR，远端 Hub 负责编码并 push 回 origin；`shared_path` 留给 OpenCode 同机过渡；`mcp` 留给不允许 Hub 碰 Git 的高隔离场景。
