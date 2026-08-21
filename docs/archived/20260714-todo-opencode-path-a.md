# TODO: OpenCode Path A 接入

> **状态：已归档（2026-07-23）**  
> Path A（A0–A4）已交付；A+ 见 ../TASKS.md  
> 现行文档入口：[../TASKS.md](../TASKS.md) · [../ARCHITECTURE.md](../ARCHITECTURE.md) · [../DEPLOYMENT.md](../DEPLOYMENT.md)

---
> 状态：**Path A 主路径已交付**（A0–A4）；A+ 为可选后续  
> 创建日期：2026-07-14  
> 更新：2026-07-17  
> 设计依据：[server-runtime-design-v4.md](20260714-server-runtime-design-v4.md)  
> 字段笔记：[opencode-a0-notes.md](20260715-opencode-a0-notes.md)  
> E2E：[20260716-e2e-test-report.md](20260716-e2e-test-report.md)（E1/E6/E10 PASS）

---

## 产品约束（勿偏离）

1. 默认 `backend=internal`，Analyze / Review 强制 internal  
2. OpenCode = **本机 sidecar**（`opencode serve` HTTP/OpenAPI）；CLI `run` 仅降级  
3. Gateway 管编排与 Git 写回；OpenCode 管改码  
4. **不做**远程 OpenCode、完整 Path B worktree、多 Git 托管抽象（见平台策略归档）

---

## 实施清单

### A0 — PoC ✅

- [x] 记录实际请求字段 → [opencode-a0-notes.md](20260715-opencode-a0-notes.md)
- [x] 本机 `opencode serve` 端到端改码落在 Gateway workspace（E1+E6）

### A1 — 配置与存储 ✅

- [x] `AgentBackendsConfig` / `BackendConfig`
- [x] 默认 `internal`；`config.example.yaml` 示例 `opencode-local`
- [x] Agent：`Backend`、`BackendOptions` + migration + API

### A2 — 抽取 write helpers ✅

- [x] `prepareWriteWorkspace` / `finalizeWriteChanges`
- [x] `runWriteTask` + `InternalCodingBackend`

### A3 — OpenCode HTTP Backend ✅

- [x] `CodingBackend` + `OpenCodeHTTPBackend`
- [x] 写任务按 backend；非写强制 internal
- [x] health 失败 → `failed`；可选 `allow_fallback_internal`
- [x] `?directory=` + `X-Opencode-Directory`

### A4 — 测试与运维 ✅（主路径）

- [x] httptest mock OpenCode
- [x] ARCHITECTURE + sidecar 运维说明
- [x] WebUI：Agent backend 下拉
- [ ] 集成：mock server + 假仓库（可选加强，非阻塞）

### A+ — 可选后续

- [ ] SSE 进度 → Issue 评论或 task progress
- [ ] 持久化 `opencode_session_id`
- [x] `allow_fallback_internal`
- [ ] Claude PrintBackend（契约型 CLI，非盲扫）

---

## 验收（Path A Done）

1. ✅ 默认 internal 行为  
2. ✅ coder + sidecar → Issue→改码→PR（E6）  
3. ✅ sidecar 宕机可读失败（E10）  
4. ✅ Analyze/Review 不走 OpenCode（E8）  
5. ⏳ Session 续 OpenCode session（A+）  
6. ✅ 文档说明 sidecar 启动  

---

## 明确不做

- 远程 `opencode serve` / 跨机 workspace  
- Path B bare mirror + worktree 基础设施  
