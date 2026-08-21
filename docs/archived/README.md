# 归档文档

命名：`{YYYYMMDD}-{原文件名}`，日期取文档创建时间（git 首次提交日；无记录则用文内日期）。

本目录存放**已实现、已过时或已确认冻结**的设计 / 任务 / 决策 / 点时报告。现行文档在 [`../`](../)：

| 现行 | 用途 |
|------|------|
| [`../ARCHITECTURE.md`](../ARCHITECTURE.md) | 技术架构 |
| [`../DEPLOYMENT.md`](../DEPLOYMENT.md) | 部署指南 |
| [`../TASKS.md`](../TASKS.md) | 任务清单与按需 backlog |
| [`../AGENTS.md`](../AGENTS.md) | Agent 配置与 role 说明 |
| [`../HUB-BACKENDS.md`](../HUB-BACKENDS.md) | Hub 后端权威文档（git_sync 信任模型 / 契约 / SLO） |
| [`../remote-hub-deployment-flow.md`](../remote-hub-deployment-flow.md) | 远端 Hub 部署流程 |
| [`../todo-20260714-LLMProvider-可选增强.md`](../todo-20260714-LLMProvider-可选增强.md) | LLM 剩余可选增强 |

---

## 索引

| 文件 | 说明 |
|------|------|
| `20260714-server-runtime-design-v4.md` | OpenCode / CodingBackend 设计（shared_path 时代；权威已由 ../HUB-BACKENDS.md 取代） |
| `20260723-RENAME-TO-MATEA.md` | 项目更名实施梳理（已实施完成） |
| `20260803-规划评审-Hub演进与Agent简化方案.md` | Hub 演进评审（结论已并入产品演进计划） |
| `20260803-TASKS.md` | v0.11.4 任务清单 |
| `20260804-Phase1.5-plan.md` 等 Phase1.x | Phase 1.5 计划与各阶段 code review（已收官） |
| `20260805-Phase2-plan.md` | Phase 2 实施方案（D1–D12，已执行完毕） |
| `20260805-Phase2-evolution-direction.md` | Phase 2 演进方向讨论 |
| `20260808-IM-channel-integration-analysis.md` | IM 渠道接入分析（结论：不自研 SDK） |
| `20260811-Phase2-code-review.md` / `20260813-Phase2-progress-review.md` / `20260813-Phase2-e2e-report.md` / `20260814-Phase2-webui-e2e.md` | Phase 2 过程评审与 E2E 报告（点时） |
| `20260814-TASKS.md` | Phase 1 + Phase 2 已完成部分任务归档 |
| `20260814-CONFIG-AUTOMATION.md` | 配置自动化细化方案（Phase 2.5，P0–P2 已全部落地） |
| `20260814-hub-git-sync-plan.md` | git_sync 初版计划（已被 v3.1 三阶段方案取代） |
| `20260815-git-sync-3phase-plan.md` / `20260815-git-sync-evaluation.md` | git_sync v3.1 三阶段方案与评估（Phase 2.6 已全量落地） |
| `20260817-a0-spike-results.md` / `20260817-a8-acceptance.md` | A0 前置验证实测 / A8 阶段 A 验收（真实 OpenCode+Gitea） |
| `20260820-TASKS.md` | Phase 2.5 + Phase 2.6 完整交付记录 |
| `20260820-phase2.6-review.md` | Phase 2.6 审核报告（22 项勾选核实，偏差已修正） |
| `20260820-phase2.5-p2-review.md` | Phase 2.5 P2 评审与修复记录（R1–R8 + D 项，全部修复） |
| `20260531-gitea-ai-agent-design.md` | 早期产品设计（已被 ARCHITECTURE / v2 工作流取代） |
| `20260531-webhook-gateway-design.md` | 早期 Gateway 详细设计（已实现） |
| `20260531-llm-prompt-design.md` | 早期 Prompt/Provider 设计（接口以代码为准） |
| `20260601-agent-development-decisions.md` | 早期开发决策与竞品笔记 |
| `20260603-web-ui-design.md` | Web UI 设计方案（已实现） |
| `20260604-TASKS.md` | Phase 1–13 任务归档 |
| `20260604-sandbox-roadmap.md` | 沙箱增强路线图（核心已交付；可选见 ../TASKS） |
| `20260605-TASKS.md` | Phase 15 任务归档 |
| `20260605-e2e-test-report.md` | 早期 E2E 测试报告 |
| `20260615-trigger-rules-and-workflow-improvement.md` | Assign 工作流 v2 设计（已交付） |
| `20260616-assign-workflow-progress.md` | v2 完成总览 |
| `20260616-TASKS.md` | Phase 16–19 任务归档 |
| `20260709-v2-gitea-integration-checklist.md` | v2 联调清单（历史） |
| `20260710-LLMProvider模型选择与Token配置扩展方案.md` | LLM 模型/Token 主方案（已实施） |
| `20260710-opencode-integration.md` | OpenCode 早期 CLI 方案（已废弃） |
| `20260714-server-runtime-design*.md` | 运行时设计 v1–v3（v4 见上方 `20260714-server-runtime-design-v4.md`） |
| `20260714-coding-gateway-multi-vcs.md` | **平台策略：Gitea 优先**（已确认冻结） |
| `20260714-TASKS.md` | 2026-07-14 前分散 backlog |
| `20260714-internal-capabilities-toolpack-mcp-skills.md` | ToolPack/MCP/Skills/Analyze 设计（已落地） |
| `20260714-todo-opencode-path-a.md` | OpenCode Path A 清单（A0–A4 已交付；A+ 见 ../TASKS） |
| `20260715-opencode-a0-notes.md` | OpenCode directory 字段笔记（已实现） |
| `20260716-TASKS.md` | **P0–P2 核心演进**完整交付记录 |
| `20260716-OPEN-SOURCE-CHECKLIST.md` | 开源准备清单（全部完成，v0.10.0 已发布） |
| `20260716-e2e-test-report.md` | 本机全量 E2E 报告（点时） |
| `20260717-v2-merge-signoff.md` | S1 Merge→done 签核 |
| `20260717-RELEASE-v0.10.0.md` | v0.10.0 发布步骤（已执行） |
| `20260722-architecture-evaluation.md` | 架构深度评估（点时；P1 后续已硬化） |
| `20260723-TASKS.md` | **P3 开源 + 开源后加固**完整交付记录 |
