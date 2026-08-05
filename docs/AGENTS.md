# Matea Agent 指南

> 默认 backend 是 `builtin`，下载二进制、配置 LLM、创建 Agent 即可用。`hub-*` backend 是可选演进路径，用于把 Agent 执行外包给外部 Hub。

## Agent 是什么

Agent 是 Matea 在 Gitea 上的「功能账号」：每个 Agent 有 role、backend、Prompt 模板和可操作的仓库列表。用户通过在 Issue/PR 上 **Assign**、**Request Reviewer** 或 **@用户名** 触发 Agent。

## 默认路径：builtin backend

无需额外服务，推荐所有新用户从这里开始：

```text
Gitea Webhook → Matea Dispatcher → builtin Agent Loop → 写回 Gitea
```

- 多轮 Tool-Use 在 Matea 进程内执行
- 使用 `llm.providers` 中的 Provider/Model
- Matea 自带轻量级沙箱（目录隔离，不依赖 Docker）

### 推荐默认三件套

| Agent 名 | role | 触发方式 | 作用 |
|----------|------|----------|------|
| `matea-analyst` | `analyze` | Issue 上 Assign | 需求/Bug 分析，输出评论报告 |
| `matea-coder` | `coder` | Issue 上 Assign | 实现或修复，提 PR |
| `matea-review` | `review` | PR 上 Request Reviewer | 代码审查，输出审查评论 |

创建时点击「从模板创建」即可一键生成；命名可随时替换，不影响功能。

## 可选路径：hub-* backend

| backend | 状态 | 说明 |
|---------|------|------|
| `hub-opencode` | Phase 1 可用 | 将 Prompt/上下文提交到 OpenCode Hub 执行 |
| `hub-hermes` / `hub-openclaw` / `hub-api` | Phase 2 | 当前不可用，选择会明确报错 |

### hub-opencode 与 builtin 的差异

| 维度 | builtin | hub-opencode |
|------|---------|--------------|
| LLM 配置 | 读取 `llm.providers` | Hub 自管，Matea 不读取 `llm.providers` |
| 连接参数 | 无 | 在 `agents.backends.<name>` 统一设置 |
| Agent 页字段 | Provider/Model/Temperature/Loop Config 全显 | 仅显 System Prompt/User Template + `opencode_model`/`opencode_provider`/`opencode_agent` 覆盖 |
| 沙箱/PR | Matea 负责 | Matea 准备路径并负责 git/PR，OpenCode 在该路径工作 |

### 如何启用 hub-opencode

1. 在系统配置或 `config.yaml` 添加命名后端：

```yaml
agents:
  default: builtin
  backends:
    my-opencode:
      type: hub-opencode
      base_url: "http://localhost:8080"
      auth:
        username: "matea"
        password: "${OPENCODE_PASSWORD}"
      workspace_mode: matea_path   # Phase 1 仅支持 matea_path
```

2. 创建 Agent 时 **Coding Backend** 选择 `my-opencode`。
3. （可选）在 Agent 编辑页覆盖提交到 OpenCode 的模型/Provider。

## Role 是闭集

创建 Agent 必须三选一：

- `analyze`：分析
- `coder`：实现/修复
- `review`：审查

同一 role 可创建多个实例（如 `matea-coder-go`、`matea-coder-fe`），按仓库/技术栈特化。**不引入 capabilities 概念**——role 就是职责真相。

## 触发方式速查

| 场景 | 操作 |
|------|------|
| 让分析师看 Issue | Issue 上 Assign `matea-analyst` |
| 让开发者实现 Issue | Issue 上 Assign `matea-coder` |
| 让审查者看 PR | PR 上 Request Reviewer `matea-review` |
| 续作/追问 | Issue/PR 评论中 @Agent 用户名 |
| 强制开发（跳过分析） | 评论 `/dev` |
| 重置工作流 | 评论 `/matea reset` |

> v2 已弃用 `ai:analyze` / `ai:solve` 等 Label 触发及 routes 配置。

## 账号自动创建

`gitea.auto_provision` 默认为 `true`，创建/更新 Agent 时 Matea 会自动创建同名 Gitea 用户并签发 Token。若环境要求手动管理账号，设置 `gitea.auto_provision: false`，然后手动创建同名用户并在 Agent 编辑页填入 Gitea Token。
