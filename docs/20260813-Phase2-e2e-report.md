# Phase 2 本地真实 E2E 验证报告

**日期**：2026-08-13
**分支**：`phase2/hub-ecosystem`（含 E2E 期间修复）
**性质**：真实服务端到端验证（非 mock 集成测试）——真实 Gitea 1.26.2 / 真实 OpenCode sidecar 1.18.4 / 真实 LLM（SenseNova + OpenCode Zen big-pickle）/ 假 Hermes（按官方 Runs API 契约实现的最小假服务）/ 自建 deliver 事件水槽
**前置评审**：[20260813-Phase2-progress-review.md](20260813-Phase2-progress-review.md)

---

## 一、环境

| 组件 | 版本/地址 | 说明 |
|---|---|---|
| Matea | 本分支 `go build` | `config.e2e.yaml`（127.0.0.1:8080，独立 db/workspace/log） |
| Gitea | 1.26.2 @ `localhost:3000` | 本机原生安装（`x:\gitea`），repo `e2e/gateway-poc`，webhook → matea |
| OpenCode | 1.18.4 @ `127.0.0.1:4096` | `opencode serve`，Zen provider 已连接，模型 `big-pickle` |
| 假 Hermes | `scripts/common/e2e-mock-hermes.go` @ 9090 | 实现 `POST /v1/runs` + `GET /v1/runs/{id}` + Bearer 鉴权；支持 hold/complete 控制完成时机；记录全部 submission |
| deliver 水槽 | `scripts/common/e2e-deliver-sink.go` @ 9095 | 接收 `/event`，供断言 |
| builtin LLM | SenseNova `deepseek-v4-flash` | OpenAI 兼容端点 |

E2E Agent 矩阵（经 API 创建，auto_provision 自动建 Gitea 账号 + collaborator）：
`e2e-oc-analyze` / `e2e-oc-review` / `e2e-oc-coder`（backend=`opencode-local`）、`e2e-hm-analyze` / `e2e-hm-review`（backend=`hermes-local`）、`e2e-bi-analyze`（backend=`builtin`）。

---

## 二、场景矩阵与结果（10/10 PASS）

| # | 场景（TASKS.md 条目） | 驱动方式 | 关键证据 | 结果 |
|---|---|---|---|---|
| S-A | hub-opencode analyze（2.2.1） | issue #35 assign `e2e-oc-analyze` | task#1 success；日志 `opencode session created`；AI 评论含真实 README 分析；`hub_handles` 行 `opencode-local/ses_…/done`；`memories.analysis_summary` 已写 | ✅ |
| S-B | hub-opencode review（2.2.2） | PR #36 请求 `e2e-oc-review` | task#2 success；clone PR head 后审查并回评；handle done；`review_summary` 记忆已写 | ✅ |
| S-C | hub-opencode 写任务（2.4.4 + E-2） | issue #37 assign `e2e-oc-coder` | task#3 solve_issue success；PR #38（`ai/dev/issue-37`）创建且含目标文件 `PHASE2-OPENCODE.md`；**写任务 Handle 落库并标 done（E-2）** | ✅ |
| S-D | deliver 热更新（F-1）+ builtin task_completed（S1） | 运行中 `PUT /api/config` 设 `deliver.webhook_url`（**不重启**）→ issue #39 assign builtin Agent | task#4 success；水槽收到 `task_completed`（repo/issue_id=39/action=comment/content 全字段）——**热更新即时生效** | ✅ |
| S-E | pr_merged 事件（2.4.3） | API 合并 PR #38 | 水槽 3 秒内收到 `pr_merged`（pr=38 issue=37 action=notify） | ✅ |
| S-F1 | hub-hermes analyze（2.1.2） | issue #40 assign `e2e-hm-analyze` | 假 Hermes 收到 `POST /v1/runs`（Bearer 鉴权通过、session_id=`matea:e2e/gateway-poc`）；Poll 完成；评论写回；handle done；记忆已写 | ✅ |
| S-F2 | hermes reply + 跨任务记忆（2.1.4 + 2.1.5） | issue #40 评论 @mention | task#6 success；submission 含 `conversation_history`（4 轮）+ **input 注入「Previously remembered context: analysis_summary」**（D3 记忆共享）；session 续接同一 `matea:<repo>` | ✅ |
| S-G | 重启重接 + 幂等（2.1.1-a/b） | hold 模式提交任务 → `taskkill` matea → 重启 → flip complete | 重启日志 `Reattached 1 hub task(s)`；**submission 计数恒为 1（无重复 Submit）**；恢复后轮询完成、评论写回、handle done | ✅ |
| S-H | hub-opencode reply（2.2.3） | issue #35 评论 @mention | task#8 success；OpenCode 回答引用正确 skill 路径 | ✅ |
| S-I | hub-hermes review + diff 注入（2.1.3） | 新 PR #43 仅请求 `e2e-hm-review` | task#11 success；submission input 含 `## Diff` 段且含目标文件 diff 文本 | ✅ |

---

## 三、E2E 发现的问题与处置

### G-1（真实缺陷，已修复）：hub 路径任务完成不产生 deliver 事件 → 2.2.4 承诺不成立

- **发现**：deliver 配置生效后，hermes 任务（#5/#6/#7）与 opencode 任务（#8）完成时水槽**均无事件**，仅 builtin 任务（#4）有。代码核对：`mapHubResult` 仅在 hub 结果显式携带 `DeliverRequest` 时 emit，而 OpenCode 适配器从不构造 DeliverRequest —— 文档承诺「OpenCode 无 IM 时配 `deliver.webhook_url` 即可通知人类」（2.2.4 / AGENTS.md）实际不成立。
- **修复**：`mapHubResult` 在 `res.Deliver == nil` 时按 task 合成 `task_completed` 事件（语义与 builtin 一致：未配置静默 no-op）；`deliver.Client` 新增 `Enabled()`；新增 `TestMapHubResultSynthesizesDeliverWithoutRequest` 等 2 个单测。
- **回归验证**：task#9（hermes analyze）完成后水槽收到合成 `task_completed` ✅。

### G-2（日志误导，已修复）：deliver 未配置时仍打 "fanned out"

S-C 期间（webhook_url 为空）日志打印 `deliver event "task_completed" fanned out`，实际为空操作。修复：`emitDeliverEvent` 改用 `Enabled()` 判定，disabled 时与 nil 同样静默/告警，不再误报成功。

### G-3（归因丢失，已修复）：hub 分支 TaskContext 未填 `TaskID`

opencode 会话标题与日志出现 `Task 0`（应为真实任务 ID）。6 处 runner TaskContext 字面量补 `TaskID: task.ID`（analyze/review/interaction × hermes/opencode 两分支）。

### G-4（次要，未修，记录）：hermes 适配器 conversation_history 角色映射偏差

Agent 自己的历史评论被映射为 `user` 角色（判定条件 `c.Author == "matea" || c.Author == b.name`，而 Author 是 Gitea 用户名如 `e2e-hm-analyze`，不等于 backend 名 `hermes-local`）。多轮对话语义略受影响，不影响功能正确性。建议后续把 Agent 的 gitea_username 传入 TaskContext 以正确映射 assistant 角色。

### G-5（行为观察，未修，记录）：多同 role Agent 时 review 路由不保证选中被请求者

PR #36 上先请求过 `e2e-oc-review`，之后请求 `e2e-hm-review` 时任务仍路由给 `e2e-oc-review`（webhook 的 `requested_reviewers` 含全量列表，路由按序命中首个匹配 Agent）。换新 PR 仅请求 `e2e-hm-review` 则正确路由（S-I）。属既有路由语义，非 Phase 2 回归；多 review Agent 特化场景下建议后续按「被请求者精确匹配优先」优化。

---

## 四、回归状态

```
go build ./...          ✅
go vet ./...            ✅（修复涉及包）
go test ./... -count=1  ✅ 17/17 包 PASS（deliver 合成修复 + TaskID 修复后全量）
```

## 五、结论

Phase 2 全部新功能在**真实服务环境**下端到端验证通过：hub-opencode 三类任务（2.2.1–2.2.3）、hub-opencode 写任务全链路含 PR 落地与 Handle 持久化（2.4.4/E-2）、hub-hermes 三类任务含 diff/对话历史/记忆注入（2.1.2–2.1.5）、重启重接与幂等（2.1.1-a/b）、deliver 出站与热更新（2.3.3/2.3.4/S1/F-1）、pr_merged 生命周期事件（2.4.3）。E2E 期间发现并修复 1 个真实功能缺陷（G-1 deliver 合成）+ 2 个次要问题（G-2/G-3），记录 2 个观察项（G-4/G-5）。

**2.4 验收场景 Matea 侧至此全部可验收**；跨系统腿（真实 Hermes gateway ↔ 飞书）仍按既定口径留待人工验收。

### 复现方式

```bash
# 服务栈
cd x:\gitea && ./gitea.exe web                       # Gitea :3000
opencode serve --port 4096                           # OpenCode :4096
go run scripts/common/e2e-mock-hermes.go             # 假 Hermes :9090
go run scripts/common/e2e-deliver-sink.go            # deliver 水槽 :9095
set -a; source data/e2e-env.local; set +a
./matea.exe -config config.e2e.yaml                  # Matea :8080
# 然后经 Gitea API 建 issue/PR、assign/mention/请求 reviewer 驱动场景；
# 断言点：/api/tasks、水槽 GET /events、假 Hermes GET /debug/submissions、SQLite hub_handles/memories 表。
```
