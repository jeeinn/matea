# IM 渠道接入架构分析

> 日期：2026-08-08 ｜ 状态：**方案建议 · 待讨论**
> 范围：探讨 IM（飞书/企微/Slack）与 Matea 的集成模式，不改动代码
> 核心问题：用户通过 IM 创建 Issue / 触发 Agent → Matea 执行 → 结果回流 Gitea，这条链路如何设计？

---

## 0. 场景定义

### 0.1 用户故事

> 用户在飞书群聊里说："@matea 帮我看看 ai-dev 仓库的 issue #12 是什么问题，让 analyst 分析一下"
> 
> 期望：Agent 分析完成 → 结果出现在 Gitea Issue 评论 → 用户同时在飞书收到通知

### 0.2 端到端链路

```
用户 (飞书/企微/Slack)
  │
  │ ① 发消息
  ▼
Hub (Hermes / 自建 bridge)
  │
  │ ② 解析意图 → 创建/查询 Gitea Issue
  │ ③ 触发 Matea 执行（webhook 或 REST）
  ▼
Matea
  │
  │ ④ Agent 处理（builtin / OpenCode / Hermes）
  │ ⑤ 写回 Gitea（评论/PR）
  │ ⑥ deliver 出站事件（task_completed）
  ▼
Hub (接收 deliver webhook)
  │
  │ ⑦ 推送到飞书
  ▼
用户 (飞书收到完成通知)
```

---

## 1. 核心架构决策：Matea 在链路中的位置

### 1.1 原则重申

| 原则 | 含义 |
|------|------|
| Matea 不直连 IM | 不自研飞书/企微 SDK，不接 WebSocket/长轮询 |
| Matea 是 Gitea 唯一写方 | 评论/PR 只能由 Matea 写回 |
| Matea 无 IM 感知 | 不知道消息来自飞书还是 Slack，只认识 Gita webhook |

### 1.2 Matea 的边界

```
┌──────────────────────────────────────────────────────┐
│  Matea                                               │
│  ┌──────────────────────────────────────────────┐    │
│  │  入站：仅 Gitea webhook（issue event）        │    │
│  │  出站：写回 Gitea + deliver webhook 事件      │    │
│  └──────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────┘
          ▲                        │
          │ Gitea webhook          │ deliver outbound
     ┌────┴────�              �────▼────┐
     │  Gitea   │�────────────│  Hub    │
     │ (权威源) │  Hub 可读写  │ (IM网关) │
     └─────────�              └────┬────┘
                                   │ IM 双向
                              ┌────▼────┐
                              │  用户   │
                              │ (飞书等) │
                              └─────────┘
```

**关键洞察**：Matea 只和 Gitea + Hub 交互。Hub 是所有 IM 流量的唯一入口/出口。

---

## 2. IM 入站：用户请求如何到达 Matea

### 2.1 两种路径对比

| 路径 | 描述 | 优势 | 劣势 |
|------|------|------|------|
| **A. Hub → Gitea → Matea** | Hub 收到 IM 消息 → 调用 Gitea API 创建 Issue → Gitea webhook 自动触发 Matea | 零新 API；Matea 入站不变 | Hub 需直连 Gitea；链路多一跳 |
| **B. Hub → Matea → Gitea** | Hub 调用 Matea 的 REST 提交端点 → Matea 内部创建 Issue + 触发处理 | 原子操作；Hub 不依赖 Gitea | Matea 需暴露入站 API（新增面） |

### 2.2 推荐路径 A（Hub 通过 Gitea 间接触发 Matea）

**理由**：
1. Matea 入站保持仅为 Gitea webhook——**不变量不破**
2. Hermes/OpenCode 本来就需要直连 Gitea API（Hermes 自带 Gitea connector）
3. 用户在 IM 创建的 Issue 会出现在 Gitea 中，成为可审计的工作记录
4. Hub → Gitea webhook → Matea 是 Gitea 用户已经在用的模式（Issue event → webhook → Matea）

### 2.3 路径 A 的完整流程

```
用户在飞书说 "@matea 看看 ai-dev#12，让 analyst 分析"
  │
  ▼
Hub 解析消息：repo=ai-dev, issue=12, action=assign agent=analyst
  │
  ├─ 若 Issue #12 不存在 → Hub 调用 Gitea API 创建 Issue
  ├─ 若已存在 → 直接 @mention analyst（评论中写 "@matea-analyst 请分析"）
  │
  ▼
Gitea 触发 issue event webhook → Matea 收到
  │
  ▼
Matea 按既有流程处理（Runner → Agent Loop → 写评论）
  │
  ▼
Gitea Issue 出现分析评论 ✓
  │
  ├─ 同时触发 deliver outbound（task_completed 事件）
  │
  ▼
Hub 收到 deliver webhook → 推送到飞书群
  │
  ▼
用户在飞书看到 "matea-analyst 已完成分析：[摘要]"
```

### 2.4 何时需要路径 B（Matea 暴露 REST API）？

仅当以下场景出现时考虑：

| 场景 | 为何需要 REST API | 优先级 |
|------|-------------------|--------|
| 需要「即时响应」| 同步返回 taskId，Hub 可在 IM 中回复「任务已创建 #123」 | 低（可 Poll Gitea 评论确认） |
| 需要「权限隔离」| Hub 不想给 Matea 的 Gitea token 让 Hub 知道 | 中（Hub 直连 Gitea 也需 token） |
| 需要「非 Issue 触发」| 直接提交任务而不创建 Issue | 低（违背 Gitea-first） |

**结论**：Phase 2 不暴露 Matea REST 入站 API。路径 A 已覆盖核心场景。如有需要，Phase 3 再评估。

---

## 3. IM 出站：结果如何推送到 IM

### 3.1 唯一机制：deliver 出站 webhook

Phase 2 D5 已拍板 deliver 为出站扇出。这就是 IM 回传的通道：

```
Matea 任务完成
  │
  ▼
deliver 模块：POST {webhook_url}
{
  "event": "task_completed",
  "channel": "feishu",        // 或 wecom/slack（Hub 路由依据）
  "thread_id": "feishu-oc_xxx", // Hub 创建的会话 ID
  "repo": "org/ai-dev",
  "issue_id": 12,
  "pr_id": null,
  "action": "comment",
  "content": "分析结论：..."
}
  │
  ▼
Hub 接收 → 根据 channel + thread_id 推送到飞书
```

### 3.2 关键字段设计

| 字段 | 用途 | 来源 |
|------|------|------|
| `channel` | Hub 路由到哪个 IM 平台 | Agent 配置（Hub 侧决定） |
| `thread_id` | 回复到哪个会话/线程 | Hub 创建任务时生成，存入 TaskContext |
| `content` | 推送到 IM 的文本摘要 | Agent 输出截断或摘要 |
| `gitea_url` | Gitea 评论链接（用户可点击跳转） | Matea 构建 |

### 3.3 用户体验

```
飞书用户视角：
────────────────────────────────────────
[matea-bot] 任务 #task_1234 已完成
  📋 org/ai-dev#12
  � matea-analyst
  � 分析结论：该 issue 描述的是...
  🔗 查看完整评论：https://gitea.../issues/12#issuecomment-456
────────────────────────────────────────
```

**关键**：deliver 推送到 IM 的内容是「摘要 + 链接」，完整分析在 Gitea 评论中。

---

## 4. 完整场景映射

### 场景 1：IM 创建 Issue 并触发分析

```
[飞书群] 用户 A: "@matea 新建 issue 说 ai-dev 的登录页加载慢，标题'登录页性能问题'，让 analyst 分析"

[Hub] 1. 解析意图 → create_issue + assign analyst
      2. 调用 Gitea API 创建 Issue（Hub 用自有 token）
      3. Issue 创建后 webhook 触发 Matea

[Matea] 4. 按 analyze_issue 流程处理 → 写评论到 Issue

[飞书群] [matea-bot] Issue #45 已创建并分析完成：
          标题：登录页性能问题
          分析：经检查，可能的原因是...
          查看：https://gitea.../issues/45#issuecomment-789
```

### 场景 2：IM 查询已有 Issue 状态

```
[飞书群] 用户 B: "@matea ai-dev#12 现在什么状态？"

[Hub] 1. 查询 Matea MCP（若实现）或直接查 Gitea API
      2. 直接在飞书回复（不经 Matea 处理）

[飞书群] [matea-bot] Issue #12 当前状态：analyzing
          Agent: matea-analyst
          已运行: 3 分钟
          查看：https://gitea.../issues/12
```

**注意**：这是只读查询，Hub 直接查 Gitea 即可，不需要 Matea 参与。
但若需要「Matea 工作流状态」（是否排队、哪个 Runner），可通过 MCP Server（D4 可选）的 `matea_get_issue_status` 工具。

### 场景 3：PR 合并后飞书通知

```
[Gitea] PR #78 被合并
       ↓ webhook
[Matea] 识别 event = "pr_merged"
       ↓ 触发关联 Issue 的状态更新
       ↓ deliver outbound: {"event":"pr_merged","repo":"org/ai-dev","issue_id":12,"pr_id":78}
[Hub] 推送到飞书群

[飞书群] [matea-bot] 🎉 PR #78 已合并，关联 Issue #12 关闭
          查看：https://gitea.../pulls/78
```

### 场景 4：Agent 请求用户反馈（多轮）

```
[Matea] analyst 分析中发现信息不足，写 Gitea 评论：
        "@用户A 能否提供复现步骤？"
       ↓ deliver outbound: {"event":"agent_question","content":"能否提供复现步骤？"}

[飞书群] [matea-bot] � matea-analyst 提问 (ai-dev#12)：
          "@用户A 能否提供复现步骤？"
          回复此消息将作为评论添加到 Issue
```

**注意**：要实现「用户在飞书回复 → 同步到 Gitea 评论」，需要 Hub 的 IM 双向能力。这是 **Hub 侧实现**，Matea 只需 deliver 事件 + 在 Gitea 评论中留下占位符。

---

## 5. 架构建议汇总

### 5.1 Matea 侧（Phase 2 交付）

| 组件 | 作用 | 在 Phase 2 中 |
|------|------|--------------|
| deliver 出站 | 任务完成后 POST 事件到 Hub | **必做**（D5） |
| Gitea webhook 入站 | 接收 Issue 创建/评论事件 | **既有** |
| Gitea 写回 | 评论/PR 落地 | **既有** |
| MCP Server（可选） | 让 Hub 查询 Matea 状态 | **可选**（D4） |

**Matea 不需要新的入站 API**。所有 IM → Matea 的流量通过 Gitea webhook 中转。

### 5.2 Hub 侧（Hermes 或自建 bridge）

| 能力 | 说明 | 谁负责 |
|------|------|--------|
| IM 入站 | 接收飞书/企微消息 | Hermes 自带（原生支持） |
| IM 出站 | 推送到飞书/企微 | Hermes 自带（deliver feishu） |
| Gitea 读写 | 创建 Issue、写评论 | Hub 需直连 Gitea API |
| 调用 Matea | 通过 Gitea webhook 间接触发 | Hub 创建 Issue → webhook → Matea |
| 接收 Matea deliver | 消费 webhook_url 事件 | Hub 暴露接收端点 |

### 5.3 配置示例（用户视角）

```yaml
# Matea 配置 (Matea 侧)
deliver:
  webhook_url: "https://hermes.example.com/api/deliver"  # Hub 接收端点

agents:
  - name: matea-analyst
    role: analyze
    backend: builtin
    # 新增可选字段：deliver_channel（通知到哪个渠道）
    deliver:
      channel: feishu
      thread_prefix: "matea-ai-dev-"  # 飞书群话题前缀
```

---

## 6. 进阶场景：何时需要 MCP Server？

### 6.1 MCP 的价值定位

| 功能 | 无 MCP（Hub 直连 Gitea） | 有 MCP |
|------|--------------------------|--------|
| 创建 Issue | Hub 调 Gitea API | 相同 |
| 写评论 | Hub 调 Gitea API | 相同 |
| 查询 Issue 内容 | Hub 调 Gitea API | 相同 |
| **查询 Matea 工作流状态** | ❌ 无法实现 | ✅ `matea_get_issue_status` |
| **触发非 Issue 任务** | ❌ 只能创建 Issue | ✅ `matea_assign_agent` |
| **重置 Session** | ❌ 无接口 | ✅ `matea_reset_session` |

### 6.2 推荐：MCP 作为「高级集成」而非必需

**Phase 2 最小可用（无 MCP）**：
```
Hub → 创建 Gitea Issue → webhook → Matea 处理 → deliver → Hub 推送到 IM
```
覆盖 90% 场景（创建 Issue、分析、审查、编码、结果通知）。

**Phase 2.5/3 增强（有 MCP）**：
```
Hub → 通过 MCP 查询 Matea 状态 → 在 IM 中展示「队列中/处理中/已完成」
Hub → 通过 MCP 触发即时任务（不创建 Issue）→ Matea 处理 → deliver
```

### 6.3 MCP 工具清单（面向 IM 场景扩展）

若实现 MCP Server，除了 D4 的 4 个工具，可增加 IM 专用工具：

| MCP 工具 | 用途 | 优先级 |
|----------|------|--------|
| `matea_get_issue_status` | 查询 Issue 在 Matea 中的工作流状态 | 高（IM 状态查询必需） |
| `matea_assign_agent` | 分配 Agent 处理 Issue | 高（IM 触发必需） |
| `matea_list_agents` | 列出可用 Agent | 中（IM 帮助信息） |
| `matea_post_comment` | 在 Gitea 写评论 | 中（IM 回复同步） |
| `matea_create_issue` | 在 Gitea 创建 Issue | 低（Hub 可直接调 Gitea） |
| `matea_reset_session` | 重置工作流状态 | 低（高危操作） |

---

## 7. 产品边界：明确不做

| 不做项 | 理由 |
|--------|------|
| Matea 直连 IM SDK | 违反「不自研 SDK」铁律 |
| Matea 暴露 IM 入站 WebSocket | Hub 是唯一 IM 网关 |
| Matea 存储 IM 用户身份 | Hub 负责用户映射，Matea 只认 Gitea 用户 |
| Matea 实现 IM 消息渲染 | 纯文本 + 链接足够，富文本卡片是 Hub 的事 |
| IM ↔ Gitea 双向实时同步 | Hub 负责同步，Matea 只输出事件 |

---

## 8. 结论与建议

### 8.1 一句话方向

> **IM 渠道 = Hub 的入站（IM 消息接收 + Gitea API 调用）+ Matea 的出站（deliver webhook）**
> 
> Matea 不感知 IM，只通过 deliver 事件通知 Hub，由 Hub 负责推送到飞书/企微/Slack。

### 8.2 Phase 2 交付物

| 交付 | 说明 | 工作量 |
|------|------|--------|
| deliver 出站模块 | POST 事件到 `deliver.webhook_url` | 已在 D5 计划中 |
| TaskContext.Channel/ThreadID | 任务携带 IM 通道/会话标识 | 2 行字段 + 传递逻辑 |
| deliver 事件丰富 | 增加 `agent_name` / `gitea_url` / `summary` | ~0.5 人日 |

### 8.3 不新增 Matea 入站 API

通过 Gitea webhook 中转已覆盖核心场景。IM → Gitea → Matea 的链路零新增 Matea 代码。

### 8.4 MCP Server 重新定位

从「可选增强」调整为「IM 高级集成推荐」——无 MCP 时 90% 场景可用，有 MCP 时支持状态查询和即时触发。

### 8.5 推荐决策点 D12（IM 渠道集成）

> **D12 ｜ IM 渠道集成模式**
> - **A（推荐）Hub 通过 Gitea 间接触发 + Matea deliver 出站** — Matea 零新增入站 API
> - B 新增 Matea REST 入站 API（任务提交端点）— 功能更强但扩大 Matea 边界
> - C 双向实时同步 — 过度设计
>
> ✅ 推荐 A。理由：符合 Matea 入站仅为 Gitea webhook 的不变量；Hub 触发链路已在 Gitea 用户场景验证。
>
> 待拍板：是否同意路径 A？
