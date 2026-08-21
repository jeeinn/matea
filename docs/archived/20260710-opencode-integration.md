# TODO: OpenCode 后端接入方案（早期稿）

> 状态：**已归档** — 方案已被 [server-runtime-design-v4](20260714-server-runtime-design-v4.md) 取代（HTTP/`opencode serve` 优先，非 CLI 主路径）  
> 未完成实施清单见 [`20260714-todo-opencode-path-a.md`](20260714-todo-opencode-path-a.md)  
> 创建日期：2026-07-10  
> 归档日期：2026-07-14

---

## 一、方案概述

在 Gitea Agent Gateway 中接入 **OpenCode** 作为可选的 Coding Backend，替换 Dev/Bugfix Agent 的内置 Agent Loop。Gateway 保留 Git 操作（clone/branch/commit/push/PR）和 Gitea 编排能力，OpenCode 负责"理解代码 + 改代码"的核心逻辑。

### 架构定位

```
┌─────────────────────────────────────────┐
│  Gateway（编排层）                        │
│  Webhook → 路由 → Session → Git 回写     │
└─────────────────┬───────────────────────┘
                  │
     ┌────────────┼────────────┐
     ▼            ▼            ▼
 内置 Agent    OpenCode     Claude Code
 Loop+Tools   CLI / Server   CLI / Server
```

### 核心设计原则

1. **职责分离**：Gateway 管编排，OpenCode 管编码
2. **配置化切换**：按 Agent 级别选择 backend（internal / opencode / claude-code）
3. **渐进式迁移**：保持现有功能完整，支持平滑切换
4. **安全第一**：命令白名单、超时控制、审计日志

---

## 二、配置 Schema

### 2.1 新增配置段

在 `config.yaml` 中新增 `agents.backends` 配置：

```yaml
agents:
  backends:
    default: internal          # 默认使用内置 Agent Loop
    
    opencode:
      type: cli                # cli | http
      command: "opencode"      # 支持绝对路径，如 /usr/local/bin/opencode
      args: ["run", "--quiet", "--format", "json"]
      env_from:
        OPENAI_API_KEY: ""     # 从环境变量读取，或直接配置值
      timeout: "30m"           # 进程执行超时
      working_dir_mode: "session"  # session | task | temp
    
    opencode-server:
      type: http
      base_url: "http://localhost:4096"
      api_key: "${OPENCODE_API_KEY}"
      timeout: "30m"
    
    claude-code:
      type: cli
      command: "claude"
      args: ["-p", "--bare", "--dangerously-skip-permissions"]
      env_from:
        ANTHROPIC_API_KEY: ""
      timeout: "30m"
      working_dir_mode: "session"
```

### 2.2 Agent 表新增字段

在 `internal/store/agent.go` 的 `Agent` 结构体中新增：

```go
type Agent struct {
    // ... 现有字段 ...
    Backend string `json:"backend"`  // internal | opencode | claude-code | opencode-server
}
```

### 2.3 配置优先级

1. Agent 级 `backend` 字段（最高优先级）
2. `agents.backends.default` 配置
3. 默认值：`internal`

---

## 三、接口设计

### 3.1 Runner 接口扩展

在 `internal/agents/runners.go` 中新增 `ExternalCLIRunner`：

```go
type ExternalCLIRunner struct {
    llmRegistry      *llm.Registry
    giteaFactory     GiteaClientFactory
    sandboxCfg       sandbox.Config
    db               *store.DB
    backendCfg       BackendConfig
    defaultMaxTokens int
    defaultTemp      float64
}

func (r *ExternalCLIRunner) Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error) {
    // 1. 获取或创建 Session 工作区
    // 2. Clone/Fetch 仓库
    // 3. 创建分支
    // 4. Spawn 外部 CLI 进程
    // 5. 解析输出
    // 6. Git Commit/Push
    // 7. 创建 PR
    // 8. 更新 Session
}
```

### 3.2 BackendConfig 结构

```go
type BackendConfig struct {
    Type            string                 `yaml:"type"`            // cli | http
    Command         string                 `yaml:"command"`         // CLI 命令路径
    Args            []string               `yaml:"args"`            // CLI 参数
    EnvFrom         map[string]string      `yaml:"env_from"`        // 环境变量映射
    BaseURL         string                 `yaml:"base_url"`        // HTTP 模式下的服务地址
    APIKey          string                 `yaml:"api_key"`         // HTTP 模式下的 API Key
    Timeout         time.Duration          `yaml:"timeout"`         // 执行超时
    WorkingDirMode  string                 `yaml:"working_dir_mode"` // session | task | temp
}
```

### 3.3 RunnerFactory 扩展

修改 `RunnerFactory`，根据 Agent 的 `backend` 字段选择 Runner：

```go
func (f *RunnerFactory) GetRunner(taskType string, agent *store.Agent) (Runner, error) {
    switch agent.Backend {
    case "opencode", "opencode-server", "claude-code":
        return f.NewExternalCLIRunner(agent.Backend), nil
    default:
        // 原有逻辑：根据 taskType 返回内置 Runner
    }
}
```

---

## 四、实现步骤

### 阶段一：配置层（1-2 天）

| 步骤 | 文件 | 内容 | 状态 |
|------|------|------|------|
| 1.1 | `internal/config/schema.go` | 新增 `BackendConfig` 和 `AgentBackends` 结构 | pending |
| 1.2 | `internal/config/config.go` | 加载 backends 配置 | pending |
| 1.3 | `config.example.yaml` | 添加 `agents.backends` 示例配置 | pending |
| 1.4 | `internal/store/agent.go` | Agent 表新增 `backend` 字段 | pending |
| 1.5 | `internal/store/sqlite.go` | 添加数据库迁移（新增 backend 列） | pending |

### 阶段二：Runner 层（2-3 天）

| 步骤 | 文件 | 内容 | 状态 |
|------|------|------|------|
| 2.1 | `internal/agents/runners.go` | 新增 `ExternalCLIRunner` 实现 | pending |
| 2.2 | `internal/agents/runners.go` | 修改 `RunnerFactory.GetRunner` 支持 backend 选择 | pending |
| 2.3 | `internal/agents/runners.go` | 实现 `runWriteTask` 的外部 CLI 版本 | pending |
| 2.4 | `internal/sandbox/sandbox.go` | 命令白名单新增 `opencode`、`claude` | pending |

### 阶段三：API 层（1 天）

| 步骤 | 文件 | 内容 | 状态 |
|------|------|------|------|
| 3.1 | `internal/api/router.go` | Agent CRUD API 支持 `backend` 字段 | pending |
| 3.2 | `web/src/views/Agents.vue` | 前端添加 backend 选择下拉框 | pending |

### 阶段四：测试与优化（2-3 天）

| 步骤 | 文件 | 内容 | 状态 |
|------|------|------|------|
| 4.1 | `internal/agents/runners_test.go` | 新增 `ExternalCLIRunner` 单元测试 | pending |
| 4.2 | `tests/integration/agent_test.go` | 新增集成测试（mock OpenCode） | pending |
| 4.3 | `internal/sandbox/sandbox.go` | 完善超时控制和错误处理 | pending |
| 4.4 | 文档更新 | 更新 README 和 ARCHITECTURE | pending |

---

## 五、关键实现细节

### 5.1 外部 CLI 进程管理

```go
func runExternalCLI(ctx context.Context, cfg BackendConfig, workDir string, prompt string) (*cli.Result, error) {
    cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
    
    // 设置工作目录
    cmd.Dir = workDir
    
    // 注入环境变量
    env := os.Environ()
    for key, value := range cfg.EnvFrom {
        if value == "" {
            value = os.Getenv(key)
        }
        env = append(env, fmt.Sprintf("%s=%s", key, value))
    }
    cmd.Env = env
    
    // 输入 prompt
    stdin, err := cmd.StdinPipe()
    if err != nil {
        return nil, err
    }
    go func() {
        defer stdin.Close()
        stdin.Write([]byte(prompt))
    }()
    
    // 捕获输出
    var stdout, stderr strings.Builder
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    
    err = cmd.Run()
    return &cli.Result{
        Stdout:   stdout.String(),
        Stderr:   stderr.String(),
        ExitCode: cmd.ProcessState.ExitCode(),
        Error:    err,
    }, nil
}
```

### 5.2 输出解析

OpenCode CLI 支持 `--format json`，输出为 JSON 格式：

```json
{
    "status": "success",
    "summary": "Fixed authentication bug in auth.go",
    "changes": [
        {"file": "internal/auth/auth.go", "action": "modified"},
        {"file": "internal/auth/auth_test.go", "action": "added"}
    ],
    "errors": []
}
```

解析逻辑：

```go
func parseOpenCodeOutput(output string) (*CodeChanges, error) {
    var result struct {
        Status  string `json:"status"`
        Summary string `json:"summary"`
        Changes []struct {
            File   string `json:"file"`
            Action string `json:"action"`
        } `json:"changes"`
        Errors []string `json:"errors"`
    }
    
    if err := json.Unmarshal([]byte(output), &result); err != nil {
        return nil, err
    }
    
    return &CodeChanges{
        Success: result.Status == "success",
        Summary: result.Summary,
        Files:   result.Changes,
        Errors:  result.Errors,
    }, nil
}
```

### 5.3 Session 续作支持

OpenCode 支持 `--continue` 参数，与 Gateway Session ID 映射：

```bash
opencode run --continue session-${session_id} "Continue fixing the bug"
```

### 5.4 Windows 环境支持

OpenCode 官方支持 Windows，无需 WSL2。配置示例：

```yaml
opencode:
  type: cli
  command: "C:\\Program Files\\OpenCode\\opencode.exe"
  args: ["run", "--quiet", "--format", "json"]
```

---

## 六、测试策略

### 6.1 单元测试

- **配置解析**：测试 `BackendConfig` 从 YAML 加载
- **Runner 选择**：测试 `RunnerFactory.GetRunner` 根据 backend 返回正确 Runner
- **输出解析**：测试 JSON 输出解析逻辑

### 6.2 集成测试

使用 mock OpenCode 服务进行端到端测试：

```go
func TestExternalCLIRunner_Run(t *testing.T) {
    // 启动 mock OpenCode 服务
    mockServer := startMockOpenCodeServer()
    defer mockServer.Close()
    
    // 创建 Agent，backend 指向 mock 服务
    agent := &store.Agent{
        Backend: "opencode-server",
        // ... 其他配置
    }
    
    // 执行任务并验证结果
    runner := NewExternalCLIRunner(mockServer.URL)
    result, err := runner.Run(ctx, task, agent)
    
    assert.NoError(t, err)
    assert.Equal(t, "success", result.Status)
}
```

### 6.3 手动测试

| 测试场景 | 步骤 | 预期结果 |
|----------|------|----------|
| OpenCode CLI 模式 | 配置 backend=opencode，执行 solve_issue | 代码被修改，PR 创建成功 |
| OpenCode Server 模式 | 启动 opencode serve，配置 backend=opencode-server | 任务执行成功，复用 server 连接 |
| Session 续作 | 首次执行后，再次 @mention | OpenCode 使用 --continue 恢复会话 |
| 超时控制 | 配置短超时，执行耗时任务 | 任务被正确终止 |
| 错误处理 | 断开 LLM 连接 | 返回友好错误信息，任务标记失败 |

---

## 七、风险评估

### 7.1 技术风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| OpenCode API 变动 | 中 | 使用稳定版本，锁定依赖 |
| 进程生命周期失控 | 中 | 设置严格超时，进程监控 |
| 输出格式不一致 | 低 | JSON 格式解析，异常处理 |
| Windows 环境兼容性 | 低 | OpenCode 原生支持，测试覆盖 |

### 7.2 安全风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Prompt Injection | 高 | 输入验证，权限隔离 |
| 密钥泄露 | 高 | 环境变量注入，禁止硬编码 |
| 命令注入 | 中 | 命令白名单，参数验证 |
| 路径穿越 | 中 | 沙箱路径验证 |

### 7.3 运维风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 冷启动延迟 | 中 | Server 模式复用，sidecar 部署 |
| 资源消耗 | 中 | 并发控制，资源限制 |
| 依赖服务不可用 | 中 | 健康检查，降级到内置 Loop |

---

## 八、迁移路径

### 8.1 渐进式迁移

1. **阶段 1**：实现功能，默认关闭（所有 Agent 使用 internal backend）
2. **阶段 2**：在测试环境启用，验证稳定性
3. **阶段 3**：为特定 Agent 配置 opencode backend
4. **阶段 4**：根据效果逐步扩大范围

### 8.2 回滚策略

- 修改 Agent 的 `backend` 字段为 `internal` 即可回滚
- 无需代码变更，仅需配置调整

---

## 九、参考链接

- OpenCode 官方文档：https://opencode.ai/
- OpenCode GitHub：https://github.com/opencode-ai/opencode
- OpenCode CLI 文档：https://docs.opencode.ai/cli
- 项目架构文档：`docs/ARCHITECTURE.md`
- 沙箱路线图：`docs/archived/20260604-sandbox-roadmap.md`

---

## 十、待办事项清单

```
[ ] 阶段一：配置层实现
    [ ] 1.1 internal/config/schema.go - BackendConfig 结构
    [ ] 1.2 internal/config/config.go - 加载配置
    [ ] 1.3 config.example.yaml - 添加示例
    [ ] 1.4 internal/store/agent.go - backend 字段
    [ ] 1.5 internal/store/sqlite.go - 数据库迁移

[ ] 阶段二：Runner 层实现
    [ ] 2.1 internal/agents/runners.go - ExternalCLIRunner 实现
    [ ] 2.2 internal/agents/runners.go - RunnerFactory 扩展
    [ ] 2.3 internal/agents/runners.go - runWriteTask 外部 CLI 版本
    [ ] 2.4 internal/sandbox/sandbox.go - 命令白名单扩展

[ ] 阶段三：API 层实现
    [ ] 3.1 internal/api/router.go - Agent API 支持 backend
    [ ] 3.2 web/src/views/Agents.vue - 前端 backend 选择

[ ] 阶段四：测试与优化
    [ ] 4.1 internal/agents/runners_test.go - 单元测试
    [ ] 4.2 tests/integration/agent_test.go - 集成测试
    [ ] 4.3 完善超时控制和错误处理
    [ ] 4.4 更新文档
```