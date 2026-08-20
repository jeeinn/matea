# 任务清单

> 更新：2026-08-17（Phase 1/2 主链路已归档；**git_sync 按 v3.1 三阶段计划重新排列**，配置自动化继续并行）  
> 产品边界：**Gitea 优先** · 内置 Agent 默认可用 · **可插拔 harness 执行内核**（builtin / OpenCode / Hermes / Phase 3 的 Pi·Codex·Claude） · Matea 是 Gitea 唯一写方 · 不自研 IM SDK · 不引入外部 harness SDK  
> 核心决策：
> - [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md)
> - [20260805-Phase2-plan.md](20260805-Phase2-plan.md)
> - git_sync 三阶段方案（v3.1）→ [20260815-git-sync-3phase-plan.md](20260815-git-sync-3phase-plan.md)
> - 配套评估 → [20260815-git-sync-evaluation.md](20260815-git-sync-evaluation.md)
> - 配置自动化细化方案 → [CONFIG-AUTOMATION.md](CONFIG-AUTOMATION.md)
> 已归档交付记录：
> - **Phase 1 + Phase 2 已完成部分** → [archived/20260814-TASKS.md](archived/20260814-TASKS.md)
> - v0.11.4 任务清单 → [archived/20260803-TASKS.md](archived/20260803-TASKS.md)
> - P0–P2 核心演进 → [archived/20260716-TASKS.md](archived/20260716-TASKS.md)
> - P3 开源 + 开源后加固 → [archived/20260723-TASKS.md](archived/20260723-TASKS.md)
> - 写路径 / Agent 摩擦 / Bootstrap（Issue #4）→ [archived/20260724-TASKS.md](archived/20260724-TASKS.md)
> - 架构评估（点时）→ [archived/20260722-architecture-evaluation.md](archived/20260722-architecture-evaluation.md)

---

## 演进主线

```text
P0–P2 → P3 → 写路径/摩擦/Bootstrap（已归档）→ PR 续作注入 review / 逻辑 Issue 归一 / Agent 并发 / 可观测性（已交付）
        │
        ├─► Phase 1：HubBackend 抽象 + Gitea 触发入口整理 + 体验简化 ✅（已归档）
        ├─► Phase 2：大脑可插拔地基 → Hub 后端接入（OpenCode / Hermes）→ deliver 出站 ✅（已归档）
        ├─► Phase 2 收尾：MCP Server（可选）、剩余跨系统验证场景、配置自动化 🔄（当前）
        ├─► Phase 2.6：git_sync 落地（v3.1：A0 前置验证 + 共存窗口 + 拆细）🔄（当前重点）
        ├─► Phase 3：多触发形态（REST/CLI）+ 策略配置化
        └─► Phase 4：可选 matea gateway serve + 拆包评估
```

---

## Phase 2：Hub 后端接入（剩余待办）

> 方案：[20260805-Phase2-plan.md](20260805-Phase2-plan.md)（D1–D12 已拍板，方案冻结）  
> 已完成部分已归档至 [archived/20260814-TASKS.md](archived/20260814-TASKS.md)。

### 2.3 MCP Server + Deliver（剩余项）

- [ ] **2.3.1 （可选）实现 `internal/ingress/mcp`：Matea 作为 MCP Server**
  若实现则**严格最小 4 工具**：`matea_get_issue_status` / `matea_assign_agent` / `matea_list_agents` / `matea_post_comment`。不做 `create_issue`/`list_open_issues`/`reset_session`。
  📌 降级原因：Matea↔Hermes 主链路为 HTTP Runs API，人类渠道由 Hermes gateway `deliver`（feishu/wecom 原生）承担；2.4 验收不依赖 MCP Server。
  ⚠️ **不可复用现有代码**：`internal/agent/tools_mcp.go` 是 MCP **Client**（Matea 消费外部 MCP），此处需要的是 MCP **Server**（Matea 暴露工具），方向相反，属全新实现，工作量按「从零起」计。
  📌 若落地，暴露的工具集须遵循 2.0.3 分层策略（Gitea 读侧 + 网关级 skill script；沙箱类默认不给）。
  🔶 **决策（2026-08-13）**：本期（phase2/hub-ecosystem 收尾）**不做，向后延迟至后续周期**。理由：MCP Server 为可选项（2.4 验收不依赖），且实现需从零起步（Server 与现有 Client 反向），投入产出比低；不影响 Phase 2 主进度与全量测试。

- [ ] **2.3.2 （可选）MCP 入站鉴权：API Key 或 mTLS**
  默认仅监听 localhost，公网配合反向代理。
  🔶 **决策（2026-08-13）**：随 2.3.1 一并**向后延迟**；2.3.1 不做则本项无承载对象。

- [x] **2.3.3 实现 `deliver` 模块：仅出站事件扇出** ✅（已归档）

- [x] **2.3.4 配置 `deliver.webhook_url`** ✅（已归档）

- [ ] **2.3.5 SystemConfig 页增加 MCP 配置块（Deliver 已完成）**
  SystemConfig 新增「MCP Server」Tab（enable + listen + auth）。即便 2.3.1 可选，配置块保留以便启用。
  🔶 **进度（2026-08-13，S2 + 决策更新）**：「Deliver」Tab 已落地（webhook_url/timeout/max_retries 三字段，见 2.3.4）。「MCP Server」Tab **本期不做、向后延迟**——随 2.3.1 决策（MCP Server 实现延迟），后端无 `mcp.*` schema，做 UI 即无后端支撑的死 UI；待后续周期 2.3.1 落定后再补对应 Tab 与 keys.go 白名单。

- [ ] **2.3.6 飞书/企微/钉钉等渠道不自研 SDK，文档给出推荐拓扑**
  文档给出推荐拓扑。
  🔶 **备注（2026-08-13）**：属文档/架构决策项，非代码阻塞点；「不自研 SDK、走 Hub 或用户 bridge」的立场已在 2.2.4 / 2.3.3 / review 报告多处落地，推荐拓扑文档可在后续周期补，不阻塞本期全量测试。

### 2.4 验证场景（mock Hub 验收，不依赖真实飞书）

- [ ] **2.4.1 飞书用户「帮我看看 issue #12」 → Hermes 经 runs API 查 Gitea 上下文（若 MCP 落地则 `matea_get_issue_status`）→ 回飞书**
  🔶 依赖 2.3.1 MCP Server，向后延迟。

- [ ] **2.4.2 飞书用户「让 AI 改」 → Hermes `matea_assign_agent`（或 Matea HTTP API）→ Matea 执行 → 写回 Gitea**
  🔶 依赖 2.3.1 MCP Server，向后延迟。

- [x] **2.4.3 PR 合并后，Matea deliver 出站事件 → 用户飞书** ✅（已归档）

- [ ] **2.4.4 Gitea Assign `@matea-coder`（backend=hub-opencode）→ OpenCode 执行 → 结果写回 Gitea**
  需在真实 OpenCode 环境下跑通完整写任务链路（workspace 制备 → OpenCode 执行 → patch/summary 返回 → Matea finalize → PR 创建）。当前代码路径已具备，缺端到端真实环境验证。

---

## Phase 2.5：配置自动化与首次用户体验（新增）

> 目标：让 MATEa 配置从「填表」变成「向导」，从 70 个字段变成 15 行 + 点几下。  
> 详细方案：[CONFIG-AUTOMATION.md](CONFIG-AUTOMATION.md)（细化版）  
> 实施顺序建议：P0 与 Phase 2 收尾并行开发 → P1 → P2。

### P0（优先落地）✅ 已完成（phase2.5/config-automation）

- [x] **C-1 `/setup` 向导页面（三步：Gitea → LLM → 确认）**
  前端新建 `SetupWizard.vue`；后端支持批量配置写入；完成后跳转强制改密。
  落地：`web/src/views/SetupWizard.vue`（Token 门禁 → Gitea → LLM → 确认 → 成功页展示一次性 webhook_secret）；批量写入走 `POST /api/setup/complete`。

- [x] **C-2 Setup Token 安全模型**
  首次启动生成一次性随机 token，打印在控制台；30 分钟有效期；与默认密码解耦；未初始化时 `/setup` 可免登访问。
  落地：`internal/api/setup_token.go` — 24 字节随机 hex（48 字符）打印横幅；30min TTL，过期惰性重新生成并再次打印；常量时间比较；完成后 `Invalidate()`。

- [x] **C-3 未初始化时 `/` 自动跳转 `/setup`，初始化后 `/setup` 跳登录**
  前端路由守卫根据 `/api/setup/status` 判断。
  落地：`GET /api/setup/status` 改为**公开端点**（只暴露缺失键名，不暴露值）；`web/src/stores/setup.js` 缓存状态，路由守卫双向跳转。

- [x] **C-4 自动检测本地 Ollama**
  扫描 `http://localhost:11434/api/tags`；命中后自动填充 base_url 并拉取模型列表。
  落地：`GET /api/setup/detect`（1.5s 超时探测），向导 LLM 步骤展示已安装模型标签，点击即选。

- [x] **C-5 自动检测本地 OpenCode（端口可配置）**
  允许用户输入端口，扫描 `/health`；命中后提示可在 Hub 后端配置中接入。
  落地：探测顺序 = 已配置 hub-opencode 后端 URL → localhost:4096 → 8081；冒烟实测命中本机 :4096。端口来源 = 后端配置（C-14 子页面属 P1）。

- [x] **C-6 `gitea.webhook_secret` 可选，留空自动生成**
  修改 `internal/config/setup.go:CheckSetup` 不再把 `webhook_secret` 视为必填；向导完成时自动生成 32 字节 hex 写入 DB。
  落地：CheckSetup 移除该项；`GenerateWebhookSecret()`；`setup/complete` 与 `PUT /api/config`（配置 Gitea 且无 secret 时）均自动生成——空 secret 会静默关闭 webhook 签名校验。

- [x] **C-7 Gitea Token scope 自检**
  `TestConnection()` 调用 `/api/v1/user` 解析权限；至少检查 `write:admin` + `repo`。
  落地：`/api/setup/test/gitea` + 向导测试按钮复用 `TestConnection()`（非管理员时返回 write:admin 警告）；`setup/complete` 服务端强制复测。

- [x] **C-8 配置写入 DB + 热重启组件**
  复用现有 `ConfigManager.Update()` 与 `onConfigChange` 回调；向导完成页批量写入并触发 LLM Registry / Gitea Client / Dispatcher 热更新。
  落地：`setup/complete` 逐项 `Update()`（含 llm.providers 合并保留其他 provider）后 `notifyConfigChange()`。

- [x] **C-9 首次登录强制修改默认管理员密码**
  向导完成后跳转 `/setup-password`；不改密码无法进入 Dashboard；支持 `auth.default_admin_password` 自动生成。
  落地：**此前已完整实现**（`MustChangePassword` 标记 + jwtWrap 403 `must_change_password` + 前端守卫跳 `/change-password` + 改密拒绝默认密码）；向导成功页提示 admin/admin123 首登改密。

- [x] **C-10 Dashboard 初始化引导卡**
  未初始化时显示引导卡片，点击跳转 `/setup`。
  落地：Dashboard 欢迎卡改为 setup-aware（`setupStore.setupRequired` 时也显示，步骤高亮跟随真实进度）；未初始化时路由已全局重定向 `/setup`（C-3）。

### P1

- [x] **C-11 Provider 预设模板**
  DeepSeek / OpenAI / Anthropic / SenseNova / Ollama / 自定义；选择预设后自动填充 base_url + 默认模型，用户只填 API Key。
  落地：`internal/config/provider_presets.go` 单一事实源 + `GET /api/config/provider-presets`（JWT）与 `GET /api/setup/provider-presets`（Setup Token）成对端点；`SetupWizard.vue` 与 `SystemConfig.vue` 均改为从后端拉取同一预设源（移除向导内硬编码；SystemConfig 新增 Provider 对话框「预设」快捷填充）。单测 `TestDefaultProviderPresets` PASS，`go build ./...` + vite build 通过。

- [x] **C-12 自动拉取 LLM 模型列表（即选即拉，未保存 provider 也支持）**
  向导/SystemConfig 填 base_url + api_key + type 后，点「拉取模型」即调用 discover。
  落地：复用 `ConfigManager.discoverModels`（已注入 `modelDiscoveryFn`）；新增导出方法 `DiscoverModels(providerName, baseURL, apiKey, providerType)`；`internal/api/config.go` 新增 `discoverModelsHandler`（POST，校验 base_url 非空否则 400，返回 `{success, source, models, error?}`）；路由 `POST /api/config/discover-models`（JWT）与 `POST /api/setup/discover-models`（Setup Token）成对；前端 `setup.js`/`index.js` 新增 `discoverModels`；`SetupWizard.vue` 默认模型改为「拉取模型」按钮 + 下拉；`SystemConfig.vue` Provider 对话框高级区新增「拉取模型列表」按钮。单测 `TestDiscoverModels` / `TestDiscoverModelsEmptyBaseURL` PASS，`go build ./...` + vite build 通过；`/api/setup/discover-models` 实测：空 base_url→400、不可达→200 干净报错（真实 HTTP 探测已触发）。

- [x] **C-13 站点级 Webhook 自动注册/状态检查（入站 Gitea→Matea）**
  新增 `server.public_url` 配置（默认空=关闭）；SystemConfig 新增「入站 Webhook」Tab：填对外地址后「检查状态」/「自动注册」站点级 webhook（回调固定 `{public_url}/webhook/gitea`）；向导完成时若已配 public_url 则最佳努力自动注册（非阻塞，失败仅日志）。
  落地：`internal/config/schema.go` 加 `ServerConfig.PublicURL`；`keys.go` 注册 `server.public_url`（四处处）；`internal/gitea/hooks.go` 新增 `ListAdminWebhooks`/`CreateAdminWebhook`/`EnsureWebhook`（默认事件 issues/issue_comment/pull_request/pull_request_comment）；`internal/api/config.go` 新增 `giteaWebhookHandler`（`POST /api/config/gitea-webhook`，action=check|register，空 public_url 返回 closed，缺 Gitea 凭据返回 400）；`internal/api/setup.go` 完成时 `go ensureInboundWebhook` 最佳努力注册；`SystemConfig.vue` 新增 Tab + 对外地址输入 + 检查/注册按钮 + 回调 URL 展示。
  测试：`internal/gitea/hooks_test.go`（List/Existing/Create/Empty 4 例）、`internal/api/config_webhook_test.go`（closed/register/check-existing/missing-gitea 4 例）均 PASS；`go build ./...` + vite build 通过。

- [x] **C-14 `agents.backends`（Hub 后端）配置子页面**
  在 `ConfigManager` 中新增对 `agents.backends` 的 CRUD 支持；SystemConfig 新增「Hub 后端」Tab（默认后端选择 + 后端列表表格 + 新增/编辑/删除弹窗，builtin 不可编辑/删除）。
  落地：
  - 配置层 `internal/config/backends.go`：`ParseAgentBackendsJSON`（校验 type/base_url/workspace_transport/default）、`MarshalAgentBackendsJSON`（密码脱敏 `********`）、`MaskSensitiveInBackendsJSON`、`ApplyAgentBackendsJSON`（占位符还原真值）。
  - 注册键 `agents.backends` 到 `keys.go` 五处（configKeys / getConfigValueTyped / applyConfigEntry / getConfigEntry / parseConfigValue）+ `schema.go` 结构 JSON 标签。
  - `internal/api/config.go`：`maskDisplayMap` 与 `maskConfigValue` 对 `agents.backends` 脱敏；新增 `restoreMaskedBackendPasswords` 在 API 层还原 `********` 占位符（与 `llm.providers` 一致的 C-17 模式），避免 DB 持久化掩码值、重启后密码损坏。
  - 前端 `web/src/views/SystemConfig.vue`：「Hub 后端」Tab + 编辑弹窗 + `saveAll`/`applyConfigData` 接入（保存序列化 `backendsConfig`、加载解析 `agents.backends`）。
  - 测试：`internal/config/backends_test.go`（解析/空值/非法 type/base_url/transport/default、脱敏、占位符还原/新密码 9 例）、`internal/api/config_agents_backends_test.go`（PUT 掩码保持真值 + 重启安全、API 层还原 2 例）均 PASS；`go build ./...` + vite build 通过。

- [x] **C-15 精简 `config.example.yaml` 到 ~15 行**
  只保留 `server` / `database` / 可选的 `gitea` / `llm` / `auth`；`workspace`/`logging`/`deliver` 移除（workspace=自动默认 ./data/work，logging=默认 info，deliver=SystemConfig「Deliver 通知」Tab 配置）。YAML 已校验可解析（含内联 map 写法）。

- [x] **C-16 配置变更审计日志**（随 P0 提前完成）
  利用 `operation_logs` 表记录 who/when/key（敏感值 mask）。
  落地：`config_update` / `config_delete` / `setup_complete` 三类动作落 `operation_logs`；token/secret/api_key/password 全 mask，llm.providers 内 api_key 单独 mask；who 维度待用户上下文接入（当前 agent_id=0）。

- [x] **C-17 敏感配置字段 API 不回显、前端 mask**（随 P0 提前完成）
  `gitea.admin_token`、`llm.providers.*.api_key`、`gitea.webhook_secret` 等返回时 mask，表单显示 `••••••••`。
  落地：`GET /api/config` 返回 `********` 占位（结构保留）；PUT 中占位 = 保持原值（含 llm.providers 内 api_key 还原）；测试连接端点对占位回退到已存真值；前端三处掩码提示。

### P2

- [ ] **C-18 一键连接测试（Gitea/LLM/Hub 后端状态灯）**
  Dashboard 或 SystemConfig 顶部显示各组件健康状态。

- [ ] **C-19 健康检查面板**
  汇总 Gitea / LLM / Hub 后端 / Deliver / DB 状态。

- [ ] **C-20 配置导入/导出（JSON，不含 secrets）**
  支持将 `system_config` 导出为 JSON（敏感字段脱敏），并在新实例导入。

- [ ] **C-21 环境变量自动发现与提示**
  启动时检测 `GITEA_URL`、`SENSENOVA_API_KEY` 等，向导中提示「检测到环境变量，是否使用」。

- [ ] **C-22 文件系统可用空间预警**
  启动时 `statfs` 检查 `data/` 所在分区，低于阈值时日志/前端提示。

---

## Phase 2.6：git_sync 落地（v3.1：A0 前置验证 + 共存窗口 + B2 拆细）

> 方案来源：[20260815-git-sync-3phase-plan.md](20260815-git-sync-3phase-plan.md)（v3.1，与本节同步）  
> 决策基调（用户拍板）：**`builtin` 路径完全不动**；hub-* 路径大胆重构；先打通关键路径、把可见难题调研清楚，其余逐步完善。  
> 凭据模型（v3 纠正、v3.1 沿用）：**凭据交给 Hub-* 持有并使用**——Matea 在 `Prepare` 签发限定前缀 read-write deploy key 注入 `GitSyncInfo` 交给 Hub；Hub 持凭据 clone/编辑/commit/push 草稿分支 `matea/hub-{taskID}`；Matea 只 fetch + 三要素校验 + 开 PR（审批）。**绝不给 Hub admin token**。  
> OpenCode 与 Hermes **路径对齐**：两者纯作「远端 Hub」对待，**OpenCode 未必同机 sidecar，也可能非同机部署**；差异只在运行位置，同一 Hub 自 push 契约。  
> v3.1 关键护栏：① 阶段 A 前加 **A0 前置验证**（先证伪再开工）；② 阶段 A 内部 **保留 shared_path/git_sync 共存窗口**（A5 才删）；③ **base_head 漂移默认 fail+告警**（不自动 rebase）；④ **CreatePR helper 提前提取**（A1）；⑤ **SQLite 迁移提前到 A2**；⑥ **B2 拆为 3 子项**。  
> ⚠️ 凭据限制：Gitea 部署密钥为 **repo 级**（read-only / read-write），**无 per-branch 粒度**；「限定分支前缀写」必须由 Matea 三要素校验（分支名 `matea/hub-{taskID}` + `hub_handles` 所有权）在应用层强制。

### 统一 git_sync 模型（v3.1）

```text
                  ┌─────────────────────┐
  hub submit  ──► │ WorkspaceTransport   │  ① Prepare：签发 deploy key（read-write，限定前缀）
  (OpenCode/      │                     │     生成分支 matea/hub-{taskID}，记录 base_head 锚点
   Hermes,        │  Prepare            │     构造 GitSyncInfo（clone_url + 凭据 + draft_branch）
   位置透明)       │  Approve            │
                  │  Cleanup            │
                  └─────────┬───────────┘
                            │ GitSyncInfo
                            ▼
                  ┌─────────────────────┐
                  │ Hub（位置透明）       │  clone → 编辑 → commit → push 草稿分支
                  │ OpenCode / Hermes   │  （持凭据，自 push；差异仅运行位置）
                  └─────────┬───────────┘
                            │ GitSyncResult{DraftBranch, DraftHEAD}
                  ┌─────────▼───────────┐
                  │ WorkspaceTransport   │  ② Approve：fetch 草稿分支
                  │  Approve            │     三要素校验（分支独占 / 起点锚定 / footer）
                  │                     │     复用 CreatePR helper 开 PR（不代 push、不重提交）
                  └─────────┬───────────┘
                            │
                  ┌─────────▼───────────┐
                  │ Cleanup             │  ③ 撤销 deploy key
                  └─────────────────────┘
```

- **`WorkspaceTransport` 接口**：`Prepare`（签发凭据 + 生成分支 + 构造 `GitSyncInfo`）、`Approve`（fetch + 三要素校验 + 开 PR，复用 exported CreatePR helper）、`Cleanup`（撤销凭据）。
- **`GitSyncInfo`**：`CloneURL`（含凭据）、`DraftBranch`、`BaseBranch`、`BaseHEAD`、`CommitAuthor`、`RequiredFooter`、`HubPush:true`。
- **`GitSyncResult`**：`DraftBranch`、`DraftHEAD`（由 Hub 在 push 后回填）。
- **三要素校验**：分支名独占（`hub_handles` 登记）+ 起点锚定（`BaseHEAD`，漂移默认 fail+告警）+ footer `matea-task-id` 签名。
- **共存窗口**：A1-A4 期间 `IsWorkspaceTransportValid` 同时接受 `shared_path` 与 `git_sync`；A5（gating：B1 验收后）才收敛为仅 `git_sync`。

### 阶段 A0：前置验证（spike，不写业务代码）

> 原则：先证伪再开工，避免阶段 A 在未知路径上硬扛。  
> ✅ **已完成（2026-08-17）**：实测报告 → [20260817-a0-spike-results.md](20260817-a0-spike-results.md)

- [x] **A0.1 OpenCode git 能力 spike（~2 天）** ✅（2026-08-17，真实 OpenCode 1.18.4 + docker Gitea 1.22.6 实测通过）
  验证：HTTP API 把 `clone_url`+deploy key 传给 OpenCode；OpenCode 在 `X-Opencode-Directory` 能否 `git clone <带 token URL>` / `git commit` / `git push` 草稿分支；是否支持外部 git 凭据注入。
  结果：全链路通过（base64 注入私钥 → `GIT_SSH_COMMAND` → clone/commit/push `matea/hub-200` 落地 Gitea）。
  ⚠️ 坑已记录：Windows 原生路径、同步 POST 不可作唯一完成信号、无人值守需 `permission.*=allow`。

- [x] **A0.2 Gitea deploy key API spike（~1 天）** ✅（2026-08-17，Gitea 1.22.6 实测通过）
  创建 201 / 列表 / 删除 204 幂等且立即生效；rw key 可 push、ro key push 被拒；**`write:repository` scope 即可，无需 admin**；重复 key 材料 422（每任务须新密钥对）。

- [x] **A0.3 决策点：OpenCode 是否作为阶段 A pilot** ✅（2026-08-17）
  A0.1 通过 → **OpenCode 当 pilot**，阶段 A 按序推进；Hermes 阶段 B 对齐同一契约。

### 阶段 A：git_sync 关键路径打通（OpenCode pilot + 删 shared_path，含共存窗口）

> 目标：在 OpenCode 写链路上让 git_sync 从 Submit 到出 PR 全程跑通（OpenCode 持凭据自 push）；**shared_path 在 A5（gating：B1 验收后）才删**，A1-A4 保留共存窗口。

- [x] **A1 抽象 `WorkspaceTransport` 接口 + `gitSyncTransport` 实现 + 提前提取 CreatePR helper** ✅（2026-08-17，commit de1cefc）
  `Prepare`=签发 deploy key + 生成分支 + 构造 `GitSyncInfo`；`Approve`=fetch + 三要素校验 + 开 PR；`Cleanup`=撤销凭据。定义 `GitSyncInfo`/`GitSyncResult`/三要素。
  **共存窗口**：新增 `WorkspaceTransportGitSync` 常量，`IsWorkspaceTransportValid` **暂时同时接受 `shared_path` 与 `git_sync`**（不提前收紧）。
  **提前提取**：`finalizeWriteTaskPR` 已提取为 `internal/agents/write_pr.go` 的 exported `FinalizeWriteTaskPR`，处理「PR 已存在则更新评论」，供 `Approve` 与 builtin 复用。

- [x] **A2 增量扩展 `TaskContext`/`BackendResult`/`HubHandle` + 提前写 SQLite 迁移** ✅（2026-08-17，commit ec9c949）
  `TaskContext.GitSync`/`BackendResult.GitSync` 已加（omitempty 向后兼容）；`hub_handles` 增 `draft_branch`/`base_head`/`deploy_key_id` 三列，`ALTER` + 默认值迁移已落地（不拖到 C3）。

- [x] **A3 改造 `runViaHub` 区分读/写** ✅（2026-08-17，commit ca3aa07）
  写任务（solve_issue/solve_comment/fix_bug）在 Submit 前 `Prepare` 注入 `GitSyncInfo`；Done 后合成 `GitSyncResult`（确定性分支名 + fetch 权威校验）→ `Approve` 开 PR → `Cleanup` 撤 key；Failed/Canceled/中断同样回收。handle 行持久化 DraftBranch/BaseHEAD/DeployKeyID，重启重连无需重 Prepare。

- [x] **A4 OpenCode 写路径从 `CodingBackend.Run`(shared_path) 切到 `runViaHub` 写通道（前提：A0.1 通过）** ✅（2026-08-17，commit 455d57c）
  `runner_write.go` 中 hub-opencode + git_sync backend 直接走 `runViaHub`；Submit 注入 base64 私钥 + clone/footer/trailer 指引（`BuildGitSyncInstructions`），不再要求 `SandboxPath`；OpenCode 自 push 草稿分支，回传 `matea-draft-head:` trailer 供交叉核对（fetch 远端为权威）。E2E 单测走真实 git file:// 裸仓全通过。

- [x] **A5 干净删除 `shared_path`（gating：B1 验收后才执行，不在 A1-A4 提前删）** ✅（2026-08-18，B1 验收通过后执行）
  删 `CodingBackend.Run` 的 shared_path 写路径（`ResolveCodingBackend` 对 hub-opencode/hub-hermes 改为显式迁移报错；`runner_write.go` 删 opencode 双轨 Handle 持久化/重连块与 CodingBackend 健康检查/builtin 回退死代码）；删 `WorkspaceTransportSharedPath` 常量；`IsWorkspaceTransportValid`/`ValidWorkspaceTransports` 收敛为仅 `git_sync`（`mcp` 列名待 C1 收尾）；`ApplyBackendDefaults` 空值默认 `git_sync`；`ValidateBackendWorkspaceTransport` 对 `shared_path` 给迁移报错并**接入 `LoadWithBootstrap` 启动校验**（陈旧配置启动即失败，不再等到任务期）；`allow_fallback_builtin` 字段标 deprecated（保留 YAML 兼容）。重写 `workspace_transport_test.go`（含 shared_path 拒绝用例）与 `opencode_http_test.go` ResolveCodingBackend 系列；`opencode_hubbackend_test.go`/`hermes/e2e_test.go` 配置填充改 `git_sync`；`gitsync_hermes_test.go` shared_path 用例改为字面量防御性路由断言。读/reply 的 OpenCode SandboxPath 最小工作区契约**保留**（非写 transport 语义）。全量 17 包 PASS。

- [x] **A6 Gitea deploy key 程序化创建/回收（基于 A0.2）** ✅（2026-08-17，commit 8b31654）
  `internal/gitea/deploy_key.go`（Create/Delete/List）+ `internal/agents/deploy_key_issuer.go`（每任务新 ed25519 密钥对，注册 read-write deploy key；Revoke 3 次退避重试 + ctx 感知 + 孤儿 key 告警）；`executor.SetGiteaClientFactory` 用 admin client 接线注入 RunnerFactory；KeyID 持久化到 `hub_handles.deploy_key_id`。**绝不把 Matea admin token 交给 Hub**（Hub 只拿任务级私钥）。

- [x] **A7 测试：fake OpenCode + fake Gitea** ✅（2026-08-17）
  对抗用例全拒（纯函数单测 `workspace_transport_test.go` + 真实 git file:// 裸仓 `gitsync_approve_test.go`）：错分支 / 错起点（orphan）/ 缺 footer / 无改动 / 假完成 / base 漂移（fail+warn）全部拒绝且不开 PR；失败路径 key 仍回收（`TestRunViaHubGitSyncRejectsMissingFooter`）。单测：分支生成 / 三要素 / `Approve` / `Prepare`（无 ssh_url、issuer 失败传播、默认分支回退）/ `Cleanup` nil 安全。E2E：OpenCode solve_issue → Hub 自 push → Matea 开 PR（`gitsync_write_test.go`）。builtin 全量 17 包 PASS 不受影响。真实 OpenCode E2E 归 B5。

- [x] **A8 阶段 A 验收** ✅（2026-08-17，验收报告 → [20260817-a8-acceptance.md](20260817-a8-acceptance.md)）
  真实 OpenCode 1.18.18 + docker Gitea 1.22.6 端到端：任务 8 `success`，OpenCode 自 push `matea/hub-8`（footer `matea-task-id: 8`），Matea 三要素校验后开 PR #4，deploy key 全回收，issue 评论写回成功；`go test ./...` 17 包 PASS；shared_path 保留未动。附带根治 mock SSE 未终止导致的 agent loop 卡死（A0 遗留坑）。

### 阶段 B：Hermes 远端接入 + Session/安全收口

- [x] **B1 Hermes backend 对齐 OpenCode 同一 Hub 自 push 契约** ✅（2026-08-18）
  `buildRunRequest` 注入 `BuildGitSyncInstructions`（与 A4 OpenCode **字节一致**的凭据 + clone_url + draft_branch + footer + trailer 契约，置于 prompt 末尾近因窗口）；本地 Hermes 0.20.3 源码核验 Runs API（`input`/`instructions`/`session_id`/`conversation_history`）无需 schema 变更——契约纯 prompt 驱动，Hermes 用自带工具自 push，trailer `matea-draft-head:` 即 `GitSyncResult` 回传（无 patch 回传特例）。`runWriteTask` git_sync 分支上提至 `ResolveCodingBackend` 之前并经 `resolveGitSyncWriteHub` 统一匹配 hub-opencode/hub-hermes（此前 hub-hermes 写任务在此硬报错 "unsupported coding backend type"）；健康探针失败在 Prepare 签发 key 之前快速失败，git_sync 下**故意不**静默回退 builtin（避免信任模型被替换为 Matea 持 agent token 自 push）。`runViaHub` 部分 `GitSyncResult` 兜底加固（空字段从 Prepare 契约 + trailer 回填）。测试：hermes 包注入/无注入用例 + agents 包 `gitsync_hermes_test.go`（runWriteTask 全链路路由/不健康快速失败/shared_path 不进入 git_sync/部分结果回填），全量 17 包 PASS。真实 Hermes E2E 归 B5。**B1 验收通过，A5（删除 shared_path）解锁。**

- [x] **B2.1 `agent_sessions` schema 迁移**：`WorkspacePath` → `Branch`+`LastHead`+`Memory`（DDL + 迁移 + 默认值）。✅（2026-08-18）
  新增 `last_head`（会话最新草稿分支 head SHA，续作锚点）与 `memory`（会话级滚动摘要，B2.3 注入续作 prompt，与 repo/issue 级 `memories` 表不同）两列：DDL（新库）+ `ALTER TABLE ... DEFAULT ''`（存量库幂等迁移）；Session 结构体 + 全部 7 处 CRUD/查询点同步；`WorkspacePath` 字段与列**保留并标 deprecated**（消费方在 B2.2 切换后删列）。测试：往返读写 + Update 覆盖 + 旧行默认值（无 NULL scan 错）。全量 17 包 PASS。

- [x] **B2.2 `prepareWriteWorkspace` session 分支改造**：续作逻辑从依赖 `WorkspacePath` 改为基于 `LastHead` 起新草稿分支。✅（2026-08-18）
  续作全 git 原生化：session 任务不再复用磁盘 workspace——每个写任务全新 task 级 sandbox + clone（续作走新增 `Git.CloneFull`，浅克隆够不到草稿分支上的锚点 SHA），随后 `git checkout -b <branch> <LastHead>` 锚定；锚点丢失（远端草稿分支被删/回卷）→ 明确报错 "session continuation anchor ... not found"（提示归档会话），不静默重开。存量 session（只有 `Branch` 无 `LastHead`）回退 `prepareExistingBranch`（锚远端分支头）；`task.BaseBranch` 非空（solve_comment PR head）优先于 LastHead 锚定。分支命名沿用 `resolveBranchPlan`（复用 session.Branch → 同一 PR 持续更新，一 issue 一 PR 流不变）。删除 `syncSessionWorkspace` 及其 3 个测试（磁盘同步逻辑整体下线）；`workflow.SessionService` 不再给 coder 会话分配 `WorkspacePath`（deprecated 列保留供 lifecycle GC 回收存量目录）；sandbox 清理改为无条件 `defer`（`UseSession` 语义转为"finalize 时记录 branch+head"）。新增 `saveSessionProgress`：push 成功后同写 `Branch`+`LastHead`（两处 push 点：finalize 常规 push + 无变更兜底 push）。验收测试 `write_workspace_continuation_test.go`（真实 git file:// 远端，bare HEAD symbolic-ref 修正）：① main 分叉后续作锚定 LastHead（HEAD==LastHead 且 main 新提交不泄漏）② 锚点丢失报错 ③ 存量 session 回退远端分支头 ④ 新 session 全新分支+记录 ⑤ saveSessionProgress 单元。全量 17 包 PASS。

- [x] **B2.3 Hub 侧 LastHead 续作契约与测试**：OpenCode/Hermes 下次任务基于 `LastHead` 起新草稿分支；`memories` 表 + session 记忆注入 prompt。✅（2026-08-18）
  **续作锚点契约**：`GitSyncInfo.AnchorHEAD`（空=BaseHEAD，语义不变——非续作指令字节不变）。续作任务（session.LastHead 非空）的 hub 从 LastHead 起**新的** per-task 草稿分支（`BuildGitSyncInstructions` 渲染 `git checkout -b <draft> <anchor>` + 续作说明"锚点不可达则 STOP 报错，不回默认分支重开"）。三要素校验锚点化：ancestor 检查 + footer 范围改为 `anchor..head`（续作只签自己任务内的提交——上一任务的提交带旧 footer，范围含它必误判）；**base 漂移检查仍锚 BaseHEAD**（窗口=本任务 Prepare→Approve，与续作无关）。`Approve` 成功后把 `result.DraftHEAD` 归一化为 fetch 到的权威值（hub 漏报 trailer 也能记准 LastHead）。**持久化**：`hub_handles.anchor_head` 新列（DDL+幂等 ALTER），重接时从行重建锚点而非重读 session——并发同会话任务推进 LastHead 后重接校验仍用原锚点。**写回**：Approve 成功后 `saveSessionProgress`（Branch+LastHead，空 head 不覆盖好锚点）+ 新增 `saveSessionMemory`（latest-wins 滚动摘要，4000 rune 上限，带 task 前缀）。**记忆注入**：`TaskContext.SessionMemory` 新字段 + 共享渲染器 `BuildMemoryContext`（session 块在前、repo/issue keys 排序输出）；`buildHubWriteTaskContext` 注入 `loadMemoryKeys`+`loadSessionMemory`（此前写任务完全不带 memories 表）；Hermes 换用共享渲染器；**OpenCode 此前完全丢弃 MemoryKeys——已修复**，记忆块置于 git 契约之前（近因窗口保留给强制工作流）。测试 `gitsync_continuation_test.go`：校验锚点单测×3 + 指令渲染 + BuildMemoryContext；真实 git 对抗——跨 base 移动续作通过（范围/祖先锚定 LastHead 的直接证据）+ 从 base tip 起分支被拒；runViaHub 全链路（AnchorHEAD 下发+handle 持久化+LastHead/Memory 写回+key 回收）；全新 session 无锚点；buildHubWriteTaskContext/OpenCode Submit 记忆注入；writer 守卫。hermes 包补 session memory 渲染用例。全量 17 包 PASS。

- [x] **B3 diff 白名单默认开基础校验** ✅（2026-08-18）
  `allowed`/`denied_paths`；越权 diff 落库审计。三要素证明草稿分支"从哪来"，diff 白名单约束"碰什么"——hub-push 信任模型下最高危泄漏面是密钥材料进草稿分支（任务级 deploy key 就在 hub 工作区 clone 上一级目录）。**内置 deny 默认常开**：`.env`/`.env.*`、`*.{pem,key,p12,pfx}`、`id_rsa*`/`id_ed25519*`、契约自身的 `key` 文件（BuildGitSyncInstructions 第 2 步）；无 `/` 模式同时匹配 basename（`.env` 覆盖 `config/.env`），尾部 `/*` 递归（`vendor/*` 覆盖任意深度——path.Match 的 `*` 不跨 `/`，不递归会静默缩窄安全规则）。**deny 绝对优先**（allowed 不能豁免 deny）；`allowed_paths` 非空时变更为进一步收敛（每个变更路径须命中其一）。配置：`BackendConfig.AllowedPaths/DeniedPaths`（yaml `allowed_paths`/`denied_paths`），启动期 `ValidateBackendDiffPaths` 校验 glob 语法（非法 pattern 在 Approve 永不命中 = 静默失效，必须 fail loud）。实施：`fetchedDraft.ChangedPaths`（`git diff --name-only anchor..head`，续作同样锚定范围）→ 校验第 4 要素 → 类型化 `DiffPolicyViolationError`（`NewGitSyncTransport` 加 policy 参数，`gitSyncTransportFor` 从 backend 配置注入）→ runViaHub `errors.As` 捕获写 `operation_logs`（action=`git_sync_diff_violation`，含 backend/branch/paths）。测试 `gitsync_diffpolicy_test.go`：模式匹配（含递归语义）、内置默认、配置扩展、allow 收敛+deny 优先、类型化错误、真实 git Approve 拒绝带 `key` 的签名草稿、runViaHub 全链路（报错+审计行+key 回收+无 PR）、干净 diff 回归；config 包 `ValidateBackendDiffPaths` 用例。全量 17 包 PASS。

- [x] **B4 凭证最小权限复核 + 生命周期 hook** ✅（2026-08-18）
  deploy key 随 `hub_handle` 删除/失效回收（与 A6 衔接）。**最小权限复核结论**（逐条源码核验）：① hub 收到的只有 `GitSyncInfo{SSH clone_url + 任务级 deploy key 私钥(base64)}`——admin/agent token 从不出现在契约或指令里（Prepare 用 `SSHURL` 非带凭据 https URL；Approve 的 fetch 凭据仅在 Matea 侧）；② deploy key 为 repo 级 read-write（push 必需；Gitea 无 per-branch 粒度 → 由三要素校验 + B3 diff 白名单在应用层补偿）；③ issuer 复用 admin client，A0.2 spike 实证 `write:repository` scope 足够（**无需 site admin**——运维侧 admin_token 可按此收紧）；④ 私钥每任务新生成（ed25519/crypto/rand）、**不落库**（hub_handles 只存 DeployKeyID）、不进入 conversation/audit 日志（hub 路径不过本地 AgentLoop/sandbox audit）；⑤ 全部终态路径（Done/Failed/Canceled/abort/poll 错/Submit 错）内联 revoke。**生命周期 hook（新增 sweep 兜底）**：`SweepOrphanedDeployKeys` 覆盖三个残留泄漏窗——revoke 重试 3 次仍失败、Prepare→SaveHubHandle 间崩溃（key 已签发但无行记录）、未来 handle 行删除遗留。算法：扫描有 handle 行的 repo → `ListDeployKeys` → 仅处理 `matea-hub-task-` 前缀（运维自建 key 永不触碰）→ 任务无非终态 handle 行（在跑/重接中的受保护）且超过 30min 宽限期（覆盖 Prepare→persist 竞态）→ revoke + `operation_logs` 审计（`git_sync_key_swept`）；单 repo 失败不阻断全局。支撑：`DeployKey.CreatedAt` 新字段；store 新增 `ListHubHandleRepos`/`ListNonTerminalHubTaskIDsByRepo`（join tasks）；Prepare 标题改用共享前缀常量。接线：Executor `startDeployKeySweepLoop`（`sync.Once` 防配置重载重复启动；启动即扫 + 10min 周期，与 session cleanup 同节奏；每 tick 重取 admin client 使重载生效）。测试：store 两查询（保护集/终态/无行 repo）；sweep 六场景（终态回收/运行中保护/新鲜宽限/崩溃窗回收/外来 key 豁免/畸形标题豁免）+ 单 repo 失败隔离 + nil 参数。全量 17 包 PASS。

- [x] **B5 E2E（真实 OpenCode + 真实 Gitea 沙箱）+ 对抗测试强化** ✅（2026-08-18）
  **对抗强化**：修复 `runViaHub` StateDone 路径在 `Approve` 失败时仍把 `hub_handles` 标为 Done 的缺陷——现在仅当四要素校验全部通过才标 Done，任何越权/无效草稿（错分支、缺 footer、base 漂移、diff 违规、pushed nothing）都标 **Failed** 并回收 key。新增单测：`TestRunViaHubGitSyncWrongBranchRejected`（hub 自择分支 → fetch 不到 mandated draft → Failed）、`TestRunViaHubGitSyncMissingFooterRejected`（mandated 分支存在但提交无 footer → Failed）、既有 `TestRunViaHubGitSyncWritePathHubPushedNothing`/`TestRunViaHubGitSyncDiffViolationAudited` 补 handle 状态断言。base 漂移已在 `TestGitSyncApproveContinuationRejectsBaseTipStart`/`TestGitSyncApproveRejectsBaseDrift` 覆盖，行为仍是 fail+不开 PR+不自动 rebase。**真实 E2E harness**：新增 `internal/agents/gitsync_e2e_test.go`，环境变量门控（`MATEA_E2E_GITEA_URL`/`MATEA_E2E_GITEA_TOKEN`/`MATEA_E2E_GITEA_SSH_PORT`），未设置时 SKIP 不影响 CI：
  - `TestE2EGitSyncDeployKeyLifecycle`：复现 A0.2 结论，`write:repository` token 可签发 rw deploy key，key 可 SSH clone/push，DELETE 后立即失效；
  - `TestE2EGitSyncFullCycle`：真实 Gitea 上完整 Matea-side git_sync（Prepare→模拟 hub push→Approve 开 PR→Cleanup 回收 key）。
  **真实 E2E 验证通过（2026-08-18，`data/e2e/gitsync-e2e-report.html`）**：本地 Docker Gitea 1.22.6 + OpenCode + mock_llm 全链路——webhook assign 触发 → Prepare 签发 task-scoped key → hub SSH clone/commit(footer)/push `matea/hub-6` → 三要素 Approve 通过开 PR #10 → deploy key 计数归零（用后即焚成立）。OpenCode bash 不落盘的 rig 怪象用 `mock_llm.py --mode local` 绕过（mock 真实子进程跑 git 步骤，OpenCode 仅作消息载体；Matea 侧校验仍对真实远端分支权威执行）。
  全量 17 包 PASS。

### 阶段 C：清理、文档与发布

- [x] **C1 删除 `mcp` transport（仅 workspace transport 用途）** ✅（2026-08-19）
  保留 `internal/agent/tools_mcp.go` 的 agent MCP tools 能力（与 workspace transport 无关），手术式删除：`WorkspaceTransportMCP` 常量移除；`ValidWorkspaceTransports()` 收敛为仅 `[git_sync]`（与早已拒绝 mcp 的 `IsWorkspaceTransportValid` 消除不一致——此前前者仍列 mcp，WebUI/校验两侧语义分裂）；config 报错文案更新为 "removed in C1（Phase 3.9 回归）"。测试 pin：`shared_path`/`mcp` 均不得在 valid 列表、`mcp` 配置仍 fail loud、`ValidWorkspaceTransports` len=1。全量 17 包 PASS。

- [x] **C2 文档收尾** ✅（2026-08-19）
  新建 **`docs/HUB-BACKENDS.md`**：git_sync 信任模型（Hub 持凭据自 push、Matea 审批开 PR，admin/agent token 永不出 Matea）、四要素校验表、凭据最小权限模板（`write:repository` 即可）、Hub 接入契约（GitSyncInfo 字段 + 6 条必须项 + trailer）、两后端配置样例、失败语义速查、性能预算。修 **CHANGELOG**（「Phase 2 未合入 master」已过时 → 标注 PR #28 已合；新增 git_sync 阶段 A–C 条目）。`remote-hub-deployment-flow.md` 顶部加实现状态横幅：git_sync 当前唯一 transport、shared_path(A5)/mcp(C1) 已删，全文档三处标注。

- [x] **C3 SQLite 迁移收尾校验** ✅（2026-08-19）
  源码核验 `hub_handles` A2 列（draft_branch/base_head/deploy_key_id）+ B2.3 anchor_head + B2.1 agent_sessions(last_head/memory) 全部双轨落地（CREATE TABLE 新库 + `additionalMigrations` 幂等 ALTER 老库，duplicate/no-such-table 容忍）。补旧 config.yaml 迁移示例：`config.full-example.yaml` backends 段重写——旧 `shared_path`/`mcp` 行直接删除即可（git_sync 为默认）、废弃字段 `workspace_mode`/`allow_fallback_builtin` 建议删除说明、补 hermes-local 样例与 B3 `allowed_paths`/`denied_paths` 注释；YAML 解析验证通过。

- [x] **C4 性能预算** ✅（2026-08-19）
  `internal/agents/gitsync_perf_test.go`：**CI 内回归** `TestGitSyncApproveLargeRepoStress`（fast-import 合成 300-commit 仓库 + 5 commit/150 路径草稿 → Approve 全过 + fetch 证据范围完整，无计时断言防 flake）；**opt-in benchmark** `BenchmarkGitSyncApprove` 三档（50/1k/5k base commits）。实测（Windows 11，file://）：1.4–1.8s/op，历史长度本地不敏感、固定进程/协议开销为主。**SLO 落档 HUB-BACKENDS.md §七**：Approve Matea 侧 ≤2s @5k commits、≤1.5s @1k；超预算既定缓解 `--filter=blob:none`（merge-base/log/diff --name-only 无需 blob，传输降一个数量级）。修复 fixture bug：fast-import 流内每个 commit 都带 `from` 会反复重置父提交（draft 链断成单提交）——仅首 commit 携带。

- [x] **C5 阶段 C 验收** ✅（2026-08-19，发版按用户决定留待手动）
  **验收清单全过**：① 配置仅 `git_sync` 一种 transport（C1 后 `ValidWorkspaceTransports()=[git_sync]`，测试 pin 死）；② 全量 17 包 `go test ./...` PASS + `go vet` 干净 + `go build` 成功；③ 文档齐备——[HUB-BACKENDS.md](HUB-BACKENDS.md)（信任模型/契约/SLO）、CHANGELOG（Phase 2 合入标注 + git_sync A–C 条目）、remote-hub-deployment-flow.md（实现状态横幅）、config.full-example.yaml（迁移示例）；④ 工作树干净，`phase2.6/git-sync` 领先 master 19 commits（A0–C5 全链）。**发版（新 tag）按用户决定留待手动执行**——分支已就绪，可随时合并 master 并打 tag。

---

## Phase 3：多触发形态与策略配置化（3–6 月）

- [ ] **3.1 实现 `internal/ingress/api`：REST API 直接提交任务**
  `POST /api/v1/tasks` 标准化入口。

- [ ] **3.2 CLI 触发：`matea run --repo=... --issue=... --agent=...`**

- [ ] **3.3 把 workflow gate 拆为独立策略单元**
  `require-analysis-before-code`、`no-self-review` 等可配置。

- [ ] **3.4 策略 Skill 化：高级用户可写 YAML/JSON 策略**
  与 Agent 解耦。

- [ ] **3.5 Cron 扫描：定时处理 backlog issue/PR**
  可选触发方式。

- [ ] **3.6 评估是否拆包：`matea-core` / `matea` / `hub-hermes` / `hub-opencode`**
  先在一个仓库内抽象，后端数量增多后再拆。

- [ ] **3.7 新增 harness：Pi / Codex / Claude Code（D12，机制验证后 trivial）**
  接入方式 = **spawn 其 CLI 或连其本地 server（out-of-process）**，**不引入任何外部 SDK**（全为 TS/Python，无法嵌进 Go 进程）。每新增一个 = 实现一个 `Harness` + 注册表加一行。

- [ ] **3.8 Gitea 写侧工具评估（D11 挂账）**
  `gitea_create_comment` / `gitea_create_pr` 是否暴露给 harness。风险：推理中途可写 → 乱序评论、重复评论、部分失败不可回滚。需先给出幂等与回滚方案再评估。

- [ ] **3.9 `workspace_transport=mcp`（L2 全隔离档，D11 部署档位）**
  真异地/跨组织边界部署，工作区经 MCP 文件工具抵达。⚠️ 性能税：agent loop 内每次 `read_file` 一次 round-trip，大仓库比 L0 慢一个数量级 —— 是**为隔离付费，不是升级**。依赖 2.3.1 MCP Server 落地。

- [ ] **3.10 MCP Server 实时工具桥 + 网关级 skill script 远程执行（D11 安全前置）**
  必须同时满足：① 只在沙箱工作目录内执行；② API Key 鉴权；③ 单 workflow 作用域绑定。三者缺一不可。

---

## Phase 4：企业网关 + 拆包评估（远期）

- [ ] **4.1 实现 `matea gateway serve` 子命令**
  集中处理：幂等 / 同 issue 锁 / 多 backend 路由 / 审计门禁。

- [ ] **4.2 单用户默认 `matea serve`；企业用 `matea gateway serve`**

- [ ] **4.3 多 VPS 调度扩展：`hubapi.Provider` 按 task/repo/channel 选择 Hub 实例**
  首版手动配置，需要中央调度时再实现。

- [ ] **4.4 拆包评估：core / adapter / gateway 独立仓库/模块**
  视后端数量和社区需求决定。

---

## 继续保留的「按需 / 可选」项

> 来自 v0.11.4 清单，未纳入主线的可选增强，继续延后或按需启动。

| 类别 | 项 | 说明 |
|---|---|---|
| WebUI | 高亮未推送分支 | 可选，当前未推送给提示 |
| 运维 | bootstrap 启动日志打印「Logging to file: …」 | 可选提示 |
| 沙箱 | `cat` 行号范围、`find` glob、审计日志内容摘要 | 按需 |
| 沙箱 | 拦截 agent 侧 `git commit` / `git push` | 可选安全增强 |
| LLM | tiktoken 精确计数 | 可选，有估算即可 |
| LLM | 超长 Session 语义摘要 | 可选 |
| LLM | per-task 成本预算上限 | 可选 |
| 继续延后 | 文件级 analyze 落地 checklist | soft gate 文案已归档 |
| 继续延后 | API 中间件链 | 有运维痛点再立项 |
| 继续延后 | `gitea.Client` Transport 显式复用 | DefaultTransport 已够用 |

---

## 明确不做

| 项 | 说明 |
|---|---|
| GitHub / GitLab / Gitee 多平台 Host SPI | Gitea-first |
| Issue 级任意 PR base（label/body） | 边缘场景 |
| 自研飞书/企微/Slack SDK | 由 Hub 或用户自配 bridge 解决 |
| 把 Matea 降级为纯库 | 默认仍是可独立运行产品 |
| 强制用户先装 Hub 才能使用 | builtin 默认可用 |
| 同 Agent 忙碌时硬拒绝入队 | 串行必须可排队 |
| 自动双向同步 Matea Prompt 与 Hub Skill | Phase 2 手动 export/import，不做自动写 |
| 接管远程 harness 自身的 skill/plugin 机制 | OpenCode plugin、Claude Code skills 各管各的；Matea 只保证自己的 skill 能被任何 harness 用到（D11） |
| 用 ToolBox 替换成品 harness 自带工具 | 只能 append 不能 replace；重名会导致模型在两套同功能工具间摇摆（D11） |
| 给远程 harness 塞一套沙箱文件工具 | 负收益：它自带且直读本地盘更快，经 ToolBox 多一个 round-trip（D11） |
| 引入外部 harness 的 SDK（Pi/Codex/Claude/OpenCode） | 全为 TS/Python，无法嵌进 Go；统一 out-of-process spawn/connect（D12） |

---

## 现行文档（非归档）

| 文档 | 用途 |
|------|------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | 现行架构 |
| [AGENTS.md](AGENTS.md) | Agent 配置与 role 说明 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | 部署 |
| [CONFIG-AUTOMATION.md](CONFIG-AUTOMATION.md) | **配置自动化细化方案（Phase 2.5）** |
| [20260805-Phase2-plan.md](20260805-Phase2-plan.md) | Phase 2 实施方案（D1–D12 已拍板，方案冻结） |
| [20260815-git-sync-3phase-plan.md](20260815-git-sync-3phase-plan.md) | **git_sync 三阶段方案（v3.1，当前重点）** |
| [20260815-git-sync-evaluation.md](20260815-git-sync-evaluation.md) | git_sync 评估与社区解法对照 |
| [20260817-a0-spike-results.md](20260817-a0-spike-results.md) | **A0 前置验证实测报告（OpenCode + Gitea deploy key，均通过）** |
| [20260817-a8-acceptance.md](20260817-a8-acceptance.md) | **A8 阶段 A 验收报告（真实 OpenCode+Gitea 端到端出 PR，通过）** |
| [matea_产品演进实施计划_保留产品形态_引入_hub_后端.md](matea_产品演进实施计划_保留产品形态_引入_hub_后端.md) | 产品定位与演进规划 |
| [server-runtime-design-v4.md](server-runtime-design-v4.md) | OpenCode / CodingBackend（待按 HubBackend 刷新） |
| [todo-20260714-LLMProvider-可选增强.md](todo-20260714-LLMProvider-可选增强.md) | LLM 可选增强 |
| [archived/20260814-TASKS.md](archived/20260814-TASKS.md) | **Phase 1 + Phase 2 已完成部分归档** |
| [archived/20260804-Phase1.5-plan.md](archived/20260804-Phase1.5-plan.md) | Phase 1.5 计划（已收官归档） |
| [archived/20260805-Phase2-evolution-direction.md](archived/20260805-Phase2-evolution-direction.md) | Phase 2 演进方向讨论（结论已并入 Phase2-plan） |
| [archived/20260808-IM-channel-integration-analysis.md](archived/20260808-IM-channel-integration-analysis.md) | IM 渠道接入分析（结论已并入 D5/D6） |
| [archived/20260803-TASKS.md](archived/20260803-TASKS.md) | v0.11.4 任务清单归档 |
| [archived/](archived/) | 历史设计、签核、E2E |
