# 项目重命名实施梳理：gitea-agent-gateway → Matea

> 状态：**已实施完成（分支 `chore/rename-to-matea`）** — 构建通过、`go test` 仅 9 个已知 git 沙箱测试失败（与改名无关），其余全绿。
> 目标名：**Matea** ｜ 二进制：**matea** ｜ 读音：`妈提-呃` / `mate-a`
> 命名来源：`mate`（伙伴 / AI 副驾）+ `tea`（Gitea）｜ 定位语：**"Matea — your AI dev mate for Gitea"**
> 仓库（后续）：`github.com/jeeinn/matea`（模块路径随之取全路径）

---

## 1. 命名决策摘要（已确认）

| 维度 | 现状 | 目标 | 决策状态 |
|---|---|---|---|
| 模块路径（go.mod） | `gitea-agent-gateway` | `github.com/jeeinn/matea` | ✅ 确认（仓库将改名） |
| Go import 前缀 | `gitea-agent-gateway/internal/...` | `github.com/jeeinn/matea/internal/...` | ✅ 确认（机械替换） |
| 二进制名 | `gateway` / `gateway.exe` | `matea` / `matea.exe` | ✅ 确认 |
| 展示名 / 标题 | `Gitea Agent Gateway` | `Matea` | ✅ 确认 |
| `workspace_mode` 枚举 | `gateway_path` | `matea_path` | ✅ 确认（**不兼容旧配置**，无真实用户） |
| 默认 DB 路径 | `./data/gateway.db` | `./data/matea.db` | ✅ 确认（附迁移说明） |
| 默认日志名 | `gateway.log` | `matea.log` | ✅ 确认（随 D3 一致） |
| JWT issuer | `gitea-agent-gateway` | `matea` | ✅ 确认（claim 字符串变更；当前校验**不读** iss，见 P1-8） |
| 仓库 URL 引用 | `github.com/jeeinn/ai-dev` | `github.com/jeeinn/matea` | ✅ 确认（随仓库改名） |
| 产品标识 `gateway-agent` / `<!-- gateway-agent -->` | `gateway-agent` | `matea-agent` | ✅ 确认（**硬切**，不双读旧标记） |
| `/gateway reset` 命令 | `/gateway reset` | `/matea reset` | ✅ 确认（**硬切**，不双读旧命令） |
| 任务失败原因文案 | `gateway restarted; ...` | `matea restarted; ...` | ✅ 确认（一并改） |
| OpenCode session title | `gateway-task-%d` | `matea-task-%d` | ✅ 确认（一并改） |
| 测试仓库 `e2e/gateway-poc` | `e2e/gateway-poc` | 保留 | ✅ 确认保留（Gitea 侧夹具，与产品名解耦） |

> 说明：文档中“gateway”作为**架构概念**（如“webhook gateway / 网关服务”）一律**保留**，仅替换上表中的全名/展示名/产品标识锚点。
>
> **硬切约定（D9 / D10）**：不实现过渡期双读（不接受旧 `/gateway reset`，不识别旧 `<!-- gateway-agent -->`）。无真实用户，一次性断裂可接受；发布说明写明即可。

**标识符保留（不改）：**
- 变量/参数名 `gatewayDir`、脚本参数 `$GatewayURL` 等架构概念命名 → **保留**。
- Go 常量名 `opencodeWorkspaceModeGatewayPath`：字符串值改为 `"matea_path"`；常量标识符**建议**改为 `opencodeWorkspaceModeMateaPath`（可读性），非必须。

---

## 2. 范围与排除项

**纳入（需改）：** 仓库源码、根文档、配置、CI、前端源码（含 `index.html`）、E2E 脚本与脚本文档、LICENSE、`.gitignore`、`.env.example`、项目记忆、活跃 docs 中的仓库 URL 引用。

**排除（不改 / 不相关）：**
- `docs/archived/`（已归档除外）。
- `data/` 运行时数据：`gateway*.db`、`logs*/gateway*.log`、根目录 `gateway`/`gateway.exe` 产物（按 §5 迁移/重建，不编辑旧文件）。
- `web/node_modules/`、`web/dist/`、`web/package-lock.json`（重新生成）。
- `data/work*/`、`workspace/sessions/*` 仓库快照副本（agent 工作区镜像）。
- `.git` 历史；`.idea/`、`config.remote.yaml.bak` 等 IDE/备份元数据。
- GitHub 仓库改名后，旧 `github.com/jeeinn/ai-dev` 历史 compare 链接由 GitHub 自动重定向（CHANGELOG 历史条目保留，不逐一改写）。

---

## 3. 重命名面总览（优先级 + 复杂度）

| # | 区域 | 影响范围 | 类型 | 优先级 | 复杂度 |
|---|---|---|---|---|---|
| 1 | go.mod 模块声明 | 1 文件 | 机械 | **P0** | 低 |
| 2 | Go import 前缀 | 95 源文件 | 机械（脚本） | **P0** | 低（量大） |
| 3 | 二进制名 `gateway`→`matea` | release.yml + 文档/脚本文档 + `.gitignore` + 根产物 | 机械 | **P1** | 低 |
| 4 | 展示名 `Gitea Agent Gateway`→`Matea`（+定位语） | README/CLAUDE/CONTRIBUTING/ARCHITECTURE/DEPLOYMENT/LICENSE/config*.yaml/E2E 描述 | 机械 | **P1** | 低 |
| 5 | 仓库 URL `jeeinn/ai-dev`→`jeeinn/matea` | README + CONTRIBUTING + SECURITY + DEPLOYMENT + 活跃 docs | 机械 | **P1** | 低 |
| 6 | `workspace_mode` 枚举 `gateway_path`→`matea_path` | opencode_http.go / schema.go / config*.yaml / DEPLOYMENT / server-runtime-design-v4 | 语义 | **P1** | 中 |
| 7 | 默认 DB 路径 `gateway.db`→`matea.db` + 迁移 | bootstrap / config / 测试 / setup-test / config*.yaml / DEPLOYMENT 备份示例 | 语义 | **P1** | 中 |
| 8 | 默认日志名 `gateway.log`→`matea.log` | logging.go / logging_test.go / e2e ps1 | 语义 | **P1** | 低 |
| 9 | JWT issuer → `matea` | `internal/auth/jwt.go:44` | 语义 | **P1** | 低 |
| 10 | 失败原因 / OpenCode title | dispatcher / store / opencode_http(+test) | 语义 | **P1** | 低 |
| 11 | 前端包名与展示名 | `web/package.json`、lock、`index.html`、Dashboard/Login | 机械 | **P2** | 低 |
| 12 | E2E 脚本与脚本文档 | `scripts/windows/*.ps1` + `scripts/README.md` + `scripts/TESTING.md` + setup-test.go | 机械 | **P2** | 低 |
| 13 | 产品标识 `gateway-agent`→`matea-agent`（**硬切**） | manager.go / gate_l1.go / 测试 / ps1 token | 语义 | **P2** | 中 |
| 14 | `/gateway reset`→`/matea reset`（**硬切**） | pipeline.go + README + ARCHITECTURE | 语义 | **P2** | 中 |
| 15 | SECURITY / CHANGELOG / MEMORY / `.env.example` | 收尾文档 | 人工 | **P3** | 低 |
| 16 | GitHub 仓外改名运维 | Settings / Topics / About / 旧 Release 说明 | 运维 | **P3** | 低 |

---

## 4. 详细清单（含已确认代码锚点）

### P0 — 编译阻断

**P0-1 go.mod**
- `module gitea-agent-gateway` → `module github.com/jeeinn/matea`
- `go.sum` 经核查**不含**该路径，无需改。

**P0-2 Go import 前缀（95 源文件，见附录 A）**
- 替换锚点：`gitea-agent-gateway/internal/` → `github.com/jeeinn/matea/internal/`
- 脚本：`sed -i 's#gitea-agent-gateway/#github.com/jeeinn/matea/#g'`（仅匹配带 `/` 的前缀，避开下方独立字符串）。
- **不要**在机械替换里误伤：
  - `internal/auth/jwt.go:44`（`Issuer: "gitea-agent-gateway"`，无 `/`，单独处理，见 P1-8）
  - `internal/agents/manager.go:15`（`"gateway-agent"`，无前缀，见 P2-3）
  - `internal/workflow/gate_l1.go` 的 `<!-- gateway-agent -->`（见 P2-3）
  - `internal/dispatcher/pipeline.go` 的 `/gateway reset`（见 P2-4）
  - 失败原因 / `gateway-task-` 等（见 P1-9）

### P1 — 核心重命名

**P1-1 二进制名 `gateway`→`matea`**
- `.github/workflows/release.yml`：L56–60 `gateway-{os}-{arch}` → `matea-{os}-{arch}`；L64 `sha256sum gateway-*` → `matea-*`；L79 `./gateway` → `./matea`；L85–89 `files:` 列表；文件头注释同改。
- `.gitignore`：`gateway` / `gateway.exe*` → `matea` / `matea.exe*`。
- 根目录产物 `gateway`/`gateway.exe`：删除后 `go build -o matea .` 重建。

**P1-2 文档构建/运行命令（⚠️ 运行命令 `./gateway` 也要改，勿只改 `go build` 行）**
- `README.md`：`go build -o gateway .`（L87/L158/L230）→ `matea`；运行命令 `./gateway`（L88）、`./gateway -config config.yaml`（L164）一并改。
- `CLAUDE.md`：L13 `go build -o gateway .`；L16 `./gateway -config config.yaml`。
- `CONTRIBUTING.md`：L28 `go build -o gateway .`；L40 `./gateway -config config.yaml`。
- `docs/ARCHITECTURE.md`：L574 `go build -o gateway .`；L577 `./gateway -config config.yaml`。
- `docs/DEPLOYMENT.md`（二进制出现最分散）：L61 示例 `gateway-linux-amd64`；L64 `chmod +x gateway-linux-amd64`；L65/L66 `./gateway-linux-amd64`（含 `-config`）；L78 `gateway-windows-amd64.exe`；L91 `go build -o gateway .`；L94 `./gateway`；L310 `go build -o gateway .`。
- `scripts/README.md`：`.\gateway.exe` → `.\matea.exe`。
- `scripts/TESTING.md`：`go build -o gateway.exe` / `.\gateway.exe` → `matea.exe`。
- `.env.example`：注释「启动 gateway」→「启动 matea」。

**P1-3 展示名 `Gitea Agent Gateway`→`Matea`**
- `README.md`（标题+简介，建议补定位语）、`CLAUDE.md`、`CONTRIBUTING.md`、`docs/ARCHITECTURE.md`、`docs/DEPLOYMENT.md`、`config.example.yaml`/`config.full-example.yaml`/`config.yaml` 注释。
- `LICENSE`：`Copyright (c) 2026 Gitea Agent Gateway Contributors` → `Matea Contributors`（或 `Matea / jeeinn Contributors`，执行时统一一种）。
- `docs/DEPLOYMENT.md` systemd 单元：`Description=Gitea Agent Gateway` → `Description=Matea`。
- `scripts/windows/e2e-setup-gitea.ps1`：仓库描述文案 “Local E2E test repository for Gitea Agent Gateway” → `… for Matea`（仓库名 `gateway-poc` **保留**）。

**P1-4 仓库 URL `jeeinn/ai-dev`→`jeeinn/matea`**
- `README.md` L3/L4 徽标、L91 Releases 链接。
- `CONTRIBUTING.md` L3/L7/L11/L71（仓库链接与 clone 地址）。
- `SECURITY.md` L3/L16（仓库链接与 Security Advisories URL）——**必须改**（非「无引用」）。
- `docs/DEPLOYMENT.md`：Releases / `git clone` 链接；产物名示例 `gateway-linux-amd64` → `matea-linux-amd64`。
- 活跃 docs（非 archived）：经 `grep` 核查，当前仅 `docs/DEPLOYMENT.md`（L61 Releases、L84 `git clone`）与根文档含 `jeeinn/ai-dev`；`docs/OPEN-SOURCE-CHECKLIST.md`、`docs/RELEASE-v0.10.0.md` **当前不含**该 URL，无需改（保留此行以便仓库改名后二次复核）。
- `CHANGELOG.md` 历史 compare 链接保留（GitHub 改名后自动重定向）。

**P1-5 `workspace_mode` 枚举 `gateway_path`→`matea_path`（✅ 不兼容旧配置）**
- `internal/agents/opencode_http.go:19`：字符串 `"gateway_path"` → `"matea_path"`；常量名建议 `opencodeWorkspaceModeMateaPath`。
- `internal/config/schema.go:245`：注释 `// first release: "gateway_path" only` → `matea_path`
- `config.yaml:78`：`workspace_mode: gateway_path` → `matea_path`
- `config.full-example.yaml:146`、`docs/DEPLOYMENT.md:546`、`docs/server-runtime-design-v4.md:251` 注释
- `docs/server-runtime-design-v4.md:410`：`非 gateway_path 直接拒绝` → `非 matea_path`

**P1-6 默认 DB 路径 `gateway.db`→`matea.db`（✅ 含迁移）**
- 代码默认：`internal/config/bootstrap.go:34`、`internal/config/config.go:135`：`./data/gateway.db` → `./data/matea.db`
- 测试期望：`internal/config/config_test.go:69/103/166`：断言值改为 `./data/matea.db`
- 测试夹具：`scripts/common/setup-test.go:15`：`./data/gateway.db` → `./data/matea.db`
- 配置示例：`config.example.yaml:22`、`config.full-example.yaml:42` 注释/默认值
- e2e 库：`config.yaml:26` `gateway-e2e.db` → `matea-e2e.db`；`scripts/windows/e2e-run-scenarios.ps1:326/338` 同改；`scripts/TESTING.md` 表格中的 `database.path` 同改
- `docs/DEPLOYMENT.md`：`gateway.db` 出现处全改——L143 `path: "./data/gateway.db"` 配置示例、L452 `gateway.db.bak`、L455 `gateway-backup.db`、L512 `sqlite3 data/gateway.db` 巡检命令 → `matea.*`
- **迁移说明（务必写入发布说明）**：旧库不会自动可见，需手动复制或首次启动自动迁移：
  ```bash
  cp data/gateway.db data/matea.db
  cp data/gateway-e2e.db data/matea-e2e.db
  ```
  （可选增强：`internal/config/bootstrap.go` 在默认路径为空且 `data/gateway.db` 存在时自动 rename，降低升级摩擦。）

**P1-7 默认日志名 `gateway.log`→`matea.log`（随 #7 一致）**
- `internal/logging/logging.go:67`（注释）、`:78`：`filepath.Join(dir, "gateway.log")` → `"matea.log"`
- `internal/logging/logging_test.go:45`：读文件路径改为 `matea.log`

**P1-8 JWT issuer → `matea`**
- `internal/auth/jwt.go:44`：`Issuer: "gitea-agent-gateway"` → `Issuer: "matea"`
- **事实说明**：当前 `ValidateToken` **不校验** `Issuer`，故仅改 claim 字符串**不会**因 iss 导致旧 token 自动失效。发布说明写「建议重新登录」即可；若未来要强制失效，需另加 iss 校验（本 rename **不做**）。

**P1-9 失败原因文案 + OpenCode session title（✅ 一并改）**
- `internal/dispatcher/dispatcher.go`：`"gateway restarted; interrupted running task"` → `"matea restarted; interrupted running task"`
- `internal/store/task.go`：同字符串默认 reason 同步
- `internal/agents/opencode_http.go:176`：`"gateway-task-%d"` → `"matea-task-%d"`
- `internal/agents/opencode_http_test.go:205`：断言 `"gateway-task-42"` → `"matea-task-42"`

### P2 — 前端、脚本与用户接口（硬切）

**P2-1 前端**
- `web/package.json`：`"name": "gitea-agent-gateway-web"` → `"matea-web"`
- `web/package-lock.json`：执行 `npm install` 重新生成（不手改）
- `web/index.html`：`<title>Gitea Agent Gateway</title>` → `Matea`
- `web/src/views/Dashboard.vue:6`、`./Login.vue:6`：展示名 → `Matea`

**P2-2 E2E 脚本（随 P1-6 / P1-7 同步）**
- `scripts/windows/e2e-run-scenarios.ps1`：L128/L139 `gateway.log`→`matea.log`；L326/L338 `gateway-e2e.db`→`matea-e2e.db`
- 参数名 `$GatewayURL`、默认 `$Repo = "gateway-poc"` → **保留**（架构/夹具名）

**P2-3 产品标识 `gateway-agent`→`matea-agent`（✅ 硬切）**
- `internal/agents/manager.go:15`：`const agentTokenName = "gateway-agent"` → `"matea-agent"`
- `internal/workflow/gate_l1.go:90/95`：`<!-- gateway-agent -->` → `<!-- matea-agent -->`
- 测试：`internal/workflow/gate_l1_test.go:123/128`、`mention_test.go:183` 断言/夹具
- `scripts/windows/e2e-run-scenarios.ps1:378`：`gateway-agent-e2e-restore-` → `matea-agent-e2e-restore-`
- **硬切**：不双读旧 `<!-- gateway-agent -->`；改名后旧 bot 评论不再被识别为 agent 评论（一次性，可接受）。发布说明写明。

**P2-4 `/gateway reset` 命令 → `/matea reset`（✅ 硬切）**
- `internal/dispatcher/pipeline.go:21/22/28`：`/gateway reset` → `/matea reset`
- 文档同步（必做）：
  - `README.md`（重置说明）：`/gateway reset` → `/matea reset`
  - `docs/ARCHITECTURE.md`（流程图/说明两处）同改
- **硬切**：不接受旧 `/gateway reset`；用户需改用新命令。发布说明写明。

### P3 — 收尾与仓外运维

- `SECURITY.md`：更新 `jeeinn/ai-dev` → `jeeinn/matea`（见 P1-4）；通读无残留展示名。
- `CHANGELOG.md`：顶部或“Unreleased”段加更名说明（含硬切：`/matea reset`、`matea-agent` 标记、DB/日志路径、失败原因文案）；历史 `gateway`/`gateway.exe` 提及保留。
- `.workbuddy/memory/MEMORY.md`：`项目名：gitea-agent-gateway` → `Matea`。
- **GitHub 仓外 checklist**（仓库改名后人工完成）：
  1. Settings → 仓库名 `ai-dev` → `matea`（确认 GitHub 自动重定向）。
  2. About / Topics / 首页描述改为 Matea 与定位语。
  3. 旧 Release 资产仍为 `gateway-*` 时，在最新 Release 说明中指向新产物名 `matea-*`（不必回改历史资产）。
  4. 本地 `git remote set-url origin …/matea.git`（开发者各自执行）。

---

## 5. 迁移说明（P1-6 / D3）

升级到 Matea 后，旧默认数据文件不可见，需迁移：

```bash
# 停止旧进程后
cp data/gateway.db data/matea.db
cp data/gateway-e2e.db data/matea-e2e.db
# 日志为可丢弃产物，无需迁移（新版本自动写 matea.log）
```

可选：在 `internal/config/bootstrap.go` 增加自动迁移——若 `Database.Path` 取默认且 `data/gateway.db` 存在而 `data/matea.db` 不存在，则 `os.Rename` 旧文件到新路径（单次、幂等）。

**用户接口硬切（无迁移路径）：**
- 评论命令仅识别 `/matea reset`
- Agent 评论标记仅识别 `<!-- matea-agent -->`

---

## 6. 建议执行阶段

- **阶段 0 — 决策已全部确认**：D1 模块全路径、D2 `gateway_path`→`matea_path`、D3 `gateway.db`→`matea.db`、D4 `gateway.log`→`matea.log`、D5 测试仓库保留、D6 JWT issuer→`matea`、D9 `gateway-agent`→`matea-agent`（硬切）、D10 `/gateway reset`→`/matea reset`（硬切）、失败原因/OpenCode title 一并改。直接进入阶段 1。
- **阶段 1 — 机械替换（脚本化）**：go.mod；95 文件 import 前缀；展示名（含 LICENSE / index.html / systemd Description）；二进制名（含 `.gitignore` / 脚本文档）；仓库 URL（含 SECURITY / DEPLOYMENT / 活跃 docs）；前端包名+src+title。
- **阶段 2 — 语义替换（代码常量）**：P1-5 枚举、P1-6 DB 路径+测试、P1-7 日志、P1-8 JWT、P1-9 失败原因/title、P2-3/P2-4 硬切（代码+文档）。
- **阶段 3 — 构建验证**：`go build ./...` + `go vet` + `go test ./...`；`cd web && npm ci && npm run build`；`go build -o matea . && ./matea` 冒烟；CI dry-run 复核 release.yml。
- **阶段 4 — 收尾**：写迁移说明、CHANGELOG 更名段（含硬切说明）、更新 MEMORY、删除旧二进制产物、执行 §P3 GitHub 仓外 checklist。

---

## 7. 风险与注意事项

1. **import 前缀漏改** → 编译失败。完成后 `grep -rn "gitea-agent-gateway/" --include="*.go" . | grep -v data/` 应为空。
2. **“gateway” 一词多义**：机械替换整词 `gateway` 危险，必须以具体锚点替换；孤立 `gateway`（架构概念）、`gatewayDir`、`$GatewayURL`、`e2e/gateway-poc` **保留**。
3. **枚举/路径改名是代码常量**（非仅配置）：漏改会导致配置不生效或测试失败。
4. **默认 DB 路径变化**使旧库不可见，上线前必须迁移或启用自动迁移（§5）。
5. **JWT issuer**：仅改签发 claim；当前不校验 iss，旧 token 仍可用至过期。发布说明建议重新登录即可。
6. **硬切用户接口**：`/matea reset` 与 `<!-- matea-agent -->` 不兼容旧写法；文档与 CHANGELOG 必须同步。
7. **前端 lock / node_modules** 必须重新生成，不可手改。

---

## 8. 完成校验清单（rename 后应为 0）

```bash
# 公共排除：data/（运行时产物）、docs/archived、本计划文档、项目记忆日log、备份元数据、CHANGELOG 历史提及
#   （.workbuddy 日log 为历史记录不改；config.remote.yaml.bak 为备份不改；CHANGELOG 旧二进制/历史条目保留）

# 1) 源码旧模块前缀
grep -rn "gitea-agent-gateway/" --include="*.go" . | grep -v data/
# 2) 旧展示名（archived / .workbuddy / 备份 / CHANGELOG 除外；含 LICENSE / html / vue）
grep -rIil "Gitea Agent Gateway" --include="*.md" --include="*.yaml" --include="*.html" --include="*.vue" . \
  | grep -v -e docs/archived -e .workbuddy -e config.remote.yaml.bak -e CHANGELOG.md
grep -n "Gitea Agent Gateway" LICENSE
# 3) 旧二进制名 / gitignore / 脚本文档（CHANGELOG 历史二进制提及保留，排除）
grep -rn "gateway-linux\|gateway-windows\|gateway-darwin\|go build -o gateway\|./gateway\|gateway\.exe" . \
  | grep -v -e docs/archived -e data/ -e CHANGELOG.md -e .workbuddy
grep -n "^gateway" .gitignore
# 4) 旧枚举 / 默认路径 / 日志（.workbuddy 日log、备份、CHANGELOG 除外）
grep -rn "gateway_path\|gateway.db\|gateway-e2e.db\|gateway.log" --include="*.go" --include="*.yaml" --include="*.md" . \
  | grep -v -e data/ -e docs/archived -e RENAME-TO-MATEA -e .workbuddy -e config.remote.yaml.bak -e CHANGELOG.md
# 5) 旧产品标识 / 命令 / 失败原因 / OpenCode title（硬切后应为 0；.workbuddy 日log、CHANGELOG 除外）
grep -rn "gateway-agent\|/gateway reset\|gateway restarted\|gateway-task-" --include="*.go" --include="*.md" --include="*.ps1" . \
  | grep -v -e data/ -e docs/archived -e RENAME-TO-MATEA -e .workbuddy -e CHANGELOG.md
# 6) 旧仓库 URL（CHANGELOG 历史 compare 链接保留重定向）
grep -rn "jeeinn/ai-dev" README.md CONTRIBUTING.md SECURITY.md docs/DEPLOYMENT.md docs/OPEN-SOURCE-CHECKLIST.md docs/RELEASE-v0.10.0.md 2>/dev/null
# 7) 前端包名与 title
grep -n "\"name\"" web/package.json   # 期望 matea-web
grep -n "<title>" web/index.html       # 期望 Matea
# 8) .env.example
grep -n "gateway" .env.example         # 期望无产品二进制指代（或仅残留架构词且可接受）
# 9) 长期项目记忆（P3 手动改）
grep -n "gitea-agent-gateway" .workbuddy/memory/MEMORY.md   # 期望 Matea
```

---

## 附录 A：受 import 前缀变更影响的 95 个 Go 源文件（由 `grep -rIl "gitea-agent-gateway/" --include="*.go"` 实测）

> 替换锚点：`gitea-agent-gateway/` → `github.com/jeeinn/matea/`（不含末尾斜杠的孤立字符串不在此列，见 §4 P0-2）。
> 以下为**实际含 import 前缀**的 95 个文件；部分同目录 .go 文件因不跨包 import 而无此前缀，sed 会自动跳过，未列入。

**根 (1)**：`main.go`

**internal/agent (16)**：context.go, conversation.go, loop.go, loop_progress_test.go, loop_test.go, platform_cmds.go, platform_cmds_test.go, tools.go, tools_mcp.go, tools_mcp_test.go, tools_skills.go, tools_skills_test.go, tools_test.go, tools_toolpack_test.go, truncate.go, truncate_test.go

**internal/agents (27)**：coding_backend.go, coding_backend_test.go, commit_message.go, commit_message_test.go, conversation_recorder.go, harness.go, harness_test.go, interaction.go, loop_config.go, loop_config_test.go, manager.go, manager_test.go, opencode_http.go, opencode_http_test.go, prompt.go, prompt_test.go, registry.go, runner_analyze.go, runner_interaction.go, runner_review.go, runner_write.go, runners.go, runners_git.go, runners_git_test.go, runners_test.go, write_workspace.go, write_workspace_test.go

**internal/api (10)**：auth_handler.go, auth_login_test.go, authorize_test.go, config.go, handlers_agents.go, handlers_users.go, middleware.go, router.go, setup.go, workflow_policy_test.go

**internal/dispatcher (11)**：comments.go, dispatcher.go, dispatcher_test.go, executor.go, executor_test.go, gates.go, pipeline.go, queue.go, task_context.go, template.go, template_test.go

**internal/gitea (2)**：client.go, debug.go
**internal/llm (2)**：registry.go, registry_test.go
**internal/mcp (3)**：client.go, client_test.go, registry.go
**internal/sandbox (1)**：audit.go
**internal/store (2)**：conversation_log.go, conversation_log_test.go
**internal/webhook (2)**：handler.go, handler_test.go
**internal/workflow (13)**：context.go, context_test.go, gate_l1.go, gate_l1_test.go, lifecycle.go, lifecycle_test.go, mention_test.go, policy.go, policy_test.go, resolver.go, resolver_test.go, session.go, session_test.go

**tests/integration (5)**：agent_test.go, helpers_test.go, webhook_test.go, workflow_test.go, workflow_unassign_test.go

---

## 附录 B：排除项明细（不改）

- `docs/archived/`
- `data/`（gateway*.db、logs*/gateway*.log、根目录 gateway/gateway.exe 产物）
- `web/node_modules/`、`web/dist/`、`web/package-lock.json`（重生成）
- `data/work*/`、`workspace/sessions/*`（镜像副本）
- `.git` 历史；`.idea/`、`config.remote.yaml.bak`（IDE/备份元数据）
- `CHANGELOG.md` 历史 compare 链接（GitHub 改名后自动重定向）
- GitHub 仓库 URL 历史（保留，依赖重定向）
- 变量名 `gatewayDir`、`$GatewayURL`；测试仓名 `e2e/gateway-poc`
