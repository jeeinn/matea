# Matea

> Matea — your AI dev mate for Gitea.

[![CI](https://github.com/jeeinn/matea/actions/workflows/ci.yml/badge.svg)](https://github.com/jeeinn/matea/actions/workflows/ci.yml)
[![Release](https://github.com/jeeinn/matea/actions/workflows/release.yml/badge.svg)](https://github.com/jeeinn/matea/actions/workflows/release.yml)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue)](#license)
[![Tests](https://img.shields.io/badge/Tests-55+-brightgreen)](#测试)

AI Agent 网关 —— 通过 Gitea Webhook 事件驱动，将 AI Agent 嵌入 Gitea 工作流。支持多种 Agent 类型，通过 Tool-Use（Function Calling）与代码库交互，自动完成代码分析、审查、开发和修复任务。

## 功能特性

- 🤖 **多种 Agent 类型** —— 需求分析、代码审查、评论交互、Issue 开发、Bug 修复
- 🔧 **Tool-Use Agent** —— 基于 LLM Function Calling，通过 read_file / write_file / search_code / run_command / apply_diff 等工具理解和修改代码
- 🔒 **轻量级沙箱** —— 目录隔离 + 命令白名单 + 超时控制 + 审计日志（不依赖 Docker）
- 🎯 **可配置模板** —— 支持自定义 System Prompt 和 User Template，支持 Go template 语法
- 🌐 **Web UI** —— Vue 3 + Element Plus 管理界面，Dashboard / Agent 管理 / 任务列表 / Prompt 编辑
- 📡 **多 LLM 支持** —— OpenAI 兼容（DeepSeek / Qwen / Moonshot / Ollama）+ Anthropic Claude
- ⚙️ **灵活配置** —— Agent 级别 loop_config 覆盖（最大迭代、Token 限制、超时控制）

## 架构概览

```
Gitea Webhook → Handler (签名验证 + 去重)
  → Dispatcher (路由匹配 + 任务队列 + 并发执行)
    → Runner (Analyze / Review / Interaction / Dev / Bugfix)
      → Agent Loop (多轮 LLM 对话 + Tool Call)
    → 写回 Gitea (评论 / PR)
```

### 核心组件

| 包 | 职责 |
|---|------|
| `internal/webhook` | HTTP Handler、签名验证、事件解析、去重 |
| `internal/dispatcher` | Router（事件→Agent 匹配）、TaskQueue（SQLite 持久化）、Executor（并发控制） |
| `internal/agents` | Runner 实现：AnalyzeRunner、ReviewRunner、InteractionRunner、DevRunner、BugfixRunner |
| `internal/agent` | Tool-Use Agent Loop：ToolRegistry + 多轮对话循环 |
| `internal/llm` | Provider 接口 + OpenAI 兼容客户端 + Anthropic 客户端 |
| `internal/store` | SQLite 数据库（WAL 模式）、自动迁移、CRUD |
| `internal/sandbox` | 工作空间隔离、命令白名单、Git 操作、审计日志 |
| `internal/gitea` | Gitea API 客户端（Issue / PR / 评论 / 文件） |
| `internal/api` | 管理 REST API + JWT 认证 |
| `internal/config` | YAML 配置加载 + 环境变量展开 |

## 快速开始

### 1. 下载并运行（推荐）

从 [Releases](https://github.com/jeeinn/matea/releases) 下载对应平台**单二进制**（如 `matea-linux-amd64` / `matea-windows-amd64.exe`），无需预置 `config.yaml`：

```bash
chmod +x matea-linux-amd64   # Linux / macOS
./matea-linux-amd64
# Windows: matea-windows-amd64.exe
```

首次启动若本地没有 `config.yaml`，会**自动写入最小 bootstrap**（监听 `8080`、数据目录 `./data/...`、随机 `auth.jwt_secret`、日志落盘 `./data/matea.log`），并打印 Web 访问地址。

然后：

1. 打开 http://127.0.0.1:8080  
2. 使用 `admin` / `admin123` 登录，**立即修改密码**（强制）  
3. 在 **系统配置** 填写 Gitea / LLM（见下一步）  

> **安全警示**：bootstrap 会随机生成 `jwt_secret`；Token / API Key 勿写入 git。未完成 Gitea/LLM 配置时，顶栏会显示 Setup 引导。Web UI **系统配置**写入数据库后优先于文件。

生产部署、systemd 等见 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)。

### 2. Web UI 配置（推荐顺序）

| 步骤 | 页面 | 操作 |
|------|------|------|
| ① | 登录 | `admin` / `admin123` → **立即改密** |
| ② | **系统配置 → Gitea 连接** | 填写 Gitea 地址、管理员 Token（需 `write:admin`）、Webhook 密钥 → **测试 Gitea 连接** → **保存全部** |
| ③ | **系统配置 → LLM 配置** | 填写 Provider JSON 与默认模型 → **测试 LLM 连接** → **保存全部** |
| ④ | **Agent 管理** | 新建 analyze / coder / review 三个 Agent，勾选目标仓库 |
| ⑤ | Gitea 仓库 | 将 Agent 用户加为协作者；配置 Webhook（见下文） |

**Gitea 管理员 Token 所需权限**：`write:admin`（创建 Agent 用户）、`write:repository`、`read:repository`。

### 3. 配置 Gitea Webhook

密钥：**自拟一串**，填入 Matea「Webhook 密钥」与 Gitea Webhook「密钥」（两边一致）。

| 范围 | 配置入口 | 适用场景 |
|------|----------|----------|
| **全站（推荐）** | **站点管理 → Webhooks** | 任意仓库事件都推到 Matea |
| 组织 | 组织设置 → Webhooks | 仅某组织下仓库 |
| 仓库 | 仓库设置 → Webhooks | 单仓细粒度 |

| 项 | 值 |
|----|-----|
| URL | `http://<matea-host>:8080/webhook/gitea` |
| Secret | 与系统配置中的 Webhook 密钥一致 |
| 事件 | Issues、Issue Comment、Pull Request、Pull Request Comment |

> 远程 Gitea 无法访问你本机的 `localhost`，需使用公网 IP、内网穿透，或将 Matea 部署到 Gitea 同机。  
> 更细说明见 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#配置-webhook)。

### 4. 验证工作流

1. 在 Gitea 创建 Issue，Assign `analyze-agent` → 等待分析评论  
2. Assign `coder-agent` → 等待 PR 创建  
3. 在 PR 上 Request `review-agent` → 等待审查评论  

详细联调清单见 [docs/archived/20260709-v2-gitea-integration-checklist.md](docs/archived/20260709-v2-gitea-integration-checklist.md)。

### 从源码构建（可选）

需要：Go 1.26+、Node.js 18+（构建前端；产物经 `go:embed` 打进二进制）。

```bash
git clone https://github.com/jeeinn/matea.git
cd matea

cd web && npm install && npm run build && cd ..
go build -o matea .
./matea          # 同样：无 config.yaml 时自动生成
```

也可预先 `cp config.example.yaml config.yaml` 再编辑；完整选项见 `config.full-example.yaml`。  
健康检查：http://127.0.0.1:8080/health

### 备选：纯配置文件方式

若不使用 Web UI，可直接编辑 `config.yaml`（支持 `${VAR}` / `${VAR:-default}` 展开）：

```yaml
gitea:
  url: "https://your-gitea.example.com"
  admin_token: "${GITEA_ADMIN_TOKEN}"
  webhook_secret: "your-webhook-secret"

llm:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com/v1"
      api_key: "${DEEPSEEK_API_KEY}"
```

## 配置说明

**推荐路径**：直接运行二进制 → Web 登录改密 → **系统配置** 写入 Gitea / LLM（存库，优先于文件）。

参考文件：[config.example.yaml](config.example.yaml)（精简示例）、[config.full-example.yaml](config.full-example.yaml)（完整注释）。主要配置段：

| 配置段 | 说明 |
|--------|------|
| `server` | 监听地址和端口 |
| `gitea` | Gitea 连接信息（URL、管理员 Token、Webhook 密钥） |
| `workspace` | Agent 工作目录配置 |
| `dispatcher` | 并发数、重试、429 退避、队列大小（无全局任务超时） |
| `llm` | LLM Provider 与连通性默认（provider/model） |
| `agents` | Agent 默认预算（tokens/timeout/temperature）与 Loop 总超时 |
| `auth` | JWT 认证配置 |
| `api` | 管理 API 认证 Token |

### Agent LLM 预算与超时

```yaml
agents:
  defaults:
    max_output_tokens: 8192   # 每次调用输出上限（单次 + Loop 每轮共用；无模型元数据时兜底）
    max_input_tokens: 115200  # 输入上限兜底（≈128K×90%；有模型元数据且 Agent=0 时走模型）
    temperature: 0.3
    timeout: "5m"             # 单次任务总超时（analyze/review/reply）
  loop:
    max_iterations: 20
    total_timeout: "30m"      # 仅多轮任务总超时（solve/fix_bug）
    iteration_interval: 3
    no_progress_limit: 3      # 连续 N 轮无进展退出（0=关闭）
    verify_commands: []       # 编码后、commit/PR 前执行的校验命令
```

任务超时由 Agent 配置控制（不再使用 `dispatcher.timeout`）。单个 Agent 可覆盖 `max_output_tokens` / `max_input_tokens` / `timeout` / `loop_config`。

### Harness 验证门禁

| 配置项 | 说明 |
|--------|------|
| `no_progress_limit` | 连续 N 轮工具调用后工作区指纹（`git status --porcelain`）不变则退出；0 = 关闭检测（config.full-example.yaml 示例为 3；YAML 省略时为 0 即关闭） |
| `verify_commands` | 编码完成后、commit/PR 前执行的 shell 命令列表；任一命令失败则任务 failed，不写回 PR；空数组 = 跳过校验 |

**示例**：

```yaml
agents:
  loop:
    no_progress_limit: 3
    verify_commands:
      - "go test ./..."
      - "go vet ./..."
```

单个 Agent 可通过 `loop_config` 覆盖系统默认值，支持设置为空数组显式禁用校验。

## 开发

### 前后端分离开发

```bash
# 终端 1: 后端（无 config.yaml 时自动生成）
go build -o matea . && ./matea

# 终端 2: 前端（热更新）
cd web && npm run dev
```

前端开发服务器运行在 `http://localhost:3001`，API 请求自动代理到后端。

### 测试

不连真实 Gitea / LLM，用仓库自带 Mock 即可验证核心逻辑：

```bash
# 全部测试
go test ./... -count=1

# 单元测试
go test ./internal/... -v -count=1

# 集成测试（Mock Gitea / Mock LLM）
go test ./tests/integration/ -v -count=1

# 单个包
go test ./internal/sandbox/ -v -count=1

# 覆盖率
go test ./... -coverprofile=coverage.out && go tool cover -html=coverage.out
```

测试框架：`testify`（assert + require）。集成测试使用 `TestEnv` 提供内存 SQLite、Mock Gitea 和 Mock LLM。  
本机真实 Gitea E2E 见 [scripts/TESTING.md](scripts/TESTING.md)。

### 代码质量

```bash
go fmt ./...
go vet ./...
```

## 项目结构

```
├── main.go                 # 入口：HTTP 服务器 + graceful shutdown
├── config.example.yaml     # 精简配置示例（可选；无文件时自动 bootstrap）
├── config.full-example.yaml # 完整配置参考
├── internal/
│   ├── agent/              # Tool-Use Agent Loop + 工具定义
│   ├── agents/             # Runner 实现 + Manager + Registry
│   ├── api/                # 管理 REST API + 认证中间件
│   ├── auth/               # JWT + bcrypt
│   ├── config/             # YAML 配置加载 + 环境变量展开
│   ├── dispatcher/         # TaskQueue + Executor + v2 流水线
│   ├── workflow/           # Event Resolver + 状态机 + 门禁 + Session + 生命周期
│   ├── gitea/              # Gitea API 客户端
│   ├── llm/                # LLM Provider 接口 + 实现
│   ├── sandbox/            # 沙箱（目录隔离 + 命令执行 + Git 操作）
│   ├── store/              # SQLite 数据库 + 自动迁移
│   └── webhook/            # Webhook HTTP Handler
├── web/                    # Vue 3 + Element Plus 前端
├── tests/                  # 集成测试
├── docs/                   # 设计文档
└── scripts/                # 工具脚本
```

## Agent 角色（v2 Assign 模型）

在 Matea 中注册多个功能性 Agent，每个 Agent 设置 `role` 并在 Gitea 上作为协作者：

### Role 定义

- **`role` 为闭集**：`analyze` | `coder` | `review`（创建任何 Agent 必须三选一）
- **Agent 实例数不限**：同一 role 可创建多个 Agent 实例，按仓库/技术栈特化（如 `matea-coder-go`、`matea-coder-fe`）
- **不使用 capabilities 概念**：role 是 Agent 职责的唯一真相，不引入 capabilities 作为别名

### Role 类型与触发

| role | 触发方式 | 说明 |
|------|----------|------|
| `analyze` | Issue 上 **Assign** analyze Agent | 需求/Bug 分析，输出评论报告 |
| `coder` | Issue 上 **Assign** coder Agent | 实现或修复（Issue 带 `bug` 标签时用 fix 系 Prompt），提 PR |
| `review` | PR 上 **Request Reviewer** review Agent | 代码审查，输出审查评论 |

**续作**：在 Issue/PR 评论中 **@Agent用户名**；`/dev`、`/reply`、`/force` 控制行为。  
**重置**：评论 `/matea reset` 或 `POST /api/sessions/reset?repo=&issue=`。

> v2 已弃用 `ai:analyze` / `ai:solve` 等 Label 触发及 routes 配置。迁移见 [设计文档 §11.2](docs/archived/20260615-trigger-rules-and-workflow-improvement.md#112-从-label-触发迁移到-assign)。

## 文档

- [技术架构](docs/ARCHITECTURE.md)
- [任务清单](docs/TASKS.md)（按需 backlog，暂缓）
- [部署指南](docs/DEPLOYMENT.md)
- [服务器运行时设计 v4](docs/server-runtime-design-v4.md)
- [开源准备清单（已归档）](docs/archived/20260716-OPEN-SOURCE-CHECKLIST.md)
- [平台策略：Gitea 优先（已归档）](docs/archived/20260714-coding-gateway-multi-vcs.md)
- [Assign 工作流 v2 设计（已归档）](docs/archived/20260615-trigger-rules-and-workflow-improvement.md)
- [测试指南](scripts/TESTING.md) · [脚本目录](scripts/README.md)
- 更多历史文档见 [docs/archived/](docs/archived/)
- [贡献指南](CONTRIBUTING.md)
- [安全策略](SECURITY.md)

## License

MIT —— 见根目录 [LICENSE](LICENSE)。
