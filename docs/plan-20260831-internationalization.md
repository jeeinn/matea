# Plan: Matea 中英双语界面（i18n）方案

> 日期：2026-08-31
> 状态：设计定稿（本文档不落地代码，待后续实现）
> 范围：所有**用户可见**的 Matea 输出文案（PR 标题/正文/评论、状态卡、L3 通知、失败/部分失败提示）
> 不在范围：内部运维日志、技术错误 detail、代码注释

---

## 0. TL;DR

| 问题 | 方向 |
|------|------|
| 当前文案全中文，未来如何支持英文？ | 抽出所有用户可见文案为语义化键，用消息目录管理；任务/Agent/仓库级 locale 决定一次任务输出什么语言 |
| 源语言选什么？ | 中文作为 fallback（与当前界面一致），英文作为第二语言；未来加日语等只需新增目录文件 |
| 语言由什么决定？ | 优先级：任务级 > Agent 级 > 仓库级 > 全局默认。异步任务要把最终决议的 locale 写入 `tasks.language`，确保重启/reattach 后仍一致 |
| 技术选型 | Go 官方 `golang.org/x/text/language` + `golang.org/x/text/message`；消息量大时可选 `nicksnyder/go-i18n` |

---

## 1. 背景

当前 Matea 的用户可见文案全部硬编码为中文，分布在：

- `internal/workflow/policy.go`：L3 通知模板（`L3AnalyzeDone`、`L3CoderPROpened`）
- `internal/workflow/status_card.go`：状态卡表头、状态标签
- `internal/agents/write_pr.go`：PR 标题前缀、PR body 标题、创建/更新回执
- `internal/dispatcher/comments.go`：`reviewerHint`、complete 状态卡追加文本
- `internal/dispatcher/executor.go`：成功/失败/部分失败评论模板

若未来面向国际用户，需要系统化地支持中英双语，而不是在代码里写 `if lang == "en"`。

---

## 2. 设计原则

### 2.1 用户文案与代码解耦

所有面向用户的字符串必须走统一翻译接口，禁止在业务代码中直接写死中文或英文句子。

```go
// 不推荐
body := "🔄 已更新 PR 分支"

// 推荐
body := T("pr.comment.updated_branch", locale, map[string]any{"branch": branchName})
```

### 2.2 中文作为 fallback

当前产品中文界面已经成熟，保持中文为默认/兜底语言，可降低迁移成本。英文作为第二语言逐步补全。

回退链：

```text
请求语言（如 en）→ 英文目录 → 中文目录（fallback）→ 直接返回键名（最后兜底）
```

### 2.3 语言决定时机

Matea 是异步后台服务，任务执行时通常已经没有原始 HTTP 请求上下文，因此语言不能在写回阶段按“当前请求”决定。建议按以下优先级在**任务创建时**固化：

1. **任务级**：`tasks.language` 字段，由任务创建时根据下方规则写入
2. **Agent 级**：每个 Agent 配置 `language: zh|en`，表示“该 Agent 产出内容的语言”
3. **仓库级**：`workflow_policies.language`，作为该仓库默认
4. **全局默认**：`config.yaml` 中 `server.language`

任务创建后，所有状态卡、PR、评论、失败提示均按 `task.Language` 渲染。

### 2.4 日志不翻译

内部运维日志、技术错误 detail 保持英文（或可配置单一语言），便于排障。只有最终写给用户看的 summary/评论/PR 走 i18n。

### 2.5 一次任务一种语言

同一次任务产出的 PR、评论、状态卡必须使用同一种语言，避免用户体验割裂。

---

## 3. 技术方案

### 3.1 消息目录

目录结构：

```text
locales/
├── default.json      # 键列表与规范（可选）
├── zh.json           # 中文（与当前硬编码文案一致）
└── en.json           # 英文
```

用 `//go:embed locales/*.json` 打包进二进制。

键命名规范：

```text
{模块}.{组件}.{语义}
```

示例：

```json
{
  "pr.title.solution": "AI Solution: {subject}",
  "pr.title.bugfix": "Bugfix: {subject}",
  "pr.body.heading": "## AI Generated Solution",
  "pr.comment.created": "✅ PR created: #{pr_id}",
  "pr.comment.updated_branch": "🔄 Updated PR branch `{branch}`",
  "status_card.state.running": "🔄 Processing",
  "status_card.state.done": "✅ Done",
  "status_card.task.label": "Task",
  "l3.analyze_done": "✅ Analysis complete (Task {task_id}).",
  "l3.reviewer_hint.named": "Request a review from {mentions} in the PR.",
  "l3.reviewer_hint.none": "Assign a review Agent in the PR to request a review.",
  "comment.footer": "*Task ID: {task_id} | Agent: {agent} | Type: {task_type}*"
}
```

### 3.2 翻译接口

提供一个包级函数（例如 `internal/i18n`）：

```go
package i18n

func T(key string, locale language.Tag, args map[string]any) string
func MustLocale(s string) language.Tag
```

使用 `golang.org/x/text/message` 的 `Catalog` + `Printer` 实现 ICU MessageFormat 子集，支持复数、select。

示例：

```go
T("pr.title.solution", localeEn, map[string]any{"subject": "Update README"})
// => "AI Solution: Update README"

T("status_card.state.running", localeZh, nil)
// => "🔄 处理中"
```

### 3.3 库选择

| 库 | 场景 | 说明 |
|---|---|---|
| `golang.org/x/text/message` | 推荐首选 | 官方、无额外依赖、支持 ICU MessageFormat、plural/select |
| `nicksnyder/go-i18n` | 消息量大、需要自动抽取 | 提供 `goi18n extract`、TOML/JSON 目录、回退链管理 |

如果项目消息量可控（目前约 30～50 条），`x/text/message` 足够；若未来要支持多贡献者翻译，可迁移到 `go-i18n`。

---

## 4. 需要抽出的文案清单

| 文件 | 当前硬编码示例 | 建议键 |
|------|---------------|--------|
| `internal/workflow/policy.go` | `✅ 分析完成（任务 {{task_id}}）` | `l3.analyze_done` |
| `internal/workflow/policy.go` | `✅ PR 已创建：{{pr_ref}}` | `l3.pr_opened` |
| `internal/workflow/status_card.go` | `\| **任务** \| %d \|` | `status_card.task.label` |
| `internal/workflow/status_card.go` | `🔄 处理中` / `✅ 完成` / `❌ 失败` | `status_card.state.*` |
| `internal/agents/write_pr.go` | `AI Solution: %s` | `pr.title.solution` |
| `internal/agents/write_pr.go` | `Bugfix: %s` | `pr.title.bugfix` |
| `internal/agents/write_pr.go` | `## AI 生成的解决方案` | `pr.body.heading` |
| `internal/agents/write_pr.go` | `✅ PR 已创建：#%d` | `pr.comment.created` |
| `internal/agents/write_pr.go` | `🔄 已更新 PR 分支` | `pr.comment.updated_branch` |
| `internal/dispatcher/comments.go` | reviewer hint 各种分支 | `l3.reviewer_hint.*` |
| `internal/dispatcher/executor.go` | `🤖 **AI Agent Response**` | `comment.result.heading` |
| `internal/dispatcher/executor.go` | 失败/部分失败模板 | `comment.failure.*`、`comment.partial_failure.*` |

---

## 5. 数据模型改动

### 5.1 `tasks` 表

新增列：

```sql
ALTER TABLE tasks ADD COLUMN language TEXT NOT NULL DEFAULT '';
```

空字符串表示“按 Agent/仓库/全局规则在创建时解析后回填”。

### 5.2 `agents` 表

新增配置字段：

```sql
ALTER TABLE agents ADD COLUMN language TEXT NOT NULL DEFAULT '';
```

### 5.3 `workflow_policies` 表

新增配置字段：

```sql
ALTER TABLE workflow_policies ADD COLUMN language TEXT NOT NULL DEFAULT '';
```

### 5.4 `config.yaml`

新增全局默认：

```yaml
server:
  language: "zh"  # 默认中文
```

---

## 6. 落地步骤建议

### 阶段一：基础设施（保持中文界面不变）

1. 创建 `internal/i18n` 包，加载 `locales/zh.json`，默认 locale = `zh`。
2. 把所有当前硬编码中文文案抽成键，代码里改为 `T("key", localeZh, args)`。
3. 此时 UI 100% 保持中文，只是不再硬编码。
4. 新增 CI 检查：禁止直接写死用户可见中文字符串到业务代码（可用正则或 `go vet` 插件）。

### 阶段二：补英文

1. 新增 `locales/en.json`。
2. 为 Agent / 仓库 / 全局配置增加 `language` 字段与迁移。
3. 任务创建时按优先级解析 locale 并写入 `tasks.language`。
4. 所有渲染点读取 `task.Language` 渲染对应语言。

### 阶段三：细节打磨

1. 日期/时间格式本地化（`2026-08-31` vs `Aug 31, 2026`）。
2. 数字/百分号格式（`1,234.56` vs `1.234,56`）。
3. 复数场景（如状态卡显示“N 个文件”）。
4. 增加 `goi18n extract` 或类似机制，防止新增键漏翻译。

---

## 7. 风险与注意事项

| 风险 | 说明 | 缓解 |
|------|------|------|
| 键膨胀 | 每种语言一个文件，键多了难维护 | 按模块分层命名；CI 检查键集合一致性 |
| 翻译延迟 | 英文更新滞后于中文 | 把英文作为必填项纳入 PR 检查；fallback 到中文兜底 |
| 异步任务语言漂移 | 任务创建后配置改了，输出语言不应变 | 创建时把 locale 固化到 `tasks.language` |
| 状态卡/PR 跨语言显示 | 同一 issue 下不同任务可能用不同语言 | 按任务语言各自渲染，不强制统一；仓库级默认可减少混乱 |
| 占位符安全 | `{subject}` 等占位符若来自用户输入，需防注入 | 翻译前先做 HTML/Markdown escape，或只放纯文本 |

---

## 8. 范围外（本次不做）

- WebUI 前端双语（本方案只覆盖后端生成的 PR/评论/状态卡）。
- 自动检测用户浏览器语言（Matea 是异步服务，无持久请求上下文）。
- 给 harness/Hub 下发语言提示（Hub 侧输出语言由 Hub 自身决定；Matea 只控制 Matea 自己写的文案）。
- 实时切换已创建任务的语言（任务创建时固化，不追溯修改）。

---

## 9. 验收标准（未来实现时）

- [ ] `locales/zh.json` 与当前所有用户可见文案一致。
- [ ] `locales/en.json` 覆盖所有键。
- [ ] 配置 `server.language: en` 后，新任务的 PR/评论/状态卡全部英文输出。
- [ ] Agent 配置 `language: en` 可覆盖全局默认。
- [ ] 任务创建后修改 Agent/仓库语言，不影响已创建任务的输出语言。
- [ ] CI 检查：业务代码不再直接出现 `#N 引用` 等用户可见中文/英文硬编码（日志除外）。
