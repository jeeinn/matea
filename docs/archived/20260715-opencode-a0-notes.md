# OpenCode A0 — 工作目录与 API 字段笔记

> **状态：已归档（2026-07-23）**  
> OpenCode directory 字段笔记（已实现）；参考代码 OpenCodeHTTPBackend  
> 现行文档入口：[../TASKS.md](../TASKS.md) · [../ARCHITECTURE.md](../ARCHITECTURE.md) · [../DEPLOYMENT.md](../DEPLOYMENT.md)

---
> 更新：2026-07-15  
> 状态：公开文档推导 + 代码已对齐；**本机 `opencode serve` 端到端 PoC 仍待人工跑通验收**  
> 关联：[todo-20260714-opencode-path-a.md](20260714-todo-opencode-path-a.md) · [server-runtime-design-v4.md](20260714-server-runtime-design-v4.md)

---

## 结论（Gateway 侧实现依据）

OpenCode `serve` 用 **请求级 directory** 绑定项目实例，而不是仅靠 message body：

| 方式 | 说明 |
|------|------|
| Query `?directory=/abs/path` | 推荐；`POST /session` / 多数路由均可用 |
| Header `X-Opencode-Directory: /abs/path` | 与 query 等价的另一通道 |
| Message body `directory` | SDK/早期草稿常见；**不足以保证 session 落在 Gateway workspace** |

Gateway `OpenCodeHTTPBackend.createSession` 在 `WorkDir` 非空时同时设置 **query + header**；`sendMessage` 仍附带 body `directory` 作为冗余。

## 本机 PoC 清单（人工）

```bash
# 1. 启动 sidecar（端口与 config agents.backends 一致）
opencode serve --port 4096

# 2. 准备临时 git 仓
mkdir -p /tmp/oc-poc && cd /tmp/oc-poc && git init && echo 'hello' > README.md && git add . && git commit -m init

# 3. 创建 session（确认响应里 directory / 后续写文件路径）
curl -sS -X POST "http://127.0.0.1:4096/session?directory=/tmp/oc-poc" \
  -H "Content-Type: application/json" \
  -H "X-Opencode-Directory: /tmp/oc-poc" \
  -d '{"title":"gateway-poc"}'

# 4. 发 message（provider/model 按本机 OpenCode 已配清单）
# 5. ls /tmp/oc-poc — 确认改动落在 Gateway 将传入的同一绝对路径
```

验收：改码文件出现在 **Gateway `prepareWriteWorkspace` 产出的绝对路径**，而非 sidecar 启动 cwd。

## 健康检查

- 默认 `GET {base_url}{health_check.path}`，常见 `/health` 或 `/global/health`
- Gateway：health **先于** clone/分支；失败 → 任务 `failed`（除非 `allow_fallback_internal: true`）

## 参考

- [OpenCode Server](https://opencode.ai/docs/server/)
- Directory routing：`?directory=` / `X-Opencode-Directory`（社区文档与 PR #21131）
