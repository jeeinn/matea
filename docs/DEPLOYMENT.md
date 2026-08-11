# 部署指南

本文档说明如何部署 Matea 到生产环境。

## 目录

- [环境要求](#环境要求)
- [维护者：打 tag 自动出 Release Draft](#维护者打-tag-自动出-release-draft)
- [快速部署](#快速部署)
- [配置说明](#配置说明)
- [Systemd 服务](#systemd-服务)
- [容器部署（暂未提供）](#容器部署暂未提供)
- [反向代理](#反向代理)
- [Gitea 配置](#gitea-配置)
- [运维管理](#运维管理)
- [故障排查](#故障排查)

> **安全警示（必读）**
>
> - Web UI 默认账号为 `admin` / `admin123`：**首次登录会强制修改密码**。
> - 首次运行会自动生成 `config.yaml`，其中 `auth.jwt_secret` 为随机值；勿使用示例中的 `change-me`。
> - Token、API Key、Webhook 密钥请用环境变量或 Web **系统配置**管理，**不要提交到 git**。

## 环境要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux (推荐) / macOS / Windows |
| Go | 1.26+（仅从源码构建时） |
| 内存 | ≥ 512MB |
| 磁盘 | ≥ 1GB（含工作空间） |
| 网络 | 能访问 Gitea 和 LLM API（配置完成前也可先启动 Web UI） |

## 维护者：打 tag 自动出 Release Draft

推送匹配 `v*` 的 tag 后，GitHub Actions（[`.github/workflows/release.yml`](../.github/workflows/release.yml)）会：

1. 构建前端并交叉编译 5 个平台二进制（linux/windows/darwin × amd64/arm64）
2. 生成 `checksums.txt`（对各二进制做 SHA256）
3. 创建 **draft** Release 并上传**单二进制**产物（无需预先准备 `config.yaml`）

维护者流程：

```bash
# 1. CHANGELOG [Unreleased] 整理进新版本段，CI 绿
git checkout master && git pull

# 2. 打 annotated tag 并推送
git tag -a v0.11.4 -m "v0.11.4"
git push origin v0.11.4

# 3. 在 GitHub Releases 打开 Draft，核对说明与附件后 Publish
```

历史手工步骤见 [archived/20260717-RELEASE-v0.10.0.md](archived/20260717-RELEASE-v0.10.0.md)。

## 快速部署

### 方式一：下载单二进制（推荐）

从 [Releases](https://github.com/jeeinn/matea/releases) 下载对应平台二进制（如 `matea-linux-amd64`），直接运行：

```bash
chmod +x matea-linux-amd64
./matea-linux-amd64
# 等价：./matea-linux-amd64 -config config.yaml
```

首次启动若本地没有 `config.yaml`，会自动写入最小 bootstrap（端口 `8080`、数据目录 `./data/...`、随机 `jwt_secret`、日志落盘 `./data/matea.log`），并打印 Web 访问地址与默认管理员提示。

然后：

1. 浏览器打开 http://127.0.0.1:8080  
2. 使用 `admin` / `admin123` 登录并**修改密码**  
3. 在 **系统配置** 填写 Gitea URL / Token / Webhook Secret 与 LLM Provider  
4. 顶栏「未完成初始化」提示消失后即可配置 Agent 并接收 Webhook  

Windows 下载 `matea-windows-amd64.exe` 后双击或在终端运行即可。

### 方式二：从源码构建

```bash
# 克隆代码
git clone https://github.com/jeeinn/matea.git
cd matea

# 构建前端
cd web && npm install && npm run build && cd ..

# 构建后端（前端资源通过 go:embed 打包进二进制）
go build -o matea .

# 直接运行（无 config.yaml 时自动生成）
./matea
```

也可预先 `cp config.example.yaml config.yaml` 再编辑；完整选项见 `config.full-example.yaml`。

## 配置说明

**推荐路径**：启动 → Web 登录改密 → **系统配置** 写入 Gitea / LLM（存入数据库，优先于文件）。

起步也可复制 [config.example.yaml](../config.example.yaml) → `config.yaml`（精简可运行集）。  
完整可选段与推荐取值见 [config.full-example.yaml](../config.full-example.yaml)；未写出的项由代码默认值填充（如 `workspace.base_dir` 默认为 `./data/work`）。

Bootstrap 文件通常只保留：

- `server.port`（默认 8080）
- `database.path` / `workspace.base_dir`（默认 `./data/...`）
- `auth.jwt_secret`（首次随机生成）
- `auth.default_admin_password`（仅首次创建 admin 用户）

### 环境变量

配置文件支持 `${VAR}` 和 `${VAR:-default}` 语法引用环境变量：

```yaml
gitea:
  admin_token: "${GITEA_ADMIN_TOKEN}"
  webhook_secret: "${GITEA_WEBHOOK_SECRET:-default-secret}"

llm:
  providers:
    deepseek:
      api_key: "${DEEPSEEK_API_KEY}"
```

建议通过环境变量管理敏感信息，不要将 Token 直接写入配置文件。

### 核心配置段

```yaml
server:
  host: "0.0.0.0"    # 监听地址
  port: 8080          # 监听端口

gitea:
  url: "https://gitea.example.com"
  admin_token: "${GITEA_ADMIN_TOKEN}"
  webhook_secret: "${GITEA_WEBHOOK_SECRET}"

database:
  path: "./data/matea.db"   # SQLite 数据库路径

workspace:
  base_dir: "./data/work"     # Agent 工作目录
  cleanup_after: "24h"        # 失败任务保留时间

dispatcher:
  max_concurrent: 3           # 最大并发 Agent 数
  task_retry_count: 1         # 整任务失败重试次数
  rate_limit_backoff: 30      # LLM 429 退避（秒）
  queue_size: 100             # 任务队列大小

llm:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com/v1"
      api_key: "${DEEPSEEK_API_KEY}"
  defaults:
    provider: "deepseek"
    model: "deepseek-chat"
  rate_limit_retries: 1       # 单次 ChatCompletion 遇 429 后的重试次数

auth:
  # 生产环境必须设置强随机 JWT_SECRET；勿使用下方默认值
  jwt_secret: "${JWT_SECRET:-change-me-in-production}"
  jwt_expiration: "24h"
  # 仅首次创建 admin 用户时生效；登录后请立即在 Web UI 修改密码
  default_admin_password: "${ADMIN_PASSWORD:-admin123}"

api:
  auth_token: "${API_AUTH_TOKEN}"   # 管理 API 认证 Token

agents:
  defaults:
    max_output_tokens: 8192
    max_input_tokens: 115200
    temperature: 0.3
    timeout: "5m"             # 单次任务超时
  loop:
    max_iterations: 20
    total_timeout: "30m"      # 多轮任务总超时
    no_progress_limit: 3      # 连续 N 轮无进展退出（0=关闭）
    verify_commands: []       # 编码后、commit/PR 前执行的校验命令

# 出站通知（task 2.3.3 / 2.2.4）：OpenCode backend 无自带 IM 渠道，必须配置
# 才能向飞书/企微/钉钉通知；builtin 评论已写回 Gitea 按需开启；Hermes 自带渠道可不配
deliver:
  webhook_url: ""                # 留空 = 关闭；OpenCode backend 必配，指向自建 bridge
  timeout: "10s"                 # 单次 POST 超时
  max_retries: 1                 # 网络错误/5xx 重试次数；4xx 不重试
```

> `deliver.webhook_url` 的指向、Event payload 字段、自建 bridge 拓扑示例见 README 「接入 Hub 后端 → 出站通知」一节。Matea **不自研 IM SDK，也不内置 bridge**——多渠道分发靠用户自建 bridge 扇出。

### Harness 验证门禁

通过以下配置防止 Agent 空转和提交未经测试的代码：

| 配置项 | 说明 |
|--------|------|
| `no_progress_limit` | 连续 N 轮工具调用后工作区指纹（`git status --porcelain`）不变则退出；0 = 关闭检测（config.full-example.yaml 示例为 3；YAML 省略时为 0 即关闭） |
| `verify_commands` | 编码完成后、commit/PR 前执行的 shell 命令列表；任一命令失败则任务 failed，不写回 PR；空数组 = 跳过校验 |
| `independent_checker` | 编码后、verify 前：用**全新 LLM 上下文**对 `git diff` 做 `VERDICT: PASS/FAIL`（防自评；默认 false） |

工作流门禁 `review_not_same_coder`（standard=soft / strict=hard）：审查 Agent 与当前开发 Agent 为同一 ID 时警告或阻断。

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

### 配置 LLM Provider

支持多个 Provider 同时配置，Agent 通过 `provider` 字段选择：

```yaml
llm:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com/v1"
      api_key: "${DEEPSEEK_API_KEY}"
    openai:
      base_url: "https://api.openai.com/v1"
      api_key: "${OPENAI_API_KEY}"
    claude:
      api_key: "${ANTHROPIC_API_KEY}"
    ollama:
      base_url: "http://localhost:11434/v1"
      api_key: "ollama"
```

## Systemd 服务

创建服务文件 `/etc/systemd/system/matea.service`：

```ini
[Unit]
Description=Matea
After=network.target

[Service]
Type=simple
User=gateway
Group=gateway
WorkingDirectory=/opt/matea
ExecStart=/opt/matea/matea -config /opt/matea/config.yaml
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

# 安全加固
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/opt/matea/data

# 环境变量
EnvironmentFile=/opt/matea/.env

[Install]
WantedBy=multi-user.target
```

创建环境文件 `/opt/matea/.env`：

```bash
GITEA_ADMIN_TOKEN=your-token-here
GITEA_WEBHOOK_SECRET=your-secret-here
DEEPSEEK_API_KEY=sk-xxx
JWT_SECRET=your-jwt-secret
ADMIN_PASSWORD=your-admin-password
API_AUTH_TOKEN=your-api-token
```

启动服务：

```bash
# 创建用户和目录
sudo useradd -r -s /bin/false gateway
sudo mkdir -p /opt/matea/data
sudo cp matea config.yaml /opt/matea/
sudo cp .env /opt/matea/
sudo chown -R matea:matea /opt/matea

# 启动
sudo systemctl daemon-reload
sudo systemctl enable gateway
sudo systemctl start gateway

# 查看状态
sudo systemctl status gateway
sudo journalctl -u gateway -f
```

## 容器部署（暂未提供）

> **短期不做 Docker / Compose / K8s。** 仓库**暂未提供** `Dockerfile`、`docker-compose.yml` 或 Helm chart。  
> 生产与本机部署请以 **预编译/源码二进制 + Systemd（或等价进程管理）** 为主，见上文 [快速部署](#快速部署) 与 [Systemd 服务](#systemd-服务)。

### 未来参考（示例，暂未提供）

以下片段仅作将来容器化时的思路参考，**不能直接构建**；请勿期望仓库根目录存在对应文件。

<details>
<summary>示意：多阶段构建思路（非可交付 Dockerfile）</summary>

```text
# 思路概要（非完整、未维护的 Dockerfile）：
# 1) builder：Go + Node，先 npm run build，再 go build -o matea .
# 2) runtime：精简基础镜像，仅拷贝 gateway 与配置，暴露 8080，挂载 /app/data
# Compose：映射端口、挂载 config.yaml、用环境变量注入 Token / JWT_SECRET
```

</details>

## 反向代理

### Nginx

```nginx
server {
    listen 443 ssl;
    server_name gateway.example.com;

    ssl_certificate /etc/ssl/certs/gateway.crt;
    ssl_certificate_key /etc/ssl/private/gateway.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket 支持（如果需要）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # 超时设置（Agent 任务可能较长）
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

### Caddy

```
gateway.example.com {
    reverse_proxy localhost:8080
}
```

## Gitea 配置

### 创建管理员 Token

1. 登录 Gitea 管理员账号
2. 点击头像 → **设置 → 应用 → 生成新令牌**（或「管理访问令牌」）
3. 创建 Token，勾选 **admin** 与 **repository** 相关写权限（至少 `write:admin`，用于创建 Agent 账号）
4. 将 Token 填入 Matea「系统配置 → 管理员 Token」或 `config.yaml` 的 `gitea.admin_token`

### 配置 Webhook

Matea 接收地址固定为：`http://<matea-host>:8080/webhook/gitea`（经反向代理时用对外 HTTPS URL）。  
**Webhook 密钥**：自拟任意字符串，同时填入 Matea「系统配置 → Webhook 密钥」与 Gitea Webhook 的「密钥」字段（两边一致即可）。

#### 全站 System Webhook（推荐：覆盖实例上所有仓库）

若希望任意仓库的 Assign / 评论等事件都能推到 Matea（Agent 为系统用户场景）：

1. 使用**站点管理员**登录 Gitea
2. 进入 **站点管理 → Webhooks → 添加 Webhook → Gitea**
3. 配置：
   - **目标 URL**: `https://gateway.example.com/webhook/gitea`
   - **密钥**: 与 Matea 中的 `webhook_secret` 一致
   - **触发事件**: 勾选 `Issues`、`Issue Comment`、`Pull Request`、`Pull Request Comment`（按需可加 Push）
   - **Active**: 启用
4. 保存并测试投递

**说明**：
- System Webhook 会接收**整个 Gitea 实例**上符合条件的事件，不限于某一个仓库
- 不要与「默认 Webhook」（仅在**新建**仓库时拷贝一份到仓库）混淆；全站投递应选 **System Webhook**
- Agent 能被调用仍取决于业务规则（如 Assign 给 Agent、评论 @Agent）；Webhook 只负责把事件送到 Matea

#### 组织级 Webhook（按组织批量）

若只想覆盖某一组织下的仓库：

1. 进入 **组织设置 → Webhooks → 添加 Webhook → Gitea**
2. 配置：
   - **目标 URL**: `https://gateway.example.com/webhook/gitea`
   - **密钥**: 与 `config.yaml` / 系统配置中的 `webhook_secret` 一致
   - **触发事件**: 勾选 Issues / Issue Comment / Pull Request / PR Comment
   - **Active**: 启用
3. 保存并测试

**组织级 Webhook 特点**：
- 自动应用到组织下所有现有仓库和未来新建的仓库
- 适合按团队隔离，比全站更可控

#### 仓库级 Webhook（单仓细粒度）

在需要 AI Agent 的仓库中：

1. 进入 **仓库设置 → Webhooks → 添加 Webhook → Gitea**
2. 配置：
   - **目标 URL**: `https://gateway.example.com/webhook/gitea`
   - **密钥**: 与 Matea 中的 `webhook_secret` 一致
   - **触发事件**: 勾选 Issues / Issue Comment / Pull Request / PR Comment
3. 保存并测试

**注意事项**：
- 全站 / 组织 / 仓库可并存；同一事件勿重复配置多条指向 Matea 的 hook，以免重复投递
- 组织级需要组织管理员权限；全站需要站点管理员权限
- 生产环境建议先在测试组织或单仓验证，再改用全站 System Webhook

### 使用 Agent

在 Issue 或 PR 中通过标签触发 Agent：

| 操作 | 方式 |
|------|------|
| 需求分析 | 给 Issue 添加 `ai:analyze` 标签 |
| 代码审查 | 给 PR 添加 `ai:review` 标签 |
| 开发实现 | 给 Issue 添加 `ai:solve` 标签 |
| Bug 修复 | 给 Issue 添加 `ai:fix` 标签 |
| 评论互动 | 在评论中 @Agent 用户名 |

## 运维管理

### 管理 API

通过 HTTP API 管理 Agent、任务和路由：

```bash
# 设置认证 Token
TOKEN="your-api-auth-token"

# 列出所有 Agent
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/agents

# 创建 Agent
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"dev-agent","type":"solve","provider":"deepseek","model":"deepseek-chat"}' \
  http://localhost:8080/api/agents

# 查看任务列表
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/tasks

# 查看统计
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/stats
```

### Web UI

访问 `https://gateway.example.com` 使用 Web 管理界面：

- **Dashboard**: 任务统计、成功率、系统状态
- **Agent 管理**: 创建/编辑/启用/禁用 Agent
- **任务列表**: 查看/取消/重试任务
- **Prompt 编辑**: 管理 System Prompt 和 User Template
- **用户管理**: 管理 Web UI 用户（仅 admin）

### 数据备份

```bash
# 备份数据库
cp data/matea.db data/matea.db.bak

# 或使用 SQLite 命令
sqlite3 data/matea.db ".backup data/matea-backup.db"
```

### 日志查看

日志输出到 stdout/stderr，使用 Systemd 时通过 journalctl 查看：

```bash
# 实时日志
journalctl -u gateway -f

# 最近 100 行
journalctl -u gateway -n 100

# 按时间过滤
journalctl -u gateway --since "2024-01-01" --until "2024-01-02"
```

日志级别在配置中设置：`debug` / `info` / `warn` / `error`。

## 故障排查

### 常见问题

**Q: Webhook 返回 401 Unauthorized**
- 检查 `webhook_secret` 是否与 Gitea Webhook 配置一致
- 检查 Gitea Webhook 的 Secret 字段是否填写

**Q: Agent 任务一直处于 pending 状态**
- 检查 LLM API Key 是否正确
- 检查网络是否能访问 LLM API
- 查看日志中的错误信息

**Q: Agent 执行超时**
- 调整 `agents.defaults.timeout`（单次任务超时，如 analyze/review）
- 调整 `agents.loop.total_timeout`（多轮任务总超时）
- 调整 `agents.defaults.max_output_tokens` / `max_input_tokens`（LLM 预算）
- 检查 LLM API 响应速度

**Q: 创建 Agent 失败**
- 检查 `gitea.admin_token` 是否有管理员权限
- 检查 Gitea API 是否可访问

**Q: 前端页面空白**
- 确认构建时前端已打包（`go:embed` 需要 `web/dist` 目录）
- 检查浏览器控制台是否有错误

### 健康检查

```bash
curl http://localhost:8080/health
# {"status":"ok","version":"0.10.0"}
```

### 数据库检查

```bash
sqlite3 data/matea.db

# 查看 Agent 列表
SELECT id, name, gitea_username, status FROM agents;

# 查看任务状态分布
SELECT status, COUNT(*) FROM tasks GROUP BY status;

# 查看最近的任务
SELECT id, task_type, status, created_at FROM tasks ORDER BY id DESC LIMIT 10;
```

## OpenCode sidecar（可选 Path A）

默认写任务走内置 `AgentLoop`（`agents.backends.default=internal`）。若 coder Agent 配置 `backend: opencode-local`，需在**同一台机器**运行 OpenCode HTTP 服务，且能访问 Gateway 准备的 workspace 绝对路径。

### 启动

```bash
# 与 config.full-example.yaml 中 opencode-local base_url 端口一致
opencode serve --port 4096
# 若启用 Basic Auth，设置 OPENCODE_SERVER_PASSWORD 并与 yaml auth.password 对齐
```

### Gateway 配置要点

```yaml
agents:
  backends:
    default: builtin
    backends:
      opencode-local:
        type: hub-opencode
        base_url: "http://127.0.0.1:4096"
        workspace_mode: matea_path   # 第一期唯一合法值
        health_check:
          path: /health                # 或 /global/health，视 OpenCode 版本
        allow_fallback_builtin: false # true=探活失败时降级内置 Loop（默认勿开）
```

Agent 侧设置 `backend: opencode-local`（仅 solve / fix_bug 写任务生效；analyze/review 强制 builtin）。

> **OpenCode 无自带 IM 渠道**：若需向飞书/企微/钉钉通知任务完成事件，必须配置 `deliver.webhook_url` 指向自建 bridge（见 README「接入 Hub 后端 → 出站通知」）。否则结果只写回 Gitea 评论，不会 IM 推送。

### 行为说明

| 场景 | 任务状态 |
|------|----------|
| sidecar 探活失败且 `allow_fallback_builtin=false` | **failed**（可读错误评论） |
| 探活失败且 `allow_fallback_builtin=true` | 切到 builtin 继续 |
| 探活成功但改码/提 PR 失败 | failed / partial（写回规则见 P0.1） |

工作目录绑定：`POST /session?directory=<workspace>` + `X-Opencode-Directory`（见 [archived/20260715-opencode-a0-notes.md](archived/20260715-opencode-a0-notes.md)）。

### 自检

```bash
curl -sS "http://127.0.0.1:4096/health"
curl -sS -X POST "http://127.0.0.1:4096/session?directory=/path/to/ws" \
  -H "Content-Type: application/json" \
  -H "X-Opencode-Directory: /path/to/ws" \
  -d '{"title":"ping"}'
```
