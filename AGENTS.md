# AGENTS.md

本文件面向 AI 编码代理，介绍 Matea 项目的结构、构建、测试与开发约定。仓库根目录下还有 `CLAUDE.md`（内容相近）与 `README.md`（面向用户的中文说明）。

## 项目概述

Matea（模块名 `github.com/jeeinn/matea`）是一个用 Go 编写的 **AI Agent 网关**：接收 Gitea Webhook 事件，路由给 AI Agent 处理，再把结果以 Gitea 评论或 PR 的形式写回。支持多轮 Tool-Use（Function Calling）Agent Loop，用于代码分析、审查、开发和修复任务。

- 入口：`main.go`（HTTP 服务器 + 优雅停机；前端产物通过 `go:embed web/dist/*` 打进二进制，产出单二进制部署）
- 版本：当前约 `0.11.4`（见 `main.go` 健康检查与 `web/package.json`；以 `CHANGELOG.md` 为准）
- License：MIT

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.26+（`go.mod` 为准）；标准库 `net/http`（无 Web 框架） |
| 数据库 | SQLite（`modernc.org/sqlite`，纯 Go，WAL 模式，单写者连接池），无 ORM |
| 配置 | YAML（`gopkg.in/yaml.v3`），支持 `${VAR}` / `${VAR:-default}` 环境变量展开 |
| LLM | OpenAI 兼容（DeepSeek / Qwen / Ollama 等）+ Anthropic Claude；工具调用走 OpenAI function-calling 格式 |
| 测试 | `testify`（assert + require） |
| 前端 | Vue 3 + Element Plus + Pinia + vue-router，Vite 构建（`web/`） |
| 认证 | JWT（`golang-jwt/jwt/v5`）+ bcrypt |

关键配置清单：`go.mod` / `go.sum`（Go 依赖）、`web/package.json`（前端）、`config.example.yaml` / `config.full-example.yaml`（运行配置示例）、`.github/workflows/ci.yml` / `release.yml`（CI/CD）。

## 项目结构

```
├── main.go                 # 入口：装配各组件、HTTP 路由、graceful shutdown
├── config.example.yaml     # 精简配置示例；config.full-example.yaml 为完整注释版
├── internal/
│   ├── webhook/            # Webhook HTTP Handler：签名验证、事件解析、去重
│   ├── dispatcher/         # 调度核心：Router、TaskQueue（SQLite 持久化）、Executor（并发）、v2 流水线
│   ├── workflow/           # v2 工作流引擎：Event Resolver、阶段状态机、三层门禁、Session 与生命周期
│   ├── agents/             # Runner 策略层（Analyze/Review/Interaction/Write）+ Manager + Registry + 编码后端
│   ├── agent/              # Tool-Use Agent Loop：ToolRegistry + 多轮 LLM 对话；tools_mcp/tools_skills 扩展
│   ├── llm/                # LLM Provider 接口、Registry、OpenAI 兼容与 Anthropic 客户端、限流退避
│   ├── sandbox/            # 沙箱：目录隔离（非 Docker）、命令白名单、Git 操作、审计日志
│   ├── store/              # SQLite 存储：自动迁移、agents/tasks/sessions 等 CRUD
│   ├── gitea/              # Gitea API 客户端（Issue / PR / 仓库 / 评论 / 用户）
│   ├── api/                # 管理 REST API + 认证中间件 + 系统配置热更新
│   ├── auth/               # JWT 签发/校验 + bcrypt 密码
│   ├── config/             # YAML 加载、env 展开、ConfigManager（DB 覆盖文件、热更新）
│   ├── mcp/                # MCP（Model Context Protocol）客户端与 Registry
│   └── logging/            # 日志级别与输出
├── web/                    # Vue 3 前端（构建产物 web/dist 被 go:embed）
├── tests/integration/      # 集成测试（TestEnv：内存 SQLite + Mock Gitea + Mock LLM）
├── scripts/                # windows/ 本机 Gitea E2E PowerShell；linux/ bash 辅助；common/ Go 一次性工具
├── docs/                   # 设计文档（archived/ 为历史归档）
├── skills/                 # 全局 Skill 示例（Agent 可发现）
└── data/                   # 运行时数据（gitignore）：DB、日志、工作区、E2E 凭据
```

## 架构与请求流

```
Gitea Webhook → webhook.Handler（签名验证 + 去重 + 崩溃恢复重放）
  → Dispatcher.HandleEvent
    → workflow.EventResolver（v2 Assign 模型：Assign/@提及/斜杠命令 → Agent）
    → TaskQueue.Enqueue（SQLite 持久化）
    → Executor（并发 worker）
      → Runner（按任务类型选择）
      → Agent Loop（多轮 LLM + 工具调用，默认上限 ~20 轮）
    → 写回 Gitea（评论 / 创建 PR）
```

要点：

- **Runner 策略**：任务类型映射 Runner——`analyze_issue`→Analyze、`review_pr`→Review、`reply_comment`→Interaction、`solve_issue`/`fix_bug`→写任务（沙箱 + Agent Loop + git clone/branch/commit/push + 建 PR）。非写任务始终走内置 internal Loop；写任务可配置 `opencode_http` sidecar 编码后端（`agents.backends`）。
- **默认工具箱**（`internal/agent`）：`read_file`、`write_file`、`list_files`、`search_code`、`run_command`、`apply_diff`，另有 MCP 工具与 Skills 扩展，全部限定在沙箱工作区内。
- **沙箱不是 Docker**：目录隔离 + 命令白名单 + 单命令超时（默认 5m）+ 输出上限（默认 1MB）+ 审计落库。Task 工作区为 `{sandbox.base_dir}/task_{id}`，Session 工作区为 `{workspace.base_dir}/sessions/...`。
- **数据库表**：`agents`、`tasks`、`sessions`、`prompt_history`、`processed_deliveries`、`operation_logs`、`users`、`system_config` 等；迁移在 `store.Open` 时自动执行。
- **配置优先级**：Web UI「系统配置」写入数据库后**优先于** `config.yaml` 文件，且支持热更新（LLM Registry、Gitea client、workflow policy 等）。无 `config.yaml` 时首次启动自动写最小 bootstrap（随机 `jwt_secret`，默认 admin/admin123）。

## 构建与运行

前置：Go 1.26+；构建前端需 Node.js 18+（仅 `go:embed` 需要 `web/dist`，不构建前端时可用 CI 已验证的路径）。

```bash
# 前端（vite build 后 fix-embed.js 会把 _ 开头的资源改名，因为 go:embed 忽略它们）
cd web && npm install && npm run build && cd ..

# 后端（CGO_ENABLED=0 可出静态二进制，modernc.org/sqlite 是纯 Go）
go build -o matea .

# 运行（无 config.yaml 时自动 bootstrap；Windows 产物为 matea.exe）
./matea -config config.yaml

# 前后端分离开发：后端跑 :8080；cd web && npm run dev（vite :3001，/api 代理到 :8080）

# 代码质量（提交前必须过）
go fmt ./...
go vet ./...
```

Web UI：http://127.0.0.1:8080（`admin` / `admin123`，首次登录强制改密）。健康检查：`GET /health`。

## 测试

```bash
go test ./... -count=1                 # 全部
go test ./internal/... -v -count=1     # 单元测试
go test ./tests/integration/ -v -count=1  # 集成测试（Mock，无需真实 Gitea/LLM）
go test ./internal/sandbox/ -v -count=1   # 单包
go test ./tests/integration/ -v -run TestWebhookIssueAssigned  # 单测函数
```

约定（详见 `scripts/TESTING.md`）：

- 统一用 `testify`：**前置条件用 `require`，独立断言用 `assert`**。
- 分类标准：需要 `TestEnv`（内存 SQLite / HTTP Server / Mock Gitea / Mock LLM）→ 放 `tests/integration/`；不需要 → 放对应 `internal/xxx/` 包内的 `xxx_test.go`（单函数与包内多步组合皆然）。
- 集成测试用 `NewTestEnv(t)` + `defer env.Cleanup()` 管理生命周期。
- 本机真实 Gitea E2E（非 CI）：见 `scripts/TESTING.md` 与 `scripts/windows/e2e-*.ps1`，凭据放 `data/e2e-env.local`（已 gitignore，勿提交）。

## 代码风格与开发约定

- 标准 Go 风格（`gofmt`）；无 linter 配置，以 `go vet` 为底线。
- 包内文件按职责拆分（如 `internal/agents/runner_*.go`、`internal/api/handlers_*.go`），新代码应对齐所在文件的命名与注释密度。
- 项目文档与注释**主要使用中文**；commit message 说明「为什么」，一个提交对应一个意图（分支命名 `feat/`、`fix/`、`docs/`）。
- Prompt 模板使用 Go template 语法（`{{.Issue.Number}}` 等），见 `config.full-example.yaml` 的 `agents.templates` 与 `internal/dispatcher/template.go`。
- v2 已弃用 `ai:*` Label 触发与 routes 配置；触发模型为 Assign Agent / @提及 / 评论斜杠命令（`/dev`、`/reply`、`/force`、`/matea reset`）。
- 不要新增未在 `go.mod` / `web/package.json` 中出现的依赖而不加说明；Go 侧刻意保持少依赖。

## CI、发布与部署

- **CI**（`.github/workflows/ci.yml`）：push/PR 到 `master`/`main` → 构建前端（`go:embed` 依赖）→ `go vet ./...` → `go test ./... -count=1`。
- **发布**：推 `v*` tag 触发 `release.yml` → 交叉编译 linux/windows/darwin × amd64/arm64 五个二进制 + `checksums.txt` → 创建 **draft** Release。维护者流程：先更新 `CHANGELOG.md`，再 `git tag -a vX.Y.Z && git push origin vX.Y.Z`，最后到 GitHub 核对并 Publish。
- **部署**：单二进制运行，支持 systemd、反向代理；详见 `docs/DEPLOYMENT.md`。容器部署暂未提供。

## 安全注意事项

- **绝不提交**：`config.yaml`（含 Token）、`.env`、`data/`（DB、日志、`e2e-env.local`）、任何含明文密钥的配置。密钥用 `${ENV_VAR}` 引用。
- 默认管理员 `admin`/`admin123` 仅用于首启，生产必须改密；生产必须更换 `auth.jwt_secret`。
- Webhook 请求必须验签（`internal/webhook/signature.go`）；沙箱命令走白名单，改动沙箱或命令执行相关代码时需特别谨慎并补测试。
- 安全漏洞**不要**开在公开 Issue/PR，按 `SECURITY.md` 走 GitHub Security Advisories 私下报告。

## 文档索引

- 架构详解：`docs/ARCHITECTURE.md`（组件逐一说明 + 关键设计决策）
- 部署：`docs/DEPLOYMENT.md`；测试：`scripts/TESTING.md`；贡献：`CONTRIBUTING.md`；安全：`SECURITY.md`
- 配置参考：`config.full-example.yaml`（含 `agents.backends`、`workflow.gates`、`session`、`sandbox`、`mcp` 等完整注释段）
- 历史设计文档在 `docs/archived/`（文件名带日期前缀）
