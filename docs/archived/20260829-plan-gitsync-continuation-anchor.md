# git_sync 续写锚点失效排查与修复方案（PR 二次 @code-opencode）

- 日期：2026-08-29
- 现场：Gitea `jeeinn/rust-study` issue #7 → 任务 14 → PR #8（`matea/hub-14` = `148f6cc1`）；PR 上二次 `@code-opencode` → 任务 16 → `matea/hub-16` = `4ec8f506`
- 报错：`runner execution: git_sync approve: git_sync approve: draft head 4ec8f50693cca0e06dd47c3e444cecccf501f518 is not descended from anchor 148f6cc18ef7b2978d698bb28feebb74533f7ad5 (start-point anchoring)`
- Matea 版本：0.12.1（远程 182.92.129.124:8080）

## 1. 现场证据（远程实测）

| 事实 | 值 |
|------|-----|
| PR #8 head | `matea/hub-14` = `148f6cc18ef7…`（任务 14 推送，footer `matea-task-id: 14`） |
| PR #8 base | `main` = `f7a40c966875…` |
| `matea/hub-16` tip | `4ec8f50693cc…`，**父提交 = `f7a40c966875`（main tip）**，不是 `148f6cc1` |
| `matea/hub-16` 提交内容 | 重新做了一遍 README 文档化 + 评审反馈修复，footer `matea-task-id: 16`，author `matea-hub <hub@matea.local>` |
| `main` 在此期间是否移动 | 否（main 仍是 `f7a40c966875`） |

关键推论：任务 16 的 hub **确实执行了 git_sync 契约**（deploy key、分支名、author、`matea-task-id: 16` footer、只推 `matea/hub-16` 都正确），
唯独**起点不是续写锚点 `148f6cc1`，而是默认分支 tip**。也就是说任务 14 的成果没有被继承，hub 从 main 重做了一遍。

## 2. 链路还原

```
issue_comment(PR #8, "@code-opencode")
  → workflow.Resolver.resolveComment
      isPRConversation=true → prID=8；resolveLinkedIssue("Refs #7") → issueID=7
  → dispatcher.pipeline
      issueID(effective)=7 → 复用 issue#7 的 coder session（Branch=matea/hub-14, LastHead=148f6cc1）
      task.BaseBranch = evt.PR? 无 → 退化取 sessionBranch = "matea/hub-14"   ← ①
  → runWriteTask → runViaHub（git_sync 写通道）
      transport.Prepare(baseBranch="matea/hub-14")                            ← ②
          base = "matea/hub-14"；BaseHEAD = 148f6cc1
          info.BaseBranch = "matea/hub-14"
      AnchorHEAD = session.LastHead = 148f6cc1                                ← ③
  → OpenCode 后端 Submit
      userPrompt = tc.UserPrompt + BuildGitSyncInstructions(含锚点)            ← ④
  → hub 执行：clone →(未从锚点起分支)→ commit → push matea/hub-16
  → Approve → validateGitSyncDraft：anchor=148f6cc1 不是 4ec8f506 的祖先 → 失败
```

## 3. 根因

### P0-1：`store.Task.BaseBranch` 语义被 git_sync 通道误用（`internal/agents/workspace_transport.go:159-182`）

`store/task.go:33` 对字段的注释是 **`PR head branch for solve_comment`（工作分支/PR 头），不是合并基线**。
builtin 通道就是这么用的，并且显式做了区分（`internal/agents/write_workspace.go:144-181`）：

```go
// "BaseBranch set" does NOT imply a genuine PR head here ...
sessionDerived := strings.TrimSpace(task.BaseBranch) == "" || task.BaseBranch == sessionBranch
case isExistingBranch && sessionLastHead != "" && task.BaseBranch == "":
```

而 git_sync 通道把同一个字段当成 **merge base**（`hub_run.go:164` → `Prepare` → `info.BaseBranch`，最终
`FinalizeWriteTaskPR(..., info.BaseBranch, ...)` 作为 PR 的 `Base`）。

后果（本次任务 16 全部命中）：
1. `info.BaseBranch = "matea/hub-14"` → 若校验通过，PR 会被开成 **`matea/hub-16 → matea/hub-14`**，而不是 → `main`（`write_pr.go:59-64`）；
2. `BaseHEAD = 148f6cc1`（draft 分支头）被当作基线，漂移检测窗口因此失效（本应在 main 上比对）；
3. `info.anchor()` 与 `BaseHEAD` 在本例巧合为同一 commit，掩盖了二者语义不同的事实，也让报错信息无法区分"锚点没下发"和"hub 没遵守"。

正确取值：任务有 PRID 时用 **PR 的 base ref**（本例 = `main`），否则用仓库默认分支。
`task.BaseBranch`（PR head / session 分支）对 git_sync 通道应当完全忽略。

### P0-2：续写锚点对 hub 只是"建议"，未做平台侧强制（`internal/agents/workspace_transport.go:419-464`）

`BuildGitSyncInstructions` 在 `AnchorHEAD != ""` 时下发 `git checkout -b matea/hub-16 148f6cc1…` 与续写说明，
但：
- 契约里**没有禁止 `--depth` / `--single-branch`**。hub 一旦浅克隆，锚点 commit 不在本地，`checkout -b <anchor>` 必然失败，
  模型（违反"失败即停止"的约定）退回 `git checkout -b matea/hub-16`，就从默认分支 tip 起分支了 —— 与现场证据完全吻合；
- 契约里**没有"开工前自校验"步骤**（`git merge-base --is-ancestor <anchor> HEAD`）；
- 校验只发生在 **Approve（整轮跑完之后）**，用户要等几分钟才看到失败。

### P1-1：OpenCode 后端的 memory 注入被静默丢弃（`internal/agents/opencode_http.go:625-640`）

```go
userPrompt := tc.UserPrompt
if mc := BuildMemoryContext(tc); mc != "" {
    userPrompt = strings.TrimSpace(userPrompt + "\n\n" + mc)   // 拼好了
}
if gitSync {
    userPrompt = strings.TrimSpace(tc.UserPrompt + "\n\n" +    // ← 又从 tc.UserPrompt 起算，mc 被丢掉
        BuildGitSyncInstructions(...))
}
```

注释写明"memory 拼在 git_sync 契约之前（保留 recency）"，实际被覆盖。
Hermes 后端写法正确（`backends/hermes/hermes.go:304`：`req.Input += "\n\n" + mc`）。
副作用：任务 16 的 OpenCode **完全没有收到"## Session continuation memory / task 14 做过什么"**，
这直接助长了它"从 main 重做一遍"的行为，是本次事故的放大项。

### P2-1：报错可诊断性不足

`is not descended from anchor X (start-point anchoring)` 没告诉运维：draft 实际是从哪个 commit 起的、
是不是从 base tip 起的、上一次任务的工作是否被丢弃。

### P2-2：`fetchDraft` 只拉 draft + base 两个分支（`workspace_transport.go:321-346`）

锚点只有在"draft 确实从它派生"时才随历史被拉到本地，因此当前不会误报；但若将来引入浅抓取/shallow，
会退化成"锚点对象不存在 → 一律判失败"。建议显式兜底抓取锚点。

## 4. 修复方案

### F1（P0-1）git_sync 通道自行解析真正的 base 分支

`workspace_transport.go` 的 `Prepare` 不再信任传入的 `baseBranch`：

```go
// resolveBaseBranch 决定 PR 的合并基线与 drift 窗口。
// task.BaseBranch 语义是"工作分支/PR head"（store/task.go:33），绝不能当基线用。
func (t *gitSyncTransport) resolveBaseBranch(adminClient *gitea.Client, owner, repo string, task *store.Task, repoDefault string) string {
    if task != nil && task.PRID > 0 {
        if pr, err := adminClient.PRGet(owner, repo, task.PRID); err == nil {
            if ref := strings.TrimSpace(pr.Base.Ref); ref != "" {
                return ref
            }
        }
    }
    if b := strings.TrimSpace(repoDefault); b != "" {
        return b
    }
    return gitea.ResolveDefaultBranch("")
}
```

并在 `Prepare` 中把 `base := gitea.ResolveDefaultBranch(baseBranch)` 换成上述调用；
`hub_run.go:187`（re-attach 分支的 `BaseBranch: task.BaseBranch`）同样需要持久化/回填正确基线
（建议：`SaveHubHandle` 时已经存了 `BaseHEAD`，补一列 `base_branch` 或在 re-attach 时用同一解析函数重算）。

### F2（P0-1）拒绝把 hub draft 分支当基线（纵深防御）

```go
if strings.HasPrefix(base, DraftBranchPrefix) { // matea/hub-
    return nil, nil, fmt.Errorf("git_sync prepare: refusing draft branch %q as base branch", base)
}
```

### F3（P0-2）契约硬化：显式禁浅克隆 + 开工前自校验

`BuildGitSyncInstructions` 第 3、4 步改为：

```
3. 完整克隆（禁止 --depth / --single-branch，锚点必须可达）：
   %[3]s git clone --no-single-branch %[4]s repo
4. cd repo && git config user.email "hub@matea.local" && git config user.name "matea-hub" \
   && git fetch origin %[2]s && git checkout -b %[1]s %[2]s \
   && git merge-base --is-ancestor %[2]s HEAD
   最后一条自校验失败＝起点错了：STOP 并报告，不要改用默认分支重新起分支。
```

（锚点为空时保持原行为：`git checkout -b <branch>` + `git merge-base --is-ancestor <BaseHEAD> HEAD`。）

### F4（P1-1）修 OpenCode memory 覆盖

```go
if gitSync {
    userPrompt = strings.TrimSpace(userPrompt + "\n\n" + BuildGitSyncInstructions(...))
}
```

补测试：断言 OpenCode Submit 的 prompt 同时包含 `## Session continuation memory` 与 `## Git workflow (MANDATORY`，
且 memory 在前。

### F5（P2-1）报错带上"实际起点"

`fetchDraft` 里额外记录 `merge-base <anchor> <draft>` 与 draft 是否从 base tip 派生，
失败信息升级为：
`draft 从 <base tip> 起分支，未继承续写锚点 <anchor>（上一轮工作不在分支内）`。

### F6（**经评审后决定不实现**）平台侧强制：Prepare 时把 draft ref 建在锚点上

`git push origin <anchor>:refs/heads/matea/hub-{taskID}`，hub 即便只跑 `git checkout -b matea/hub-N`
也会 DWIM 落到锚点；且 hub 若仍从 base tip 起分支，其 push 会因 non-fast-forward 被拒（fail 提前到 push 时刻）。
代价：Matea 首次为一个空 ref 推送（trust model v3 的"Matea 不代推"需要开这个口子），
且 `gitsync_sweep.go` 目前只扫 deploy key、不扫孤儿 draft 分支，需要补清理。

**决议（2026-08-29）：不实现。** 理由：

1. **它要解决的问题已被更轻的手段覆盖。** F6 的核心收益是"把起点错从 Approve 提前到 push 时刻"。
   而 F3 已经把失败点提前到 hub 的第 4 步——`checkout -b <anchor>` 之后紧跟
   `merge-base --is-ancestor <anchor> HEAD` 自校验，跑任何代码之前就会失败；F5 再兜一层，
   让漏网时的报错直接给出正确的 `git checkout -b` 命令。剩下的收益只是"少跑一轮 LLM"，
   不值得为此改动信任模型。
2. **成本不在代码量，在破例。** Matea 为一个空 ref 主动 push，直接违反 trust model v3
   "Matea 不代推"的不变量。这条不变量是 git_sync 其余安全校验的信任基础——deploy key 按任务发放、
   draft 分支独占性、必需 footer 都建立在"Matea 只校验不代写"之上。为 P2 级收益开口子，
   后续每个"顺手让 Matea 推一下"的改动都会援引此例。
3. **新增高危清理面。** Prepare 建了 ref 而任务失败/取消，draft 分支即成孤儿；
   `gitsync_sweep.go` 得新增远端 ref 的扫描与删除，而删除远端 ref 是不可逆操作，
   误删的代价远高于本次要防的问题。

若将来确实需要平台侧强约束，优先选这两个不破坏信任模型的变体：
(a) Prepare 时把锚点打成一个 tag 推上去（tag 非业务 ref），下发给 hub 去 `git fetch && git checkout -b <branch> <tag>`；
(b) 保持现状，把"起点错"作为 Approve 的硬门禁并附修复命令（F5 已实现）。

## 5. 验收

- `internal/agents` 单测：新增"PR 评论续写"用例——session 带 `Branch=matea/hub-14, LastHead=148f6cc1`、
  `task.BaseBranch="matea/hub-14"`、`task.PRID=8`（PR base=main），
  断言 `info.BaseBranch == "main"` 且 `FinalizeWriteTaskPR` 收到的 Base 为 `main`；
- 契约文本用例：断言含 `--no-single-branch`、锚点 checkout、以及 `merge-base --is-ancestor` 自校验；
- OpenCode Submit 用例：memory 与 git 契约同时存在且顺序正确；
- 端到端（本地 Gitea + OpenCode + mock_llm，见 `~/.workbuddy/skills/matea-gitsync-e2e`）：
  复现 issue→PR→PR 二次 mention，mock hub 故意从 main tip 起分支，确认错误信息可读且不开错 base 的 PR。

## 6. 落地顺序建议

F1 + F2 + F4（纯 Matea 侧，风险低、本次直接致病）→ F3 + F5（契约与可诊断性）→ ~~F6~~（评审后决定不实现，理由见第 4 节 F6 条目）。

## 7. 落地状态（2026-08-29）

F1~F5 已实现并合入（`52f5d28` 代码+测试、`05fd298` 文档）；**F6 经评审后决定不实现**，理由见第 4 节 F6 条目下的「决议」。

| 项 | 实现位置 | 与方案的差异 |
|----|---------|-------------|
| F1 | `internal/agents/workspace_transport.go` `resolveGitSyncBaseBranch`；`internal/gitea/pr.go` `PRBaseRef`；`internal/agents/hub_run.go` re-attach 分支 | `Prepare` 签名去掉 `baseBranch` 参数（改由自身解析）；`hub_handles` 新增 `base_branch` 列，旧行 re-attach 时按同一规则重新解析；**不保留 "main" 兜底**——解析不出非 draft 基线就报错，避免猜错 base |
| F2 | `isDraftBranch` + `resolveGitSyncBaseBranch` | 对 draft 前缀不是硬失败而是"告警 + 退到仓库默认分支"（PR base 若是 `matea/hub-*`，只能是旧版 Matea 建出来的错 PR，硬失败会让用户彻底卡住）；只有连仓库默认分支都是 draft 时才报错 |
| F3 | `BuildGitSyncInstructions` | 与方案一致：`--no-single-branch`、禁 `--depth`、`checkout -b <anchor>` 后追加 `git merge-base --is-ancestor <anchor> HEAD`；锚点为空时对 `BaseHEAD` 做同样的自校验 |
| F4 | `internal/agents/opencode_http.go` Submit | 与方案一致 |
| F5 | `fetchedDraft.StartedFromBase` / `MergeBase` + `validateGitSyncDraft` | 另加了锚点对象缺失时按 sha 兜底抓取一次（P2-2），避免"抓不到"被误判为"起点错" |
| F6 | **不实现** | 失败点已由 F3 提前到 hub 的第 4 步、F5 补齐可读性，剩余收益（少跑一轮 LLM）不足以换取"Matea 代推"这一信任模型破例与孤儿分支清理的高危面；替代方案记在第 4 节 F6 条目下 |

新增测试：`internal/agents/gitsync_base_branch_test.go`（基线解析 4 例 + PR 续写开 PR 落在 main + 旧 handle re-attach + 报错文案），
`workspace_transport_test.go` 两个新校验用例，`gitsync_continuation_test.go` 的契约文本与 OpenCode memory 顺序用例。
CHANGELOG 已记入 `[Unreleased] / Fixed`。
