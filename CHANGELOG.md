# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **PR 标题改用真实 issue/PR 标题**：此前标题取自 `task.Event`（webhook **事件名**），于是每个 PR 都叫 `AI Solution: issues`（`jeeinn/rust-study` PR #8 即如此），对评审者零信息量。现优先取关联 issue / PR 的真实标题（超 60 字截断），取不到时退化为 `Task {id}`；事件名不再作为标题来源
- **PR 相关文案中文化**：PR body 标题 `## AI Generated Solution` → `## AI 生成的解决方案`；创建回执 `✅ PR created: #N` → `✅ PR 已创建：#N`；更新分支回执 `🔄 Updated PR branch \`x\` with new changes` → `🔄 已更新 PR 分支 \`x\``
- **评论 footer 用 Agent 名替代数据库 ID**：`*Task ID: 16 | Agent: 3 | Type: review_pr*` 里的 `3` 是 `agents` 表行 id，读者无从对应。现渲染为 Gitea 用户名（如 `@code-review`），无关联账号时回退内部名
- **L3 通知模板不再内联绝对 URL**：`L3CoderPROpened` 的 `{{pr_url}}` 由服务端 `gitea.url` 拼出，docker-compose 下是内网地址（实测 `http://localhost:3000/...`），外部打不开。改为原生 `#N` 引用（Gitea 按自身 ROOT_URL 渲染，且带 PR 状态）；同时新增 `{{reviewer_hint}}`——存在 review 角色 Agent 时具名提示（`@code-review`），否则提示先指派一个，替代原来"Request reviewer Agent 进行代码审查"这种没说找谁的措辞

### Fixed
- **code review 状态卡永远停在「处理中」**：`postL3Notification` 的 switch 只覆盖 `analyze_issue` 与写任务类型，`review_pr` / `reply_comment` 直接落空，`completeStatusCard` 从未被调用。`jeeinn/rust-study` PR #8 上一条 56 秒就跑完的 review，卡片至今显示 `🔄 处理中`。现补齐这两类（detail 留空——翻转状态是卡片机制本身的职责，不应依赖 Notify 开关）
- **review 状态卡建在一处、完成在另一处**：状态卡创建用的是 `issueID`（`effectiveIssueKey`，偏向关联 issue），而结果评论与失败写回用的是 `writebackTargetID`（`review_pr` 偏向 PRID）——两套优先级相反，导致 PR 上 review 的卡建在别处、完成无处可 PATCH。现两侧统一走 `writebackTargetID`
- **状态卡的任务 ID 不再渲染成 `#N`**：Gitea 会把 `#N` 自动链接到本仓库同号的 issue/PR，于是 task 16 的卡片显示成指向 PR #16 的链接——一个完全不相干的对象。现直接输出数字（如 `| **任务** | 16 |`）。`L3AnalyzeDone` 的 `task #{{task_id}}` 同步改为 `任务 {{task_id}}`
- **git_sync 续写 PR 开错 base（start-point anchoring 事故修复）**：`Prepare` 此前把 `store.Task.BaseBranch`（语义是 PR head / 会话工作分支）当作合并基线，导致 PR 上二次 `@code-opencode` 的续写任务把新 draft 的合并目标算成上一轮的 draft 分支（`matea/hub-14`），漂移检测窗口也错放在 draft 分支上。现改为：有 `task.PRID` 时取 PR 的 `base.ref`，否则取仓库默认分支；`hub_handles` 新增 `base_branch` 列持久化该结果，重启 re-attach 对旧行按同一规则重新解析，不再回退到 `task.BaseBranch`；draft 分支（`matea/hub-*`）永不作为基线
- **git_sync 续写锚点从「建议」升级为契约**：下发给 hub 的 git workflow 明确要求完整克隆（禁 `--depth` / `--single-branch`，clone 命令带 `--no-single-branch`），并在 `git checkout -b <draft> <anchor>` 后追加 `git merge-base --is-ancestor <anchor> HEAD` 自校验，hub 一旦从默认分支 tip 重新起分支就在这一步失败，而不是等整轮跑完才在 Approve 报错
- **hub-opencode 的 memory 注入不再被 git 契约覆盖**：git_sync 分支此前从 `tc.UserPrompt` 重新拼装 prompt，把已拼好的会话 / repo memory 整块丢掉（Hermes 后端无此问题），续写任务的 hub 因此完全不知道上一轮做过什么。现改为在 memory 之后追加契约：memory 在前、强制 workflow 在后
- **start-point anchoring 报错可诊断**：draft 从 base tip 起分支时，报错直接说明「从 base tip 起分支、上一轮工作不在分支内」并给出正确的 `git checkout -b` 命令；无共同祖先时提示 `shares no history`；`fetchDraft` 在锚点对象缺失时按 sha 兜底抓取一次，避免「抓不到」被误判为「起点错」

## [0.12.1] - 2026-08-28

### Added
- **Agent 显式接管已存在的 Gitea 账号**：创建/更新 Agent 时勾选 `take_over_gitea_user` 即可显式接管同名 Gitea 账号（重置密码、签发 Token 并标记 `managed_by_matea`），解决此前仅有错误提示却无入口的问题
- **工作分支前缀统一为 `matea/`**：builtin 写路径分支从 `ai/{type}/issue-{id}` 改为 `matea/{type-kebab}-{id}`，与 hub 后端 `matea/hub-{taskID}` 统一品牌感知
- **hub 后端对话日志透传**：`BackendResult` 新增 `Messages` 字段，`hub-opencode` / `hub-hermes` 把完整对话 transcript 带回 Matea；任务详情页 `TaskConversation.vue` 复用 `debug.conversation_log.enabled` 开关展示 hub 后端对话
- **对话日志记录初始 system/user 输入**：agent loop 启动即写入首条 system/user 消息，便于追踪完整上下文
- **solve_comment 评论注入条数可配置**：新增 `agents.loop.solve_comment.max_injected_comments`（默认 50），控制注入 solver 的上下文评论数量
- **Linux 交叉编译脚本**：新增 `scripts/build-linux.sh`（Bash）与 `scripts/build-linux.bat`（Windows CMD），先构建 Web UI 再交叉编译 linux/amd64/arm64 二进制

### Changed
- **默认重试/超时策略收紧**：单次任务默认超时 5m → 20m；429 退避默认 60s、429 重试默认 10 次，缓解大模型限流导致失败
- **SenseNova / DeepSeek 预设 URL 修正**：`config.full-example.yaml` 与 provider preset 同步最新端点
- **PR 创建评论改用 Gitea 原生 `#N` 引用**：替代此前依赖 `pr.HTMLURL` 的完整链接，规避 ROOT_URL 配置错误导致的内网/不可点击链接
- **hub-opencode 模型透传策略**：不再默认透传 Agent 的 provider/model（避免命中 OpenCode 未知或付费模型）；仅当 `backend_options` 成对填写 `opencode_model` + `opencode_provider` 时才投递，否则使用 OpenCode 服务端默认模型
- **agents 配置热更新后 hub backend 免重启**：Web UI 新增/修改 `agents.backends`、`agents.defaults`、`agents.loop` 后，Dispatcher/Executor 自动重建 RunnerFactory，无需重启服务

### Fixed
- **Gitea Token 权限指引错误导致初始化被拦截**：向导/文档此前只提示 `write:admin` + repo，但 Gitea ≥1.22 细粒度 scope 各类别相互独立，`GET /user` 需要 `read:user`——按旧文案生成的 Token 在「完成初始化」时 403。`gitea.TestConnection()` 重写为分项权限探查（identity/repo/issue/admin），返回结构化 `checks` 与 `required_scopes`；解析 Gitea scope 拒绝报文给出精确缺失提示（如「Token 缺少 read:user（当前仅含：write:admin, write:repository）」）
- **向导第 1 步权限预检**：Token 输入框下方列出精确 scope 清单（`read:user` / `write:repository` / `write:issue` / `write:admin`）；「测试连接」逐项展示权限检查结果，未通过前「下一步」禁用（修改地址/Token 后需重测）；第 3 步失败可一键返回第 1 步；完成页展示降级警告（如非站点管理员）
- **文案同步修正**：SystemConfig Gitea Token 提示、README 权限清单（补 `read:user`/`write:issue`）、`docs/DEPLOYMENT.md`（含 403 scope 报错 FAQ）、`config.full-example.yaml` 注释
- **session 分支不存在时降级为新建分支**：修复首个任务失败（如 429）后分支未推送，后续任务按"已存在分支"检出导致硬失败的问题
- **solve_comment 会话分支注入 BaseBranch 时同样允许全新创建**：与上述分支降级对齐
- **hub-opencode 运行失败时透传 provider 真实错误**：OpenCode 返回 `info.error` 时（如 401/额度/模型不存在），不再吞掉为 "no assistant text message found"，直接把 provider 错误抛给上层
- **hub 对话日志审查修复**：Poll cache-miss 分支保留重启后失败会话的 transcript；`StateCanceled` 与 `abortHubRun` 分支也写入对话日志；iteration 0 去重、assistant 迭代续号；失败路径 `BackendResult` 不再填充 `GitSync`
- **Web UI 细节**：HTTP 环境剪贴板复制 fallback；模型发现后默认模型标签刷新；webhook 设置文案更明确

## [0.12.0] - 2026-08-21

任务清单复盘：砍掉 Phase 3/4 远期与不相关项（MCP Server、CLI、harness 写 Gitea、新 harness 接入、拆包、企业网关），仅保留贴近现状的 2.4.4 真实 Hub E2E 验收与 3.1/3.3/3.4/3.5 轻量可选增量；原「未发布」变更一并归入本版本。

Phase 2.5 配置自动化与首次用户体验（branch `phase2.5/config-automation`，P0 + 提前落地的 P1 项）：

### Added
- **首次运行向导 `/setup`**（C-1/C-3）：未初始化实例所有页面重定向到三步向导（Gitea → LLM → 确认）；`GET /api/setup/status` 改为公开端点（只暴露缺失键名）；初始化完成后 `/setup` 自动关闭并跳登录
- **Setup Token 安全模型**（C-2）：首次启动控制台打印一次性随机 Token（48 hex，30min TTL，过期惰性重新生成再打印，常量时间比较），门禁全部 `/api/setup/*` 写端点；与默认管理员密码解耦；向导完成后立即失效
- **本地服务自动检测**（C-4/C-5）：`/api/setup/detect` 探测 Ollama `:11434/api/tags`（返回已装模型列表，向导一键选用）与 OpenCode `/health`（已配置 hub-opencode URL → 4096 → 8081 顺序探测）
- **Provider 预设单一事实源**（C-11）：`internal/config/provider_presets.go` 统一 DeepSeek/OpenAI/Anthropic/SenseNova/Ollama/自定义；`GET /api/config/provider-presets` + `GET /api/setup/provider-presets`；向导与 SystemConfig 共用同一预设
- **LLM 模型即选即拉**（C-12）：`POST /api/config/discover-models` + `/api/setup/discover-models` 支持未保存 provider 实时探测 `/models` 或 `/api/tags`，返回可选模型下拉
- **站点级入站 Webhook 管理**（C-13）：新增 `server.public_url`；`internal/gitea/hooks.go` 实现 List/Create/Ensure 站点级 webhook；SystemConfig「入站 Webhook」Tab 支持检查/自动注册；向导完成时最佳努力自动注册
- **Hub 后端配置子页面**（C-14）：`agents.backends` 纳入 `ConfigManager` CRUD；`internal/config/backends.go` 提供解析/校验/脱敏/占位符还原；SystemConfig 新增「Hub 后端」Tab（默认后端、后端列表、增删改、builtin 保护）
- **配置变更审计**（C-16）：`config_update` / `config_delete` / `setup_complete` 落 `operation_logs`，token/secret/api_key/password 全脱敏
- **敏感字段掩码**（C-17）：`GET /api/config` 对 `gitea.admin_token` / `gitea.webhook_secret` / `llm.providers.*.api_key` / `agents.backends.*.auth.password` 返回 `********` 占位；PUT 中占位 = 保持原值（含 providers JSON 与 backends JSON 内 credential 还原）；测试连接端点对占位自动回退已存真值；前端表单掩码提示
- **组件健康面板**（C-18/C-19，P2）：`GET /api/health/summary` 并发实探 Gitea / LLM（`/models` 零费用）/ Hub 后端 / Deliver / DB / 磁盘六组件，四态语义（ok/degraded/unconfigured/disabled/error）；新增 `HealthStatus.vue` 状态灯面板与 Dashboard 集成
- **配置备份与恢复**（C-20，P2）：`GET /api/config/export` + `POST /api/config/import`；导出默认脱敏（`?include_secrets=1` 显式开关才含真实密钥，导出落审计 `config_export`）；导入校验 format 标识 + 键白名单，`UpdateBatch` 单快照预校验后原子应用；SystemConfig「备份与恢复」Tab（确认框 + 预检 + 密钥开关）
- **环境变量自动发现与一键吸入**（C-21，P2）：`GET /api/setup/env-detection`（只回名称/布尔，值不出进程）+ `POST /api/setup/apply-env`（9 个已知变量 catalog：Gitea/LLM key/Hub URL）；吸入响应附「快照固化进 DB」语义提示
- **磁盘空间预警**（C-22，P2）：新增 `internal/sysinfo` 跨平台磁盘检测（Windows `GetDiskFreeSpaceEx` quota 感知 / Unix `Statfs`）；启动时已用 ≥85% 打印 WARN；健康面板磁盘组件 + Dashboard 顶部横幅

### Changed
- **`gitea.webhook_secret` 不再必填**（C-6）：`CheckSetup` 移除该项；向导完成与 `PUT /api/config`（配置 Gitea 且无 secret 时）自动生成 32 字节 hex——空 secret 此前会静默关闭 webhook 签名校验
- **向导完成即生效**（C-8）：`POST /api/setup/complete` 服务端复测 Gitea（C-7）→ 批量写入 DB（llm.providers 合并保留既有 provider）→ `onConfigChange` 热重载 LLM Registry / Gitea Client / Dispatcher / webhook ingress → 审计 → 关闭 setup 面
- **Dashboard 引导卡**（C-10）：setup-aware，步骤高亮跟随真实初始化进度
- **精简 `config.example.yaml` 到 ~15 行**（C-15）：仅保留 server/database/可选 gitea/llm/auth；其余移入 Web UI 或自动检测
- **C-9 首次登录强制改密**经核对此前已完整实现（`MustChangePassword` + jwtWrap 403 + 前端守卫），向导成功页补充首登提示

### Fixed
- **用户入口文档漂移**：更新 `README.md` 与 `docs/AGENTS.md` 中 `workspace_mode`/`matea_path` 等 shared_path 时代残留描述，统一为 `git_sync` 事实；更正 `hub-hermes` 已可用
- **P2 评审修复批次**（2026-08-21，评审报告 `docs/archived/20260820-phase2.5-p2-review.md`）：
  - 健康面板此前读启动快照 `h.cfg`，热更新后失真——改为读 `cfgManager.Get()` 实时配置（R1）
  - `sysinfo.HumanBytes` 单位错位一档（5 GiB 显示为 "5 MiB"）且 <1KiB 恒为 "0 B"（R2）
  - 健康响应 `maskURL` 此前不脱 query，IM webhook 的 `?access_token=` 会泄漏（R3）
  - 向导 finish 此前无条件重新生成 webhook_secret，会静默覆盖 env 吸入的既有值——改为保留（R4）
  - 配置导出此前全明文且无审计——默认脱敏 + 显式开关 + 审计（R5）；`ConfigManager.Update` 运行日志此前明文记录密钥值，已脱敏（R6）
  - 配置导入/PUT 此前逐键非原子应用（map 随机顺序 + 中间态校验可致部分失败持久化）——`UpdateBatch` 原子化（R7）
  - 前端：配置导入零确认（现为预检 + 确认框）；Dashboard 与健康面板重复请求 `/health/summary`（现单请求 prop 下发）；健康探测并发化（串行最坏 ~27s → 并行 ~6s）（R8）

git_sync 写传输改造（branch `phase2.6/git-sync`，**已合入 master，PR #29**，三阶段计划 v3.1）——**审核报告 `docs/archived/20260820-phase2.6-review.md`：22 项勾选 21 项完全吻合、1 项文案偏差已修正**：

### Added
- **git_sync 信任模型**：Hub 持任务级凭据自 clone/commit/push 草稿分支 `matea/hub-{taskID}`，Matea 只 fetch + 校验 + 开 PR + 回收凭据；admin/agent token 永不离开 Matea（docs/HUB-BACKENDS.md）
- **任务级 deploy key 生命周期**：Prepare 签发一次性 ed25519 rw key（`write:repository` scope 即可，无需 site admin）→ 终态内联回收 → 10min 周期 sweep 兜底孤儿 key（审计 `git_sync_key_swept`）
- **Approve 四要素校验**：分支独占 / 起点锚定 / 必备 footer（`anchor..head` 每提交）/ diff 白名单（内置 deny `.env*`/密钥材料，deny 绝对优先，违规落 `operation_logs`）；base 漂移默认 fail + 告警，不自动 rebase
- **session 续作契约（git 原生）**：后续任务从 session `LastHead` 起新草稿分支（anchor 下发 + 校验锚点化 + handle 行持久化 anchor），滚动会话摘要注入 hub prompt

### Removed
- **`shared_path` workspace transport**（A5）：Matea 侧凭据会落到 hub 可见文件系统，信任模型过宽
- **`mcp` workspace transport 常量**（C1）：L2 全隔离档随 Phase 3.9 回归（依赖 MCP Server）

Phase 2 Hub 后端接入（branch `phase2/hub-ecosystem`，**已合入 master，PR #28**）：

### Added
- **Hub 后端抽象**：`HubBackend` 异步 Submit/Poll/Cancel 契约 + `init()` 注册制绕开包循环依赖；`hub-hermes`（官方 Runs API）与 `hub-opencode`（OpenCode sidecar HTTP）两类后端落地
- **大脑可插拔骨架（D10/D11）**：统一 `Harness` 接口 + `harnessRouter` 注册表；`ToolBox` 三层工具暴露策略（沙箱类 / Gitea 读侧 / 网关级 skill）；配置新增 `workspace_transport` 语义位（初值 `shared_path`；后于 phase2.6/git-sync 收敛为仅 `git_sync`，见上）
- **hub-hermes 接线（2.1.x）**：analyze / review / reply 经 Hermes 跑；repo/issue 级 `memories` 表实现跨任务记忆共享（D3）
- **hub-opencode 三刀（2.2.1–2.2.3）**：analyze（默认分支 shallow clone）/ review（clone PR head）/ reply（最小空 workspace，决策 B）经 OpenCode 跑；失败降级 single-shot LLM
- **deliver 出站扇出（2.3.3）**：`internal/deliver` 包把 hub 返回的 `DeliverRequest` 以 JSON POST 到 `deliver.webhook_url`（仅出站；5xx/网络错误按 `max_retries` 退避重试，4xx 不重试；空 URL = no-op）

### Fixed
- **P0 可靠性地基（2.1.1-a / 2.1.1-b）**：Hub Handle 持久化 + Executor 重启拾取 + IdempotencyKey 幂等去重。`hub_handles` 表落库 Handle（`backend`/`remote_id`/`idempotency_key`/`status`）；`runViaHub` 提交前命中非终结 Handle 即复用（防重复 `Submit`），提交后 `SaveHubHandle` 落库；`dispatcher.Start` 经 `FailOrphanedRunningTasksExceptHub` + `Executor.ReattachHubHandles` 在重启后重建轮询；stale scanner 排除 hub 任务；OpenCode `Poll` 缓存未命中时从仍存活的 sidecar 恢复结果。新增单测覆盖 store / `runViaHub` / reattach 路径。

### Known gaps (intentionally deferred, tracked in docs/TASKS.md)
- `Harness` / `ToolBox` 已建但未接入执行路径（零生产调用），收敛留待 D12 / Phase 3

## [0.11.4] - 2026-07-31

WebUI 任务页交互补丁：任务详情与 Agent 对话日志拆成独立对话框。  
推送本 tag 后由 [`.github/workflows/release.yml`](.github/workflows/release.yml) 生成 **draft** Release；维护者在 GitHub 上核对后 Publish。

### Changed
- **任务详情 / 对话分拆**（#25）：独立 `TaskConversation` 对话框承载多轮 Agent 日志（竞态安全加载、错误重试）；详情对话框专注元数据与 usage

## [0.11.3] - 2026-07-27

可观测性 WebUI 与写路径 / 工作流加固补丁：对话日志与审计日志可在 Web 查看，并合入逻辑 Issue 归一、Agent 串行队列与若干实跑修复。  
推送本 tag 后由 [`.github/workflows/release.yml`](.github/workflows/release.yml) 生成 **draft** Release；维护者在 GitHub 上核对后 Publish。

### Added
- **Agent 对话日志 WebUI**（#23）：`GET /api/tasks/{id}/conversation`；任务详情 /「对话」入口按 iteration 展示 role / content / tool_calls（需开启 `debug.conversation_log.enabled`）
- **操作审计日志 WebUI**（#24）：`/logs` 页 + 侧边栏入口；消费 `GET /api/logs`；Agent 名、任务深链 `/tasks?task=`、多行 detail 展开、分页空页回退、窗口内关键字搜索
- **逻辑 Issue 归一**（#22）：PR 评论/事件用 `Fixes/Closes #N` 作为 session/workflow 键；纯 PR 经 `effectiveIssueKey(pr_id)` 避免 `issue_id=0` 碰撞
- **Agent 并发**（#22）：`dispatcher.agent_concurrency: parallel | serial_queue`（默认 parallel）；串行时同 agent 排队等待、不硬拒绝
- **PR 续作注入 review 历史**（#20/#21）：`solve_comment` 拉取近期 PR/Issue 评论，优先 review-role

### Fixed
- **SenseNova + DeepSeek 模型门禁误杀**：`sensenova` 内置目录补齐 `deepseek-v4-flash` / `deepseek-v4-pro`；稀疏元数据按 model ID 补全 `supports_tools`（#17）
- **自发现未知模型误杀**：`/models` 仅返回 ID 且不在 builtin 时，不再把零值 `supports_tools=false` 当明确不支持（#17）
- **工作流重置**：重置时取消 in-flight 任务并释放 lock；系统默认不再误强制 `verify_commands` override（#18）
- **写路径 / Agent 摩擦**（#19）：干净树先 push 再 CreatePR；失败闭环；shell 别名改写；Git identity；bootstrap `logging.path` 落盘等

## [0.11.2] - 2026-07-24

Coder 任务失败闭环补丁：拒绝「伪工具调用当成功」、拦截不支持 tools 的模型，并在 Web 选型时提醒。  
推送本 tag 后由 [`.github/workflows/release.yml`](.github/workflows/release.yml) 生成 **draft** Release；维护者在 GitHub 上核对后 Publish。

### Fixed
- **伪 tool-call 正文**：AgentLoop 在 `tool_calls` 为空但 content 像 DSML/`<tool_call>` 等标记时直接失败，避免把未执行的工具调用贴成成功评论
- **无改动收尾**：工作区干净且 summary 像伪工具调用时，finalize 返回错误而非成功评论
- **模型能力门禁**：internal coding backend 在模型元数据 `supports_tools=false` 时拒绝 coder 任务（`meta=nil` 的未登记模型不误杀）
- **表单 tip 换行**：flex 表单项下 `.form-tip` / 警告单独成行，避免挤在输入框右侧（含 Gitea Token / Webhook 说明文案）

### Added
- **启发式检测** `LooksLikePseudoToolCall`：文档化已知覆盖与明确不识别的格式（裸 JSON 等）
- **Web 提醒**：Agent 创建/编辑在 `role=coder` 且所选模型 `supports_tools=false` 时显示黄色提示（不阻断保存）

## [0.11.1] - 2026-07-24

Web UI 表单布局与登录体验补丁。  
推送本 tag 后由 [`.github/workflows/release.yml`](.github/workflows/release.yml) 生成 **draft** Release；维护者在 GitHub 上核对后 Publish。

### Fixed
- **表单对齐**：登录 / 改密页补全 `label-width`；Agent 编辑与系统配置页孤儿操作按钮用空 label 占位，与输入框左缘对齐
- **提示文案**：缩短「最大输出 Tokens」标签；统一 `.form-tip` 间距与行高

### Changed
- **回车提交**：登录页与修改密码页按钮改为 `native-type="submit"`，输入框按 Enter 即可提交，并加 loading 防重复提交

## [0.11.0] - 2026-07-23

Matea 品牌首发：项目更名、Bootstrap 自配置、Release 恢复单二进制。  
推送本 tag 后由 [`.github/workflows/release.yml`](.github/workflows/release.yml) 生成 **draft** Release；维护者在 GitHub 上核对后 Publish。

### Added
- **Bootstrap 自生成配置**：无 `config.yaml` 时首次启动自动写入最小 bootstrap（随机 `jwt_secret`），可直接打开 Web UI
- **Setup 引导**：`GET /api/setup/status` + `/health.setup_required`；Web 顶栏在 Gitea/LLM 未配齐时引导至系统配置
- **首次登录强制改密**：默认 admin 带 `must_change_password`；仍使用默认密码时也会强制修改

### Changed
- **Release 主推单二进制**：恢复上传平台裸二进制 + `checksums.txt`（不再依赖 zip 预置 yaml）
- 部署 / README 快速开始改为：下载二进制 → 直接运行 → 浏览器配置（自动 bootstrap）
- **项目更名 `gitea-agent-gateway` → Matea**：模块路径 `github.com/jeeinn/matea`、二进制 `matea`/`matea.exe`、展示名 `Matea`、仓库 `github.com/jeeinn/matea`
  - **硬切（不兼容旧版本）**：评论命令仅识别 `/matea reset`（旧 `/gateway reset` 失效）；Agent 评论标记仅识别 `<!-- matea-agent -->`（旧 `<!-- gateway-agent -->` 失效）
  - 默认数据库 `./data/gateway.db` → `./data/matea.db`、默认日志 `gateway.log` → `matea.log`；升级时请手动复制旧库（见 `docs/DEPLOYMENT.md` 迁移段）
  - 任务失败原因文案 `gateway restarted; …` → `matea restarted; …`；OpenCode session 标题 `gateway-task-%d` → `matea-task-%d`
  - `workspace_mode` 枚举 `gateway_path` → `matea_path`（不兼容旧配置）

## [0.10.2] - 2026-07-23

### Changed
- **Release 产物**：由裸二进制改为按平台 **zip 部署包**（含 `gateway`/`gateway.exe`、`config.example.yaml`、`.env.example`、`README.txt`）；`checksums.txt` 对 zip 做 SHA256

## [0.10.1] - 2026-07-23

补丁发布：自动化 Release、配置示例分层，以及开源后若干能力加固。  
推送本 tag 后由 [`.github/workflows/release.yml`](.github/workflows/release.yml) 生成 **draft** Release；维护者在 GitHub 上核对后 Publish。

### Added
- **Release workflow**：推送 `v*` tag 时自动交叉编译五平台二进制 + `checksums.txt`，并创建 GitHub Release **draft**
- **配置双示例**：`config.example.yaml`（精简可跑）+ `config.full-example.yaml`（完整参考）；`workspace.base_dir` 默认 `./data/work`
- **Harness**：`no_progress_limit` / `verify_commands`；独立 Checker（Review 独立 Prompt、Coder `independent_checker`、L2 `review_not_same_coder`）
- **LLM 采样参数透传**：`top_p` / `frequency_penalty` / `presence_penalty`（`default_params` → ChatRequest）
- **沙箱**：`rg` 工具（未安装回退 `search_code`）；temp 与 Session workspace `Persistent` 生命周期对齐
- **架构 P1 硬化**：Registry 锁、Config 深拷贝、Webhook inbox 先落库再 200、Provider 按 `type` 选适配器等
- **工程拆分**：api / agents / dispatcher / config 大文件按职责拆分

### Changed
- 文档：已完成 TASKS / 开源清单 / E2E 签核等迁入 `docs/archived/`；现行 `docs/` 仅保留架构、部署、TASKS backlog、v4 设计与 LLM 可选增强

## [0.10.0] - 2026-07-17

首个**公开开源**发布候选（仓库已有 `v0.2`–`v0.7.0` 历史 tag，故从 **0.10.0** 起跳）。  
以**预编译二进制 + systemd**部署为主；容器示例暂未提供。  
发布步骤：[docs/archived/20260717-RELEASE-v0.10.0.md](docs/archived/20260717-RELEASE-v0.10.0.md) · 仓库：https://github.com/jeeinn/ai-dev

### Added (开源质量加固)
- **E13 E2E**：Merge open PR → workflow `stage=done`（S1；见 docs/archived/20260717-v2-merge-signoff.md）
- **loop_config 校验**：`max_iterations` 1–100、`total_timeout` 1m–1h
- **Workspace / Sandbox base_dir 对齐**：历史默认 `./workspace` 继承 `workspace.base_dir`
- **Linux**：`scripts/linux/e2e-smoke.sh`（Mock 冒烟；完整 E2E 以 Windows PS1 / pwsh 为主）

### Added
- **Agent 对话持久化（调试）**: 新增 `task_conversation_logs` 表；系统配置「调试」页可开启 `debug.conversation_log.enabled`，将 Agent Loop 每轮 LLM 消息与 tool call 写入 SQLite（默认关闭）
- **Dev/Bugfix 工具使用指引**: `BuildSolveToolPrompt()` 明确要求使用 `write_file`/`apply_diff` 实现变更、`run_command` 跑测试，并说明 Gateway 会自动 commit/push/PR

### Fixed
- **review_pr 失败不回写**: `review_pr` 任务在 `IssueID=0` 但 `PRID>0` 时，成功/失败评论写入 PR，不再因缺少 Issue ID 跳过
- **DevRunner 复用分支未开 PR**: 推送至已存在的 session/本地分支后，先查询 Gitea 是否存在 head 匹配的 open PR；若无则自动 CreatePR，避免仅回写 comment 而用户需手动开 PR
- **review_requested 无响应**: 解析 Gitea 顶层 `requested_reviewer` 字段并归一化到 `PR.RequestedReviewers`；通过 WebUI 创建 Agent 后立即刷新内存 Registry，无需重启 Gateway
- **DevRunner 忽略 WebUI system_prompt**: `runWriteTask` 在 `BuildDevPrompt`/`BuildBugfixPrompt` 基础上通过 `MergeAgentSystemPrompt` 合并 Agent 自定义指令（`## Agent-specific instructions` 段落）
- **Dev 任务 Git 操作**: session 复用 workspace 时 checkout 使用仓库 `default_branch` 而非硬编码 `main`；clone 使用 Agent Token 认证 URL；`git fetch`/`pull` 失败时立即终止任务并回写失败评论
- **Session 复用 fetch 失败**: 移除 `git remote set-branches --add` 对 `.git/config` 的污染；本地-only 分支跳过远程 fetch，改用一次性 refspec fetch；session 复用前重置 branch-specific fetch refspec；创建分支时立即持久化 `session.Branch`
- **沙箱工具跨平台**: `list_files` / `tree` / `search_code` 在 Windows 走 PowerShell，在 Unix 走 `find`/`grep`；`run_command` 在 Windows 用 `cmd /C`，Unix 用 `sh -c`；修复 Windows 上 Agent Loop 因无 `find`/`sh` 空转耗尽迭代的问题
- **任务卡住**: Ctrl+C 后 `running` 任务残留导致「已有任务正在处理」；启动时自动将孤儿 `running` 标为 failed；任务列表增加「重置」操作（`POST /api/tasks/{id}/reset`）

### Changed (Agent LLM 预算与超时统一)
- **Token**：`max_tokens` → `max_output_tokens`；新增 `max_input_tokens`；删除 `loop_config.max_tokens` 与 `llm.defaults.max_tokens`
- **默认 `max_input_tokens`**：`8192` → `65536`（缓解 tool-use 多轮后上下文被截断导致重复读文件）
- **重试拆分**：`dispatcher.retry_count` → `dispatcher.task_retry_count`（整任务）+ `llm.rate_limit_retries`（仅 429）；启动时自动迁移旧 key
- **优雅退出**：Ctrl+C / SIGTERM 时取消 in-flight Agent Loop / LLM 请求，避免任务长期卡在 `running`
- **超时**：删除 `dispatcher.timeout` 与 `loop.timeout`；单次任务用 `agents.defaults.timeout` / `agent.timeout`；Loop 仅用 `total_timeout`
- **Temperature**：迁至 `agents.defaults.temperature`（LLM Tab 不再配置）
- **截断**：发请求前按 `max_input_tokens` 截断 messages（含 tools JSON）；估算为字符数/4
- **迁移**：启动时回填 `max_output_tokens = max(旧 max_tokens, loop.max_tokens)`，并清理 system_config 旧 key

### Added (Assign Workflow v2 — Phase 16)
- **Agent role 字段**: `analyze` | `coder` | `review`，决定触发后的任务类型
- **Event Resolver** (`internal/workflow/resolver.go`): 替代 Router.Match + determineTaskType
  - `issues.assigned`: 通过 payload 中单个 `assignee` 查找 Registry Agent → 按 role 映射 task_type
  - `pull_request` + `review_requested`: 在 reviewers 中查找 review 角色 Agent
  - `issues.labeled` / `unassigned`: 忽略（v2 不再使用 Label 触发）
- **WorkflowContext 状态机** (`internal/workflow/context.go`):
  - 阶段: `idle → analyzing → analyzed → developing → reviewing → done`
  - Task 完成回调: analyze→analyzed, solve→developing(写入 PR ID)
- **L1 结构性门禁** (`internal/workflow/gate_l1.go`):
  - `l1.review_requires_pr`: review Agent 需要有 open PR
  - `l1.review_on_closed_pr`: PR 已关闭 → hard 拒绝
- **Dispatcher v2 流水线**: sender 过滤 → Resolver → L1 门禁 → WorkflowContext → in-flight 锁 → 入队
- **新数据表**: `workflow_contexts`, `agent_sessions`
- **tasks 表扩展**: `session_id`, `role` 字段
- **18 个 store 单元测试** + **16 个 resolver 测试** + **8 个集成测试**

### Breaking Changes (v2)
- **Label 触发已移除**: `issues.labeled` / `pull_request.labeled` 事件不再触发任务
- **Router.Label 匹配已移除**: `determineTaskType()` 中 `ai:solve` / `ai:fix` 等 Label 分支已删除
- **迁移**: 使用 Label (`ai:analyze`, `ai:solve`) 触发的用户需改为 Assign Agent

### Planned
- Phase 14: 沙箱增强（详见 docs/archived/20260604-sandbox-roadmap.md）
- Phase 17: Session 续作 + WorkflowPolicy L2/L3

## [0.7.0] - 2026-06-05

### Added
- 系统配置页面 (SystemConfig.vue)
  - 标签页布局: Gitea 连接 / LLM 配置 / 任务调度 / Agent 默认参数 / Prompt 模板
  - ConfigManager: DB 配置 > 文件配置 > 默认值
  - GET/PUT/DELETE /api/config 端点（含 key 校验）
  - LLM Registry 热更新
  - Prompt 模板管理（查看/新增/删除自定义模板，DB 持久化）
  - 配置项说明 tips（MaxTokens/Temperature 含义区分）
- Agent 详情页 (AgentDetail.vue)
  - 基本信息编辑 + 模板变量说明
  - 触发规则管理（Route CRUD + 快捷配置 + 预计执行行为）
  - Prompt 版本历史（详情查看 + 回滚 + 删除）
- Agent 创建增强
  - 表单分组折叠（核心字段直接展示，高级配置折叠）
  - 模板选择下拉框（从 /api/prompt-templates 动态加载）
  - Provider 下拉从配置动态读取
  - 创建表单从 agents.defaults 读取默认值
- 触发规则增强
  - 预计执行行为列（根据 event+action+label 自动推断，图标+中文描述）
  - 防重复规则（CreateRoute 唯一性检查）
  - 优先级说明（值越大越优先）
- 任务列表增强
  - 服务端分页（limit/offset + total）
  - 筛选：状态 / 任务类型 / Agent
  - Agent 名称显示（非 ID）
- Dashboard 优化
  - 新用户引导卡片（无 Agent 时显示，三步跳转）
  - 最近任务 / Agent 列表限 10 条 + 查看全部链接
- 用户管理 API
  - GET/POST/PUT/DELETE /api/users（JWT 认证）
- 配置值生效链路
  - RunnerFactory 持有 defaultMaxTokens / defaultTemp
  - runners 所有 LLM 调用使用 resolveMaxTokens / resolveTemperature
  - Agent.MaxTokens 为 0 时回退到 agents.defaults.max_tokens
- 共享组件
  - TemplateHelp.vue: 模板变量说明弹窗（三处复用）
- 文档
  - ARCHITECTURE.md 校正 + mermaid 图
  - README.md 重写
  - DEPLOYMENT.md 部署指南
  - 端到端测试报告

### Changed
- Prompt 管理拆分: 内置模板→系统配置，自定义版本→Agent 详情页
- 删除独立 Prompts.vue 页面及菜单
- 所有弹窗禁用点击外部关闭（close-on-click-modal=false）
- Menu 顺序调整: 仪表盘、任务列表、Agent 管理、用户管理、系统配置

### Fixed
- 用户管理页面返回 HTML（添加 /api/users 端点）
- 内置模板为空（/api/prompt-templates 返回内置 + 自定义模板）
- Prompt 版本记录（Agent 编辑时自动创建 prompt_history）
- AgentDetail 页面空白（form 初始化 + 错误处理）
- 启动时 prompt.templates 警告（配置校验 + 详细 WARN 提示）
- Dashboard /api/tasks 返回格式适配
- SQLite 迁移顺序（ALTER TABLE 在 CREATE TABLE 之后）
- Agent 列表模板加载改用 /api/prompt-templates

## [0.6.0] - 2026-06-03

### Added
- Web UI (Vue 3 + Element Plus)
  - Login.vue: 登录页面
  - Dashboard.vue: 仪表盘 (统计/最近任务)
  - Agents.vue: Agent 管理 (CRUD)
  - Tasks.vue: 任务列表 (详情查看)
  - Prompts.vue: Prompt 管理 (版本/回滚)
  - Users.vue: 用户管理 (admin)
- 认证系统
  - store/user.go: users 表 + CRUD
  - auth/jwt.go: JWT 认证
  - auth/password.go: bcrypt 密码哈希
  - api/auth_handler.go: 登录/登出 API
- 前端构建
  - Vue 3 + Element Plus + Vite
  - Pinia 状态管理
  - Vue Router 路由守卫
  - Axios API 客户端 (JWT 拦截器)
- 打包部署
  - go:embed 嵌入前端资源
  - SPA 路由支持

## [0.5.0] - 2026-06-03

### Added
- Prompt 历史版本管理 (store/prompt.go)
  - CreatePromptVersion: 创建新版本
  - GetPromptVersion: 获取指定版本
  - GetActivePrompt: 获取活跃版本
  - ListPromptVersions: 列出所有版本
  - ActivatePromptVersion: 激活指定版本 (回滚)
  - DeletePromptVersion: 删除版本
- Prompt 加载管理 (agents/prompt.go)
  - 优先级: DB > Agent > Config > Built-in
  - 6 个内置模板: default, analyze_issue, review_pr, reply_comment, solve_issue, fix_bug
- Prompt API 端点
  - GET /api/agents/{id}/prompts: 列出版本
  - POST /api/agents/{id}/prompts: 创建版本
  - GET /api/agents/{id}/prompts/active: 获取活跃版本
  - POST /api/prompts/{id}/activate: 激活版本 (回滚)
  - DELETE /api/prompts/{id}: 删除版本
- 数据库迁移: prompt_history 表添加 is_active 和 note 字段

## [0.4.0] - 2026-06-02

### Added
- SandboxConfig 结构定义
  - Mode: 工作目录模式 (temp | fixed)
  - CommandTimeout: 单命令超时
  - TaskTimeout: 总任务超时
  - MaxOutput: 最大输出字节数
  - MaxFileSize: 最大文件大小
  - CleanupAfter: 失败任务保留时间
- 临时目录模式 (ModeTemp)
  - os.MkdirTemp 自动创建临时目录
  - CleanupWithDelay 延迟清理
- 更丰富的上下文工具
  - tree: 目录结构展示
  - git_log: Git 提交历史
  - git_blame: 文件修改历史
- AgentLoopConfig 结构定义
  - MaxIterations: 最大迭代轮次
  - MaxTokens: 单次 LLM 调用最大 tokens
  - Timeout: 单轮超时
  - TotalTimeout: 总超时
- NewAgentLoopWithConfig: 从配置创建 AgentLoop

### Changed
- Sandbox 使用 SandboxConfig 替代旧 Config
- 路径验证支持大小写不敏感比较 (Windows 兼容)

### Fixed
- 路径穿越攻击防护
- 文件大小限制验证

## [0.3.1] - 2026-06-02

### Added
- LLM Function Calling 支持 (Tool/ToolCall/Function 类型)
- `internal/agent` 包：Tool-Use Agent 实现
  - tools.go: Tool 定义与注册，6 个基础工具
    - `read_file`: 读取文件内容
    - `write_file`: 写入/创建文件
    - `list_files`: 列出目录结构
    - `search_code`: 搜索代码内容 (grep)
    - `run_command`: 执行命令 (受限)
    - `apply_diff`: 应用 Diff 补丁
  - loop.go: Agent Loop 多轮对话核心逻辑
  - context.go: 代码库上下文加载与 Prompt 构建
- Label 任务类型支持 (`ai:solve` → solve_issue, `ai:fix` → fix_bug)
- testify 集成测试框架
- 集成测试套件 (tests/integration/)
  - helpers_test.go: 测试辅助函数 (TestEnv, MockGitea, MockLLM)
  - webhook_test.go: Webhook 端到端测试
  - agent_test.go: Agent 生命周期测试

### Changed
- DevRunner / BugfixRunner 改用 Agent Loop (Tool-Use 模式)
- RunnerFactory 增加 db 参数支持
- Executor.SetGiteaClientFactory 传递 db 给 RunnerFactory

### Fixed
- DevRunner/BugfixRunner 的 DB 注入问题 (AuditLogger nil panic)
- 推理模型支持 (reasoning_content 字段)

## [0.3.0] - 2026-06-02

### Added
- 轻量级沙箱 (`internal/sandbox/`)
  - sandbox.go: 工作目录隔离 + 命令白名单 + 超时控制 + 输出限制
  - git.go: Git 操作封装 (clone/branch/commit/push + 分支限制)
  - audit.go: 命令审计日志
- DevRunner / BugfixRunner 基础版
- 命令白名单: git, sh, bash, go, python, node, npm, make 等
- 分支名验证: ValidateBranchName + GenerateBranchName

## [0.2.0] - 2026-06-01

### Added
- Gitea API 扩展
  - PRDiff: 获取 PR diff 内容
  - PRFiles: 获取 PR 变更文件列表
  - IssueComments: 获取评论历史
- Runner 接口和实现
  - AnalyzeRunner: Issue 分析
  - ReviewRunner: PR 审查
  - InteractionRunner: @Mention 回复
  - RunnerFactory: 根据 task_type 选择 Runner
- 队列可靠性增强
  - pending task 后台扫描 (每 60 秒)
  - stale running task 恢复 (超过 10 分钟)
- API 认证 (Bearer Token 中间件)
- 配置化模板 (agents.templates)
  - Go template 渲染引擎
  - 预置 analyze/review/reply 三种模板
- API 响应隐藏 gitea_token (AgentDTO)

### Changed
- Dispatcher 使用 RunnerFactory 选择 Runner
- webhook.EventCallback 返回 bool (支持失败重试)

### Fixed
- Webhook 去重时机 (任务成功入队后才标记)

## [0.1.0] - 2026-06-01

### Added
- 项目骨架 (Go 1.26.3)
- Webhook 接收
  - HTTP Handler (签名验证 + 去重 + 异步回调)
  - HMAC-SHA256 签名验证
  - X-Gitea-Delivery 幂等去重
  - 事件解析 (issues/PR/comment)
- Gitea API 客户端
  - Admin API (创建用户 + 生成 Token)
  - Issue 操作 (评论 + 标签)
  - PR 操作 (创建 + 评论)
  - 仓库操作 (信息 + 文件内容)
- Agent 管理
  - Agent CRUD
  - 路由规则 CRUD
  - Agent 创建 (含 Gitea 账号自动注册)
- LLM 调用层
  - Provider 接口
  - OpenAI 兼容 Provider
  - Anthropic Provider
  - Provider 注册表
- Dispatcher
  - Router (Label+Assignee 双条件路由)
  - TaskQueue (SQLite + 内存队列)
  - Executor (并发控制 + 超时 + 重试)
- 管理 API
  - Agent CRUD 接口
  - 任务查询接口
  - 路由规则接口
  - 统计数据
  - 操作日志
- SQLite 存储 (WAL 模式)
- YAML 配置 (环境变量展开)

[Unreleased]: https://github.com/jeeinn/matea/compare/v0.12.1...HEAD
[0.12.1]: https://github.com/jeeinn/matea/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/jeeinn/matea/compare/v0.11.4...v0.12.0
[0.11.4]: https://github.com/jeeinn/matea/compare/v0.11.3...v0.11.4
[0.11.3]: https://github.com/jeeinn/matea/compare/v0.11.2...v0.11.3
[0.11.2]: https://github.com/jeeinn/matea/compare/v0.11.1...v0.11.2
[0.11.1]: https://github.com/jeeinn/matea/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/jeeinn/matea/compare/v0.10.2...v0.11.0
[0.10.2]: https://github.com/jeeinn/matea/compare/v0.10.1...v0.10.2
[0.10.1]: https://github.com/jeeinn/matea/compare/v0.10.0...v0.10.1
[0.10.0]: https://github.com/jeeinn/matea/compare/v0.7.0...v0.10.0
[0.7.0]: https://github.com/jeeinn/matea/compare/v0.3.1...v0.7.0
[0.3.1]: https://github.com/jeeinn/matea/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/jeeinn/matea/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/jeeinn/matea/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/jeeinn/matea/releases/tag/v0.1.0
