# Plan: Hub 后端对话日志透传（方案 A）

> 日期：2026-08-27
> 状态：设计定稿（本文档不落地代码，仅供评审与后续实施）
> 关联：Phase 2 Hub 生态 / `task_conversation_logs` 持久化

---

## 0. 决策约束（统一控制）

**Hub 后端的对话日志写入，统一受既有开关 `debug.conversation_log.enabled` 控制，不新增独立开关。**

理由：

- `debug.conversation_log.enabled` 已是系统配置里的热更新开关（默认关闭），`debug.conversation_log.max_content_chars`（默认 100000）同理。
- 前端对话页 `web/src/components/TaskConversation.vue:50` 已内置空态提示：

  > 请在「系统配置」开启 `debug.conversation_log.enabled` 后重新跑任务

  即：**hub 落库后前端零改动即可显示**，且未开启时自动提示用户去开开关。开关语义对 builtin / hub 一视同仁，符合「统一控制」。
  - 注：该空态提示的第二行「仅多轮 Agent Loop（如 solve_issue / solve_comment）会写入对话日志」在 hub 任务也会落库后将不再准确，本次一并更新。
- 因此本文档**移除**早前讨论中提议的 `debug.conversation_log.record_hub` 新键——hub 直接复用 `enabled` + `max_content_chars`。

落库判断位置：`internal/agents/runners.go` 的 `RunnerFactory` 已持有 `getDebugConfig func() config.DebugConfig`（`runners.go:81`），`runViaHub` 内直接 `f.getDebugConfig().ConversationLog.Enabled` / `.MaxContentChars` 即可，无需注入新依赖。

---

## 1. 背景与目标

Hub 后端（OpenCode / Hermes / 未来的 OpenClaw / DeepSeek harness）当前对 Matea 是「黑盒」：WebUI 对话页只显示 builtin 路径（经 Agent Loop 的 `ConversationRecorder`）记录的逐轮 LLM 消息与 tool call，hub 任务在对话页完全空白。

**目标（方案 A，本次范围）**：让 hub 任务的对话页至少可见：

- `user` 消息（system prompt + user prompt）——来自 Matea 权威源 `TaskContext`
- `assistant` 最终回复
- 失败时的 `assistant` error 消息

> 取舍：方案 A **不要求**逐轮 tool call 粒度（与 builtin 不同）。能像 builtin 那样看到每轮 tool call 属于增强项，不在本次范围。

---

## 2. 现状确认（关键事实）

| # | 事实 | 位置 |
|---|------|------|
| 1 | WebUI 对话页已完整渲染 `role`(system/user/assistant/tool) + `content` + `tool_calls`，按 `iteration` 分组（iteration 0 = 初始输入）。只要往 `task_conversation_logs` 插行，前端自动显示，**零改动** | `web/src/components/TaskConversation.vue` |
| 2 | OpenCode 在 `getLastAssistantMessage` 里**已拉取完整 message list**（`GET /session/{id}/message`），返回 `[]opencodeMessagesListItem{Info{Role,Error}, Parts{Type,Text}}`，但**只取最后一条 assistant 文本当 summary 丢弃其余**——完整 transcript 就在手边 | `internal/agents/opencode_http.go` |
| 3 | hub 后端当前完全不写对话日志；builtin 仅在 `debug.conversation_log.enabled` 开启时经 loop 的 `ConversationRecorder` 记录（含 tool call） | `internal/agents/conversation_recorder.go` |
| 4 | 落库函数 `AppendConversationMessages(taskID, iteration, []llm.Message, maxContentChars)` 已存在，`llm.Message` 含 `Role / Content / ToolCalls / ToolCallID` | `internal/store/conversation_log.go:23` |
| 5 | `TaskContext` 已带 `SystemPrompt` / `UserPrompt` 字段，是 input 的权威源；runner 持有 `tc` | `internal/agents/hub_backend.go:97-98` |
| 6 | `debug.conversation_log.enabled` / `.max_content_chars` 已是系统配置热更新开关，WebUI 开关 + 输入框已就绪 | `internal/config/*`、`web/src/views/SystemConfig.vue` |

---

## 3. 设计

### 3.1 数据流（统一、无 per-backend 分支）

```
OpenCode / Hermes 后端
  └─ 把「助手侧 transcript」填进 BackendResult.Messages   ([]llm.Message)
        ↓
runViaHub（hub_run.go，已有 tc *TaskContext）
  └─ 终态后单写入点（受 f.getDebugConfig().ConversationLog.Enabled 控制）：
       iteration 0 : system(tc.SystemPrompt) + user(tc.UserPrompt)   ← 来自 tc，权威源
       iteration 1+: res.Messages（assistant / tool / error）          ← 后端透传
        ↓
     db.AppendConversationMessages(task.ID, iteration, msgs, maxContentChars)
        ↓
     WebUI 对话页自动显示（未开启开关时显示既有提示文案）
```

**关键设计点**：input 由 runner 从 `tc` 写，后端只提供「助手侧 transcript」。好处是 Hermes 这类 poll 拿不到 `tc` 的后端，也只需在 `Submit` 缓存 input、`Poll` 追加 output，不必重复搬运 SystemPrompt。

### 3.2 改动清单

| # | 文件 / 函数 | 改动 |
|---|------------|------|
| 1 | `internal/agents/hub_backend.go` | `BackendResult` 新增 `Messages []llm.Message` 字段（JSON tag `messages,omitempty`）。 |
| 2 | `internal/agents/coding_backend.go` | `CodingResult` 新增 `Messages []llm.Message` 字段，用于把 OpenCode 侧完整 transcript 带回到 `Submit`。 |
| 3 | `internal/agents/opencode_http.go` | `getLastAssistantMessage` 改签名返回 `(summary string, messages []opencodeMessagesListItem, err error)`；`sendMessage` 在发送后保存完整 list；`Run` 把 list 映射进 `CodingResult.Messages`。 |
| 4 | `internal/agents/opencode_http.go` | `Submit` 把 `CodingResult.Messages` 映射成 `[]llm.Message` 填 `BackendResult.Messages`。**丢弃 OpenCode 自带回显的 `user` role 消息**，避免与 `tc.UserPrompt` 重复。 |
| 5 | `internal/agents/opencode_http.go` | **失败路径**：`Run` 不再在 `sendMessage` 失败时直接返回 error，而是返回 `&CodingResult{Success:false, Messages: messages, RemoteSessionID: sessionID}`；`Submit` 把该失败结果缓存进 `hubResults` 并仍然返回 `Handle`，让 `runViaHub` 的 `Poll` 进入 `StateFailed` 分支并拿到 `res.Messages`。 |
| 6 | `internal/agents/opencode_http.go` | `Poll` 的 cache-miss 重拉分支同样回填 `Messages`，保证 Matea 重启 re-attach 也能记录。 |
| 7 | `internal/agents/hub_run.go` | 新增 `recordHubConversation(task, tc, res, runErr)`：在 `StateDone` 的 read / git_sync 两条返回前、以及 `StateFailed` 分支调用。开启 `ConversationLog.Enabled` 则写入。input 用 `tc.SystemPrompt`/`tc.UserPrompt` 构造 iteration 0；output 用 `res.Messages` 构造 iteration 1+；`res == nil` 时从 `runErr` 构造一条 `assistant` error 消息。 |
| 8 | `web/src/components/TaskConversation.vue` | 更新空态提示第二行，说明 hub 任务在开关开启后也会写入对话日志。 |
| — | （删除早前提议的 `record_hub` 新键） | 统一控制，复用 `enabled` + `max_content_chars`，**无新增配置项**，配置层 0 改动。 |

### 3.3 `iteration` 分组方案

- iteration 0：本次任务的 input（`system` + `user`）。
- iteration 1, 2, 3…：每个 assistant 回合一个 iteration；其前的 `tool` 消息挂在同一 iteration（最接近 builtin 多轮视图，又不必展开 tool 调用细节）。
- 失败时：最后一个 assistant iteration 写 `role=assistant` + `content=error message`（error 来自 `opencodeMessagesListItem.Info.Error` 或 `BackendResult` 的错误透传）。

### 3.4 失败路径详细设计

**问题背景**：当前 `OpenCodeHTTPBackend.Submit` 一旦 `Run` 返回 error，就直接把 error 返回给 `runViaHub`，导致 `runViaHub` 在 `backend.Submit` 处就退出，**没有机会调用 `recordHubConversation`**。

**解决方案**：

1. `Run` 内部 `sendMessage` 失败时，不再直接返回 `error`，而是返回 `&CodingResult{Success:false, Messages: msgs, RemoteSessionID: sessionID}`。
2. `Submit` 中检查 `res.Success`：
   - 若为 `true`，按成功路径处理。
   - 若为 `false`，构造 `BackendResult{Summary: res.Summary, Messages: res.Messages}`，缓存在 `hubResults[remoteID]` 中，并返回 `Handle`。
3. `runViaHub` 的 `Poll` 会从 `hubResults` 中取出该结果，由于当前 `OpenCodeHTTPBackend.Poll` 在 `out.err != nil` 时返回 `(nil, StateFailed, out.err)`，`res` 仍为 nil，因此需要同时调整：
   - **调整 `Poll` 语义**：失败时也返回 `out.result`（即带 Messages 的 BackendResult），同时透传 error。改为：
     ```go
     if out.err != nil {
         return out.result, StateFailed, out.err
     }
     ```
   - 这样 `runViaHub` 的 `StateFailed` 分支可以拿到 `res.Messages`。
4. `recordHubConversation` 签名设计为：
   ```go
   func (f *RunnerFactory) recordHubConversation(task *store.Task, tc *TaskContext, res *BackendResult, runErr error)
   ```
   - 当 `res != nil && len(res.Messages) > 0` 时，使用 `res.Messages`。
   - 当 `res == nil || len(res.Messages) == 0` 时，从 `runErr` 构造一条 `role=assistant` 的 error 消息，保证失败时对话页至少能看到错误原因。

### 3.5 字段映射（OpenCode → llm.Message）

```
opencodeMessagesListItem.Info.Role  → msg.Role      ("user"|"assistant"|"system"|"tool")
opencodeMessagesListItem.Info.Error → 失败时并入 msg.Content（前缀标注）
opencodeMessagesListItem.Parts[].Text（Type=="text") → msg.Content
（tool parts 当前 opencodeMessagePart 仅解析 Type/Text，不展开 tool 细节；
 方案 A 不要求 tool 粒度，tool 消息仅在存在时尽力以 content 文本呈现）
```

**去重规则**：丢弃 OpenCode 回显的 `role=user` 消息；若实际观察到 `role=system` 回显，同样丢弃。

---

## 4. 兼容性调研（核心问题）

> 其他 hub-*（Hermes / OpenClaw / DeepSeek harness）能否同样兼容？还是每次对接新 hub 都要重写一种方法？

**机制完全统一，数据完整度取决于该 hub 的 API 是否吐 transcript。** 这不是 per-backend 新方法，而是「一次契约扩展（`BackendResult.Messages`）+ 一个写入点（`recordHubConversation`）」。

| Hub | 能否填 `Messages` | 完整度 | 落地动作 |
|-----|------------------|--------|----------|
| **OpenCode** | ✅ 已拉全量 list | **full**：system/user/assistant/error；tool parts 尽力（方案 A 不要求 tool 粒度） | 改动最小，见 §3.2 #3-#6 |
| **Hermes** | ⚠️ 部分 | **partial**：`GET /v1/runs/{id}` 当前只回 `output` 字符串（`hermes.go` 的 `hermesPollResponse`），**无 transcript**。填 `Messages` = input(`tc`) + output 摘要，无 tool 粒度 | 若要 full，需 **Hermes 侧扩展**（poll 返回 `messages` 或新增 transcript 端点）。Matea 机制不变，只是数据源缺 |
| **OpenClaw** | 🆕 未实现 | 取决于其 API | 写一个适配器包（参考 `backends/hermes`，`init()` 注册工厂），能吐 transcript 就 full，否则 partial |
| **DeepSeek harness** | 🆕 未实现 | 同上 | 同上 |

**结论**：

- 「对话透传」= 一次契约扩展 + 一个写入点。
- 新 hub = 一个适配器文件（实现 `HubBackend` 本来就是必修的），填同一个 `Messages` 字段。
- 唯一变量是「该 hub 的 API 给不给 transcript」——给就 full，不给就 partial（input + output）。Matea 主体、`RunnerFactory`、`runViaHub`、`store`、`WebUI` **全部不动**。

---

## 5. 风险与注意事项

- **体积**：OpenCode 多轮 transcript 可能很大，靠 `MaxContentChars`（默认 100000）截断保护；hub 路径复用 builtin 默认，无需另设。
- **敏感数据**：hub 回传内容含 diff、文件路径等仓库片段，`task_conversation_logs` 是敏感表（已有 403 权限保护），注意 retention 策略。
- **失败 transcript 易丢**：§3.4 的失败路径改造是本次落地的关键，必须保证 `Run` 失败、`Submit` 失败、`StateFailed` 分支都能保留 transcript。
- **tool 粒度**：方案 A 明确不要求。若以后想要，需扩展 `opencodeMessagePart` 解析 tool 的 name/input/output（增强项，非本次）。
- **input 去重**：OpenCode 自带回显的 `user` 消息必须丢弃，否则与 `tc.UserPrompt` 重复显示（§3.2 #4）。
- **`Poll` 失败语义调整**：将 `Poll` 在 `out.err != nil` 时从返回 `nil` result 改为返回 `out.result`，需确认无其他调用方依赖旧语义（`runViaHub` 是唯一调用方，且新语义更优）。

---

## 6. 验证与测试（建议落地时补齐）

在 `internal/agents/opencode_hubbackend_test.go`（已存在）新增：

1. OpenCode 透传全量 message list 落库（开 `enabled`，跑完校验 `task_conversation_logs` 含 system/user/assistant）。
2. `Run`/`sendMessage` 失败时 `CodingResult.Messages` 非空，并最终落入 `task_conversation_logs`。
3. `Submit` 同步失败时仍返回 Handle，Poll 进入 `StateFailed` 并保留 `Messages`。
4. `Poll` 重启 re-attach 回填 messages（cache-miss 重拉分支）。
5. 统一控制校验：`enabled=false` 时不落库（对话页显示既有提示文案）。

执行：`go build ./...` → `go test ./internal/agents/... ./internal/store/... -count=1`。

---

## 7. 落地步骤（建议）

1. 新分支（如 `feat/hub-conversation-log`，遵循 `feat/` 命名）。
2. 按 §3.2 #1-#8 改码（先 `BackendResult`/`CodingResult` 加字段，再 OpenCode 透传，再失败路径改造，再 `recordHubConversation`，最后前端文案）。
3. 补 §6 单测并跑通。
4. 本地开 `debug.conversation_log.enabled` 跑一个 OpenCode hub 任务，核对对话页显示。
5. 更新 `CHANGELOG.md` 的 `[Unreleased]`（标注「hub 后端对话日志透传，复用 `debug.conversation_log.enabled`」）。

---

## 8. 范围外（本次不做）

- builtin 与 hub 对话日志的统一 schema 重构（保持各自写入，前端统一读取）。
- tool call 逐轮粒度（需扩展 OpenCode part 解析）。
- 各 hub 的 transcript 数据源增强（Hermes 等需其 API 侧支持）。
- 自动开启开关（保持「手动开启后重跑」语义，与既有 builtin 一致）。
