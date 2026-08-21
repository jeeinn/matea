# 本机 Gitea 全覆盖 E2E 测试报告

> **状态：已归档（2026-07-23）**  
> 本机 E2E 点时报告（13/13）；非现行运维文档  
> 现行文档入口：[../TASKS.md](../TASKS.md) · [../ARCHITECTURE.md](../ARCHITECTURE.md) · [../DEPLOYMENT.md](../DEPLOYMENT.md)

---
> 日期：2026-07-16  
> 仓库：`x:\ai-dev`（commit `e92f72a`）  
> 驱动：Assign Webhook → Gateway → SenseNova / OpenCode / Mock MCP  
> 结果汇总：`data/e2e-results.json`（gitignore）

## 环境

| 组件 | 版本 / 配置 |
|------|-------------|
| OS | Windows 10 / PowerShell |
| Go | 1.26.3 windows/amd64 |
| Gitea | 1.26.2 @ `http://localhost:3000`（`x:\gitea`） |
| Gateway | 0.1.0 @ `:8080`，DB `./data/gateway-e2e.db`，workspace `./data/work-e2e` |
| OpenCode | 1.17.11 @ `:4096`（`opencode serve`） |
| Mock MCP | `scripts/common/e2e-mock-mcp.go` @ `:18080` |
| LLM（internal） | SenseNova OpenAI-compatible：`https://token.sensenova.cn/v1`，model `deepseek-v4-flash` |
| LLM（OpenCode） | Zen provider `opencode` / model `big-pickle`（见缺陷 #2） |
| 测试仓 | `http://localhost:3000/e2e/gateway-poc` |

凭据仅存于 `data/e2e-env.local`（`GITEA_ADMIN_TOKEN` / `SENSENOVA_API_KEY`），**未提交 git**。远程联调配置备份：`config.remote.yaml`。

## 场景结果

| ID | 对齐 TASKS | 结果 | 证据摘要 |
|----|------------|------|----------|
| E0 | 冒烟 | **PASS** | Gitea / Gateway / OpenCode / MCP 均 200 |
| E1 | P0.3 A0 directory | **PASS** | `POST /session?directory=` + `X-Opencode-Directory` 绑定绝对路径 |
| E2 | P1.5 Analyze 短 Loop | **PASS** | Issue [#1](http://localhost:3000/e2e/gateway-poc/issues/1) `analyze_issue` success；评论引用真实路径；无 PR |
| E3 | P2.9 Skills | **PASS** | 评论/日志含 `list_skills`、`hello`、`e2e-note`（全局 + 仓内 skill） |
| E4 | P2.8 MCP | **PASS** | 日志 `Tool result e2e_echo: e2e_echo:hello-e2e`；Issue [#2](http://localhost:3000/e2e/gateway-poc/issues/2) → [PR #3](http://localhost:3000/e2e/gateway-poc/pulls/3) |
| E5 | P0.2 internal 写路径 | **PASS** | Issue [#4](http://localhost:3000/e2e/gateway-poc/issues/4) → [PR #5](http://localhost:3000/e2e/gateway-poc/pulls/5) `E2E-INTERNAL-OK` |
| E6 | P0.3 Path A OpenCode | **PASS** | Issue [#26](http://localhost:3000/e2e/gateway-poc/issues/26) → [PR #27](http://localhost:3000/e2e/gateway-poc/pulls/27)；改码落在 Gateway workspace 绝对路径 |
| E7 | fix_bug | **PASS** | Issue [#28](http://localhost:3000/e2e/gateway-poc/issues/28) `fix_bug` → [PR #29](http://localhost:3000/e2e/gateway-poc/pulls/29) |
| E8 | Review 强制 internal | **PASS** | `review_pr` success（PR #5 / task #6），未走 OpenCode |
| E9 | P0.1 写回可靠性 | **PASS** | 破坏 agent Gitea token 后 task=`partial`，错误含 writeback 401；**非**纯 `success` |
| E10 | OpenCode health fail | **PASS** | 停 sidecar 后 task=`failed`，可读 health 错误；无静默降级 |
| E11 | P1.7 workflow-context | **PASS** | `GET /api/workflow-context` 返回 stage / active_role / pr_id |
| E12 | 回归 | **PASS** | `go test ./... -count=1` 全绿 |
| E13 | S1 Merge→done | **PASS** | PR #29（fix_bug issue #28）merge → `GET /api/workflow-context` `stage=done` |

**统计：14/14 PASS，0 FAIL，0 SKIP**

## 关键修复（本轮为跑通 E2E）

1. **脚本** `scripts/windows/e2e-run-scenarios.ps1`  
   - `-Only` 数组/CSV 解析（避免 `E0,E1` 被当成主机名）  
   - Tasks API 形状 `{data,total}` 正确解包  
   - `issue_id` / label id 用 `Normalize-Int`  
   - E3/E4/E9 证据与负面场景；E7 创建时带 `bug` label  
2. **配置** `config.yaml`：`llm.providers.sensenova` + `agents.defaults.provider=sensenova`  
3. **OpenCode WorkDir**（`internal/agents/opencode_http.go`）  
   - 传给 sidecar 前 `filepath.Abs`（相对路径曾被解析到 OpenCode 启动 cwd `x:\gitea\...` → 立即 Aborted）  
   - message POST 同步带 `?directory=` + `X-Opencode-Directory`  
4. **finalize 干净工作树仍建 PR**（`internal/agents/write_workspace.go`）  
   - OpenCode 已自行 commit/push 时，Gateway 仍对非 base 分支调用 `finalizeWriteTaskPR`

## 缺陷与备注

| # | 严重度 | 说明 |
|---|--------|------|
| 1 | 中 | OpenCode Zen 对 `deepseek-v4-flash` 返回 **CreditsError / No payment method**。E2E 将 OpenCode agents 临时改为 `big-pickle`（免费路径）。若要用 deepseek-v4-flash，需在 OpenCode workspace 配置账单，或改走本地/其它已鉴权 provider。 |
| 2 | 低 | E9 破坏 token 后，admin 代建 `users/{u}/tokens` 返回 401；已用 admin 重置密码 + Basic 自建 token 写回 DB。脚本 `finally` 恢复路径可再加固。 |
| 3 | 低 | `Find-PRForIssue` 在部分 PowerShell 展开下 detail 可能拼接多个 PR 号；workflow `pr_id` 仍正确。 |
| 4 | 信息 | SenseNova 偶发 429；internal 路径整体可完成。 |

## A0 / TASKS.md

- **E1 + E6 均 PASS**（directory 绑定 + OpenCode coder 端到端改码/PR）。  
- **已勾选** [docs/TASKS.md](20260716-TASKS.md) P0.3「A0 本机 `opencode serve` PoC 端到端验收」。

## 复现命令

```powershell
# 凭据
# data/e2e-env.local: GITEA_ADMIN_TOKEN / SENSENOVA_API_KEY / API_AUTH_TOKEN

# 依赖服务
# Gitea :3000 | opencode serve --port 4096 | go run scripts/common/e2e-mock-mcp.go
# gateway.exe -config config.yaml   # 需加载 e2e-env.local

powershell -NoProfile -File scripts/windows/e2e-setup-gitea.ps1
powershell -NoProfile -File scripts/windows/e2e-create-agents.ps1
powershell -NoProfile -File scripts/windows/e2e-run-scenarios.ps1
```

## 结论

本地 Gitea E2E 矩阵 **E0–E13 全部通过**。SenseNova 已替代原 DeepSeek 密钥路径完成 internal Analyze/Coder/Review；OpenCode Path A 在绝对 WorkDir + 可用 Zen 模型下验收通过；写回可靠性（partial）与 OpenCode health fail（failed）符合设计；E13 验证 Merge→done 状态机正确。TASKS A0 与 S1 均已按本报告勾选。

## E13 补充说明（2026-07-17）

> E13 在 v0.10.0 发布后补测。测试时 Gitea 队列 LevelDB 文件损坏，清理 `x:\gitea\data\queues\common\*` 后重启恢复。

| 项 | 值 |
|----|-----|
| 测试时间 | 2026-07-17 14:41 |
| 合并 PR | [#29](http://localhost:3000/e2e/gateway-poc/pulls/29)（fix_bug，关联 issue #28） |
| 验证方式 | `GET /api/workflow-context?repo=e2e/gateway-poc&issue=28` → `stage=done` |
| 轮询耗时 | 1 轮（≤5 秒） |
| 关联文档 | [20260717-v2-merge-signoff.md](20260717-v2-merge-signoff.md)（S1 Sign-off）
