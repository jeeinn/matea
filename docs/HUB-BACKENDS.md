# HUB-BACKENDS — Hub 后端接入契约与 git_sync 信任模型

> 面向把自有 agent 大脑（Hermes、OpenCode 或自研服务）接入 Matea 的运维/集成开发者。
> 当前唯一 workspace transport：**`git_sync`**（`shared_path` 已于阶段 A5 删除；`mcp` 常量已于 C1 删除，Phase 3.9 回归）。
> 实现入口：[internal/agents/workspace_transport.go](../internal/agents/workspace_transport.go)、[internal/agents/hub_run.go](../internal/agents/hub_run.go)。

---

## 一、信任模型一句话

> **Hub 持任务级凭据自 push 草稿分支；Matea 只 fetch + 校验 + 开 PR。Matea 的 admin/agent token 永不离开 Matea 进程。**

```
Gitea webhook → Matea Router/Executor
   │ Prepare: 生成一次性 ed25519 密钥对 → Gitea 注册 repo 级 rw deploy key
   │          （标题 matea-hub-task-{taskID}）
   ▼
Submit(TaskContext{ GitSyncInfo{ ssh clone_url, 私钥(base64), draft 分支契约 } })
   ▼
Hub 沙箱内: clone → checkout -b matea/hub-{taskID} [anchor] → 改代码
           → commit（必须带 footer matea-task-id: {taskID}）→ push
   ▼
Matea Approve: fetch（用自己的凭据，绝不用 hub 的 key）
   → 四要素校验 → 开/更新 PR → Cleanup: DELETE deploy key
```

**为什么是这个形状**：Hub 大脑是不可信执行环境（prompt 注入、模型幻觉、远端多租户都可能让它越权）。
把写权限交给 Matea 侧凭据意味着任何失控的 Hub 都能以 agent 身份直推受保护分支；任务级 deploy key
把爆炸半径收敛到「单 repo、单任务窗口、可吊销、可审计」，并配合应用层校验把越权产物挡在 PR 之外。

---

## 二、四要素校验（Approve 必须全过，否则不开 PR）

| # | 要素 | 失败即拒的行为 |
|---|---|---|
| 1 | **分支独占** | 只认契约分支 `matea/hub-{taskID}`；hub 自择分支 = fetch 不到 → 拒绝 |
| 2 | **起点锚定** | draft head 必须含 anchor 祖先提交（续作任务 anchor=session LastHead，否则=Prepare 时记录的 BaseHEAD）；且 `anchor..head` 必须有新提交 |
| 3 | **必备 footer** | `anchor..head` 范围内**每一个**提交都必须带 `matea-task-id: {taskID}` trailer |
| 4 | **diff 白名单** | `anchor..head` 的 changed paths 不得命中内置 deny（`.env*`、`*.{pem,key,p12,pfx}`、`id_rsa*`、`id_ed25519*`、契约用 `key` 文件）或 backend 级 `denied_paths`；配置了 `allowed_paths` 时须全部命中 |

另有**base 漂移检查**（独立于四要素）：Prepare→Approve 窗口内 BaseHEAD 被推进 ⇒ 默认 **fail + 告警，绝不自动 rebase**（window 只看本任务，续作不受 main 前进影响）。

任一失败路径都会：① 不开 PR；② `hub_handles` 标 **Failed**（不是 Done——不会重接）；③ deploy key 当场回收；④ diff 违规额外落 `operation_logs`（action=`git_sync_diff_violation`）；⑤ 任务标记 failed（经 executor 既有路径）。

---

## 三、凭据最小权限（运维侧模板）

| 凭据 | 谁持有 | 要求 | 说明 |
|---|---|---|---|
| Matea `admin_token` | **仅 Matea** | Gitea token，scope **`write:repository` 即可**（A0.2 spike 实证无需 site admin） | 用于签发/回收 deploy key、fetch、开 PR |
| agent 个人 token | 仅 Matea | 读 issue / 写评论 | builtin runner 用；hub 任务不下发 |
| **任务级 deploy key** | Hub（一次一钥） | repo 级 **read-write**，标题 `matea-hub-task-{taskID}` | 私钥经 prompt base64 注入 hub，**不落库、不进日志**；任务终态立即 DELETE |

> Gitea deploy key 是 repo 级粒度（无 per-branch 限制）——「只能写 `matea/hub-*`」无法靠凭据层强制，
> 由第二节的四要素校验在应用层补偿。main 保护仍建议开启（Gitea branch protection）作为纵深。

**生命周期兜底**：即使 Matea 在「签发后、记录前」崩溃或 revoke 重试失败，后台 sweep
（10min 周期，`SweepOrphanedDeployKeys`）也会回收超 30min 宽限期且无非终态 handle 的孤儿 key，
并写审计 `git_sync_key_swept`。运维自建 key（非 `matea-hub-task-` 前缀）永不被触碰。

---

## 四、Hub 接入契约（Submit 侧）

Matea 在 `TaskContext.GitSync` 中下发（JSON 字段见 `GitSyncInfo`）：

| 字段 | 含义 |
|---|---|
| `clone_url` | SSH clone URL（hub 用它 + 私钥 clone） |
| `private_key` | 一次性 ed25519 私钥（OpenSSH PEM，base64 于指令中下发） |
| `draft_branch` | **必须**推送的分支名 `matea/hub-{taskID}` |
| `base_branch` / `base_head` | PR 目标分支 / Prepare 时的 base 锚点 SHA |
| `anchor_head` | 续作锚点（空=从 base tip 起分支；非空=`git checkout -b <draft> <anchor>`） |
| `required_footer` | 每个提交必须携带的 trailer `matea-task-id: {taskID}` |
| `hub_push` | true = Hub 自 push（git_sync 语义开关） |

**Hub 必须**（指令全文由 `BuildGitSyncInstructions` 渲染并置于 prompt 末尾近因窗口）：
1. 把私钥落盘为 `key`（0600），用 `GIT_SSH_COMMAND="ssh -i key"` 操作 git；
2. `git clone <clone_url>` → `git checkout -b <draft_branch> [anchor_head]`（anchor 不可达时 **STOP 报错**，不得退回默认分支重开）；
3. 每个 commit 带 `-m "<footer>"`；
4. `git push -u origin <draft_branch>`；
5. 最终答复末尾输出 trailer `matea-draft-head: <40hex>`（Matea 会 fetch 权威值交叉核对，漏报不致命）。
6. **不得**把 `key`、`.env*`、私钥材料提交进草稿分支（diff 白名单默认拒绝并审计）。

**Hub 完成后**，Matea 以自身凭据 fetch、按第二节校验、开 PR、回收 key。Hub 不需要也不应调用 Gitea API。

---

## 五、当前后端与配置

| 后端 | type | 说明 |
|---|---|---|
| builtin | `builtin` | Matea 内置 agent loop（非 hub；git_sync 无关） |
| OpenCode sidecar | `hub-opencode` | HTTP session API；git_sync 契约经 prompt 注入（A4 落地） |
| Hermes | `hub-hermes` | 官方 Runs API（`POST /v1/runs` + 轮询）；同一契约字节级一致（B1 落地） |

```yaml
agents:
  backends:
    backends:
      opencode-local:
        type: hub-opencode
        base_url: "http://127.0.0.1:4096"
        workspace_transport: git_sync      # 唯一合法值；留空默认即 git_sync
        # denied_paths: ["deploy/*"]       # B3 可选：追加 diff deny
        # allowed_paths: ["src/*"]         # B3 可选：进一步收敛（deny 仍绝对优先）
      hermes-local:
        type: hub-hermes
        base_url: "http://127.0.0.1:9090"
        auth: { password: "${HERMES_RUNS_TOKEN}" }
        workspace_transport: git_sync
```

- 过期配置报错是**故意 fail loud**：`shared_path` → "removed in A5"；`mcp` → 不支持（C1 删除，Phase 3.9 回归）。
- 续作语义：同一 session 的后续任务自动从上次 `LastHead` 起新草稿分支（anchor 下发 + 校验锚点化，B2.3），并把滚动摘要注入 prompt（`SessionMemory`）。

---

## 六、失败语义速查

| Hub 行为 | Matea 反应 |
|---|---|
| 报 done 但没 push | fetch 失败 → 任务 failed，key 回收，handle=Failed |
| 推到自择分支 | 契约分支不存在 → 同上 |
| 提交缺 footer / 起点不在锚点上 / base 漂移 | 校验拒绝 → 同上 |
| 提交包含 deny 路径 | 校验拒绝 + `operation_logs` 审计 → 同上 |
| 报 failed / canceled | handle=Failed/Canceled，key 回收，任务 failed |
| 健康探针失败 | Prepare 之前快速失败（**不**回退 builtin——信任模型不可替换） |

> 注意：executor 的 `task_retry_count` 对 git_sync 校验失败属于整任务重试——重试会**重新 Prepare 签发新 key、重新 Submit**，hub 有机会产出合规草稿；校验拒绝本身不可在 Matea 侧修复。

---

## 七、性能预算（C4）

**SLO**：`Approve` 的 Matea 侧耗时（fetchDraft + 四要素校验 + 开 PR，**不含** hub 执行时间与网络 RTT）在 5k-commit 规模的仓库上应 **≤ 2s**；日常规模（≤1k commits）应 **≤ 1.5s**。

**实测基线**（2026-08-19，Windows 11，file:// 本地远端，`BenchmarkGitSyncApprove`，含进程冷启动）：

| 规模 | 延迟 |
|---|---|
| 50 base commits / 1 draft commit | ~1.6s |
| 1,000 base / 10 draft（200 变更路径） | ~1.4s |
| 5,000 base / 50 draft（500 变更路径） | ~1.8s |

延迟大头是固定开销（`git init` + 两次 fetch 的进程与协议启动成本），历史长度在本地几乎不敏感；
跨网络时 fetch 传输成为主项。压测用例：`internal/agents/gitsync_perf_test.go`（CI 内回归：
300-commit 仓库 + 150 路径草稿校验证据完整；benchmark 需 `-bench` 显式运行）。

**超预算时的既定缓解**（当前未启用，超 SLO 再评估）：fetch 改 `--filter=blob:none` 部分克隆——
merge-base / `log --format=%B` / `diff --name-only` 只需 commit+tree 对象，blob 按需拉取，
可把大仓 Approve 的传输量降一个数量级。
