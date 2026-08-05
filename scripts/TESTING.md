# 测试指南

> 本文是仓库**统一测试说明**：单元 / Mock 集成 / **本机真实 Gitea E2E** 前置条件与复现步骤。  
> 脚本目录索引见 [README.md](README.md)。最近一次全量 E2E 报告：[docs/archived/20260716-e2e-test-report.md](../docs/archived/20260716-e2e-test-report.md)。

## 脚本目录

| 路径 | 内容 |
|------|------|
| [`windows/`](windows/) | 本机 Gitea E2E PowerShell（setup / agents / scenarios；**主路径**，含 E13 Merge→done） |
| [`linux/`](linux/) | bash：`run-tests.sh`、`test-webhook.sh`、`e2e-smoke.sh`（Mock 冒烟）；完整 Assign E2E 用 Windows PS1 或 Linux `pwsh` |
| [`common/`](common/) | 跨平台 Go 辅助（`e2e-mock-mcp.go`、`setup-test.go`） |

---

## 本机真实 Gitea E2E（前置与复现）

与 `tests/integration`（Mock Gitea + Mock LLM）不同：本节依赖**本机 Gitea、Gateway、可选 OpenCode / Mock MCP、真实 LLM**。

### 与自动化测试的关系

| 类型 | 依赖 | 入口 |
|------|------|------|
| 单元 / 包内组合 | 无外部服务 | `go test ./internal/...` |
| 集成（Mock） | TestEnv | `go test ./tests/integration/` |
| **真实 E2E** | 本机服务 + 凭据 | 下文 + `scripts/windows/*.ps1` |

### 需要的本机服务

| 服务 | 默认地址 | 说明 |
|------|----------|------|
| Gitea | `http://localhost:3000` | 示例安装目录 `x:\gitea`；`gitea.exe`，`app.ini` 中 `ROOT_URL` 指向本机 |
| Gateway | `http://127.0.0.1:8080` | `go build -o matea.exe .` 后 `.\matea.exe -config config.yaml` |
| OpenCode（Path A / A0） | `http://127.0.0.1:4096` | `opencode serve --port 4096`；健康检查 `/global/health` |
| Mock MCP（P2.8） | `http://127.0.0.1:18080` | `go run scripts/common/e2e-mock-mcp.go` |

启动顺序建议：Gitea → OpenCode → Mock MCP → 加载凭据 → Gateway。

### 凭据（勿提交 git）

写入 `data/e2e-env.local`（`data/` 已在 `.gitignore`）：

```text
GITEA_ADMIN_TOKEN=<admin 全权限 token>
SENSENOVA_API_KEY=<OpenAI-compatible LLM key>
API_AUTH_TOKEN=dev-api-token
```

生成 Gitea admin token（本机已有 admin 用户时）：

```powershell
cd x:\gitea
.\gitea.exe admin user generate-access-token --username admin --token-name "e2e-$(Get-Date -Format 'yyyyMMddHHmmss')" --scopes "all"
```

启动 Gateway 前在同一 shell 加载环境变量：

```powershell
Get-Content data\e2e-env.local | ForEach-Object {
  if ($_ -match '^\s*([^=]+)=(.*)$') {
    Set-Item -Path "env:$($Matches[1].Trim())" -Value $Matches[2].Trim()
  }
}
```

### Gateway 配置要点（E2E）

相对远程联调，本机 E2E 常用独立库与 workspace，避免污染远程数据：

| 配置项 | 建议值 |
|--------|--------|
| `gitea.url` | `http://localhost:3000` |
| `gitea.admin_token` | `${GITEA_ADMIN_TOKEN}` |
| `gitea.webhook_secret` | 与仓库 Webhook 一致（如 `local-e2e-webhook-2026`） |
| `database.path` | `./data/matea-e2e.db` |
| `workspace.base_dir` / sandbox | `./data/work-e2e` |
| `logging.path` | `./data/logs-e2e` |
| `llm.providers.*` | OpenAI-compatible（曾用 SenseNova：`https://token.sensenova.cn/v1`） |
| `agents.defaults.provider` / `model` | 与上面对齐（如 `sensenova` / `deepseek-v4-flash`） |
| `agents.backends.opencode-local` | `base_url: http://127.0.0.1:4096`，`health_check.path: /global/health`，`allow_fallback_builtin: false` |
| `mcp.servers.e2e-mock` | `base_url: http://127.0.0.1:18080/mcp` |

远程配置可备份为 `config.remote.yaml`（勿把真实密钥写进仓库）。

### 夹具（脚本会幂等创建）

| 项 | 说明 |
|----|------|
| 组织/仓库 | `e2e/gateway-poc`（含 README、`.agents/skills/hello/SKILL.md`） |
| Webhook | `http://127.0.0.1:8080/webhook/gitea`，Secret 与 config 一致；事件含 Issues / Issue Comment / PR / PR Comment |
| 全局 Skill | 仓库根 `skills/e2e-note/SKILL.md`（Gateway `Getwd()` 扫描） |
| Agents | `e2e-analyze`、`e2e-coder-internal`、`e2e-coder-opencode`、`e2e-review`、`e2e-bugfix`（经 Gateway API 创建并加 Collaborator） |

### Windows 复现命令

在**仓库根目录**执行（先保证上表服务与凭据就绪）：

```powershell
# 可选：Mock MCP（测 MCP / E4）
go run scripts/common/e2e-mock-mcp.go

# OpenCode（测 A0 / Path A / E6 / E7 / E10）
opencode serve --port 4096

# Gateway（另开终端，已 load e2e-env.local）
.\matea.exe -config config.yaml

powershell -NoProfile -File scripts/windows/e2e-setup-gitea.ps1
powershell -NoProfile -File scripts/windows/e2e-create-agents.ps1
powershell -NoProfile -File scripts/windows/e2e-run-scenarios.ps1
# 只跑部分场景：
# powershell -NoProfile -File scripts/windows/e2e-run-scenarios.ps1 -Only E0,E1,E6
```

产物：

- `data/e2e-results.json` — 场景 PASS/FAIL（gitignore）
- Gateway 日志 — `data/logs-e2e/`
- 报告模板参考 — `docs/archived/20260716-e2e-test-report.md`

### 场景矩阵（摘要）

| ID | 覆盖 | 通过标准（摘要） |
|----|------|------------------|
| E0 | 冒烟 | Gitea / Gateway / OpenCode / MCP 健康 |
| E1 | P0.3 A0 | OpenCode session `directory` 绑定绝对路径 |
| E2 | P1.5 | Analyze 短 Loop；评论；无 PR |
| E3 | P2.9 | Skills `list_skills` / 仓内或全局 skill |
| E4 | P2.8 | MCP `e2e_echo` 出现在日志/工具结果 |
| E5 | P0.2 | internal coder → 分支 + PR |
| E6 | Path A | OpenCode coder → workspace 改码 + PR |
| E7 | fix_bug | `bug` label → `fix_bug` + PR |
| E8 | Review | `review_pr` 走 internal |
| E9 | P0.1 | 写回失败 → `partial` / `failed`，非纯 `success` |
| E10 | OpenCode health | sidecar 停 → 任务 `failed` |
| E11 | P1.7 | `GET /api/workflow-context` |
| E12 | 回归 | `go test ./... -count=1` |
| E14 | 阶段切换 unassign | Analyze→Coder 切换时前一 Agent 被移除；policy=off 时不自动 unassign；人工 assignee 保留 |

### 已知坑

1. **OpenCode WorkDir 必须绝对路径** — Gateway 侧已对 `WorkDir` 做 `filepath.Abs`；相对路径会被 sidecar 解析到错误 cwd。
2. **OpenCode Zen 计费** — 部分模型（如 `deepseek-v4-flash`）可能 `CreditsError`；E2E 曾改用 `big-pickle`。
3. **PR 创建者显示为 admin** — 分支由 agent token push，**PR 由 `gitea.admin_token` 创建**（预期设计）；评论一般由 agent 用户发出。
4. **Webhook 必须本机可达** — URL 用 `127.0.0.1:8080`，勿用 Docker 专用主机名。
5. **勿提交** `data/e2e-env.local`、DB、日志、含明文密钥的配置。

### Linux

`scripts/linux/` 目前仅有通用 bash 辅助；完整 Gitea E2E PowerShell 流程尚未移植。移植时应对齐本节前置条件，脚本可放在 `scripts/linux/e2e-*.sh`。

---

## 测试架构

```
tests/
├── integration/              # 集成测试 (需要 TestEnv)
│   ├── helpers_test.go       # 测试辅助函数 (TestEnv, MockGitea, MockLLM)
│   ├── webhook_test.go       # Webhook 端到端测试
│   └── agent_test.go         # Agent 生命周期测试
│
internal/                     # 单元测试 (各包内 _test.go)
├── agent/
│   └── tools_test.go         # Tool 注册、执行测试
├── agents/
│   └── runners_test.go       # Runner 工厂测试
├── api/
│   └── auth_test.go          # 认证中间件测试
├── dispatcher/
│   ├── dispatcher_test.go    # Dispatcher 集成测试
│   ├── router_test.go        # 路由匹配测试
│   └── template_test.go      # 模板渲染测试
├── gitea/
│   ├── client_test.go        # API 客户端测试
│   └── pr_test.go            # PR API 测试
├── llm/
│   └── provider_test.go      # Provider 接口测试
├── sandbox/
│   ├── sandbox_test.go       # 沙箱基础功能 + 多文件嵌套测试
│   └── git_test.go           # Git 操作 + 分支验证 + 完整工作流测试
└── webhook/
    └── parser_test.go        # 事件解析测试
```

## 测试分类标准

| 类型 | 标准 | 示例 |
|------|------|------|
| **单元测试** | 不依赖外部服务，测试单个函数/方法 | `TestSandboxIsAllowed`, `TestValidateBranchName` |
| **集成测试** | 需要 TestEnv（数据库、HTTP Server、Mock Gitea） | `TestWebhookIssueAssigned`, `TestAgentCRUD` |
| **多步组合测试** | 包内多步流程，无需 TestEnv | `TestSandboxFullWorkflow`, `TestSandboxWriteReadNestedFiles` |

### 判断原则

```
需要 TestEnv? (数据库/HTTP/Mock)
├── 是 → 放在 tests/integration/
└── 否 ─┬ 单函数/方法 → 放在 internal/xxx/ 对应文件
        └── 多步组合流程 → 放在 internal/xxx/ 对应文件
                            (包内可独立运行的跨模块流程，无需 TestEnv)
```

## 运行测试

### 运行所有测试

```bash
go test ./... -count=1
```

### 运行单元测试

```bash
go test ./internal/... -v -count=1
```

### 运行集成测试

```bash
go test ./tests/integration/ -v -count=1
```

### 运行特定包的测试

```bash
# 运行 sandbox 包的测试
go test ./internal/sandbox/ -v -count=1

# 运行 agent 包的测试
go test ./internal/agent/ -v -count=1
```

### 运行特定测试

```bash
# 运行特定测试函数
go test ./tests/integration/ -v -run TestWebhookIssueAssigned

# 运行匹配模式的测试
go test ./internal/sandbox/ -v -run "TestGit.*"
```

### 查看测试覆盖率

```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## 集成测试说明

### TestEnv (测试环境)

`TestEnv` 提供完整的测试环境，自动管理生命周期：

```go
env := NewTestEnv(t)
defer env.Cleanup()

// 包含:
// - 内存 SQLite 数据库
// - Mock Gitea 服务器
// - Mock LLM 提供者
// - 完整的 HTTP 服务器
// - Dispatcher 实例
```

### 使用 TestEnv 的测试

```go
func TestWebhookIssueAssigned(t *testing.T) {
    env := NewTestEnv(t)
    defer env.Cleanup()

    // 创建 Agent 和 Route
    agent := env.CreateTestAgent(t)
    env.CreateTestRoute(t, agent.ID, "issues", "assigned")

    // 启动 Dispatcher
    env.Dispatcher.Start()

    // 发送 Webhook 事件
    env.SendWebhook("issues", "delivery-001", payload)

    // 等待任务完成
    task := env.WaitForTask(t, 1, "success", 10*time.Second)

    // 验证结果
    assert.Equal(t, "analyze_issue", task.TaskType)
}
```

## 单元测试说明

### 统一使用 testify

所有单元测试统一使用 `testify` 的 `assert` 和 `require`：

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestExample(t *testing.T) {
    result, err := SomeFunction()

    // require: 失败时立即停止当前测试
    require.NoError(t, err)
    require.NotNil(t, result)

    // assert: 失败时继续执行后续断言
    assert.Equal(t, "expected", result.Value)
    assert.True(t, result.Valid)
}
```

### assert vs require

| 函数 | 行为 | 使用场景 |
|------|------|----------|
| `require.NoError(t, err)` | 失败立即停止 | 前置条件，后续代码依赖此结果 |
| `assert.NoError(t, err)` | 失败继续执行 | 独立断言，可以收集多个失败 |

## 测试覆盖范围

| 模块 | 单元测试 | 集成测试 | 说明 |
|------|----------|----------|------|
| Agent Tools | ✅ | - | Tool 注册、执行、未知工具处理 |
| API Auth | ✅ | ✅ | 认证中间件、Token 验证 |
| Agent CRUD | - | ✅ | 创建、查询、更新、删除 |
| Route CRUD | - | ✅ | 创建、查询、删除 |
| Webhook | ✅ | ✅ | 事件解析、去重、无匹配路由 |
| Task Queue | ✅ | - | 入队、出队、持久化 |
| Task Execute | ✅ | ✅ | Runner 选择、LLM 调用 |
| LLM Provider | ✅ | - | Function Calling 支持 |
| Gitea API | ✅ | - | Issue/PR/Repo 操作 |
| Sandbox | ✅ | ✅ | 文件读写、命令执行、白名单 |
| Git Operations | ✅ | ✅ | Clone/Branch/Commit/Push |
| Branch Validate | ✅ | - | 分支名验证、生成 |
| Template Render | ✅ | - | Go template 渲染 |

## 添加新测试

### 添加单元测试

在对应包目录创建 `xxx_test.go` 文件：

```go
package mypackage

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestMyFunction(t *testing.T) {
    result, err := MyFunction()
    require.NoError(t, err)
    assert.Equal(t, expected, result)
}
```

### 添加集成测试

在 `tests/integration/` 目录创建测试文件：

```go
package integration

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestMyIntegration(t *testing.T) {
    env := NewTestEnv(t)
    defer env.Cleanup()

    // 使用 env 进行测试
    // - env.DB: 数据库
    // - env.Server: HTTP 服务器
    // - env.GiteaMock: Mock Gitea
    // - env.APIRequest(): 发送 API 请求
    // - env.SendWebhook(): 发送 Webhook
}
```

## 最佳实践

1. **使用 testify**: 断言更清晰，错误信息更友好
2. **使用 TestEnv**: 集成测试自动管理环境和清理
3. **使用 Mock**: 避免依赖外部服务
4. **测试独立性**: 每个测试应该独立运行
5. **清理资源**: 使用 `defer` 确保资源释放
6. **有意义的断言**: 验证关键行为，而不是所有细节
7. **避免重复**: 单元测试和集成测试不重复覆盖相同场景
8. **遵循分类标准**: 需要 TestEnv 的放集成测试，否则放单元测试
