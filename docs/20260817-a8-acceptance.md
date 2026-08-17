# A8 阶段 A 验收报告（2026-08-17）

> 关联：[20260815-git-sync-3phase-plan.md](20260815-git-sync-3phase-plan.md) 阶段 A 验收项 A8  
> 结论：**✅ 通过** — OpenCode 写任务经 git_sync 端到端出 PR；`go test ./...` 全量 17 包 PASS；shared_path 保留未动  
> 环境：Windows 11 + Docker Gitea 1.22.6（一次性容器）+ **真实 OpenCode 1.18.18**（`opencode serve`）+ 脚本化 mock LLM（[scripts/spike/mock_llm.py](../scripts/spike/mock_llm.py)，`a8-script.json` 剧本）+ 本仓库 `phase2.6/git-sync` 分支构建的 matea

---

## 验收项核对（TASKS.md A8）

| 验收标准 | 结果 | 证据 |
|---|---|---|
| 一条 OpenCode 写任务经 git_sync 端到端出 PR（OpenCode 自 push、Matea 开 PR） | ✅ | 任务 8 `success`：PR #4 `matea/hub-8 → main`；commit `ce22fa5c` 消息含 footer `matea-task-id: 8` |
| `go test ./...` 与 builtin 全量用例 PASS | ✅ | 17 包全 ok（A7 提交 cf2b67a 复跑） |
| shared_path 仍保留（A5 后置） | ✅ | `IsWorkspaceTransportValid` 共存窗口未收紧；builtin 路径零改动 |
| git_sync 路径可运行 | ✅ | 见下方逐环节证据 |

## 端到端逐环节证据（任务 8，全部真实组件）

| 环节 | 证据 |
|---|---|
| webhook → 路由 | 签名 HMAC 验签通过，`issues/assigned` → agent `a8-coder`（role=coder, backend=oc-a8）→ `solve_issue` 入队 |
| **Prepare** | `GET /repos/matea/demo` 取 ssh_url → `GET /branches/main` 锚定 BaseHEAD `60f01c68` → `POST /repos/matea/demo/keys` **201** 创建 rw deploy key（title `matea-hub-task-8`）→ `GitSyncInfo` 注入 TaskContext |
| **Hub 自 push（真实 OpenCode bash 工具）** | mock LLM 按 `a8-script.json` 下发 6 个 bash 调用，OpenCode 1.18.18 逐个执行：还原 base64 私钥（`ssh-keygen -y` 校验通过）→ `GIT_SSH_COMMAND` clone → `checkout -b matea/hub-8` → commit（footer `matea-task-id: 8`）→ **push 成功** → `rev-parse HEAD` |
| 结果回传 | 最终响应含 trailer `matea-draft-head: ce22fa5c…`，与远端分支头一致（诚实性交叉校验通过） |
| **Approve** | fetch 草稿分支 → 三要素校验（分支独占 ✅ / 起点锚定 ✅ / footer ✅）→ `FinalizeWriteTaskPR` 开 PR #4（head `matea/hub-8` base `main`） |
| 写回 | issue #1 收到「✅ PR 已创建」评论（agent token 路径） |
| **Cleanup** | 任务终态后 `DELETE /keys/{id}` → Gitea keys 列表为空；`hub_handles` 行持久化 `draft_branch/base_head/deploy_key_id`，status=done |
| 失败路径回收 | 任务 3（hub 未 push 的对抗场景）：Approve 拒绝「draft branch not found on remote」、**不开 PR**、key 仍 204 回收 |

## 关键发现：mock SSE 响应未终止 = 「OpenCode 卡死」的根因

A0 spike 记录的「同步 POST 在工具轮次间挂起」在本轮被根治：

- **现象**：OpenCode 执行完第一个工具调用后，永远不发起下一个 LLM 请求（1.18.4 与 1.18.18 均现）。
- **根因**：mock 的 SSE 响应带 `Connection: keep-alive` 且无 `Content-Length`（HTTP/1.0）。连接不被关闭 → 客户端永远等不到响应结束 → ai-sdk 的流 `finish` 回调不触发 → agent loop 不再推进。chunk 本身已被增量解析（所以工具照样执行），造成「流完好但 loop 卡死」的假象。
- **修复**：去掉 keep-alive 头，让连接在 handler 返回时关闭（close-delimited response）。修复后 **6 轮工具调用零人工干预顺序走完**（tool_results 0→1→4→6 约 50 秒）。
- **次生坑（已一并修复）**：python 子进程里裸 `bash` 会命中 WSL（`C:\Windows\System32\bash.exe`），DrvFS 上 `chmod` 无效 → OpenSSH 以 0777 拒收私钥；须用 Git bash 全路径。仅影响 mock 的本地代执行模式，不影响经 OpenCode 的真实路径。

## rig 说明（复现与限制）

- 复现：`scripts/spike/README.md`（Gitea docker 配方、opencode.json、matea 配置要点、webhook 构造）。
- mock 两种模式：`--mode tools`（默认，驱动 OpenCode bash 工具，**A8 验收所用**）；`--mode local`（mock 直接本地代执行，调试用）。
- 剧本用 `captures` 正则从 Matea 注入的 prompt 里提取动态值（base64 私钥、clone URL、草稿分支、footer），步骤命令模板化替换——因此每次任务的全新 deploy key 无需改剧本。
- 限制：LLM 是脚本替身（无真实推理）；真实 provider + 真实 OpenCode 的组合验证归 **B5**。任务 6 曾暴露一个 rig 配置坑（agent 行缺 `gitea_token` → 写回评论 401 → 任务 `partial`；API 的 updateAgent 不支持改 token，需创建时给或直改 DB）——与 git_sync 无关，但值得在配置自动化（Phase 2.5）中消化。
- 现场（容器/密钥/工作区）已按一次性原则清理；证据即本文。

## 阶段 A 收口状态

A0 ✅ → A1 ✅ → A2 ✅ → A3 ✅ → A4 ✅ → A6 ✅ → A7 ✅ → **A8 ✅**  
**A5（删除 shared_path）按计划在 B1 验收后执行，本轮不做。** 下一步：阶段 B（B1 Hermes 对齐同一 Hub 自 push 契约）。
