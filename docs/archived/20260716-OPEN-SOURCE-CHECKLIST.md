# 开源准备清单

> **状态：已归档（2026-07-23）**  
> 开源准备清单已全部勾选（v0.10.0 已发布）；现行 backlog 见 ../TASKS.md  
> 现行文档入口：[../TASKS.md](../TASKS.md) · [../ARCHITECTURE.md](../ARCHITECTURE.md) · [../DEPLOYMENT.md](../DEPLOYMENT.md)

---
> 更新：2026-07-17  
> 对应任务：[TASKS.md](../TASKS.md) §P3  
> 背景：P0–P2 核心演进已交付（见 [archived/20260716-TASKS.md](20260716-TASKS.md)、[20260716-e2e-test-report.md](20260716-e2e-test-report.md)）

本文跟踪**首次公开发布**前必须完成（阻塞）与强烈建议完成（质量加固）的事项。

---

## 开源阻塞（首发前必须）

| # | 项 | 说明 | 验收 |
|---|-----|------|------|
| B1 | **LICENSE 文件** | README 声明 MIT，仓库需有对应 `LICENSE` | 根目录存在 MIT `LICENSE` |
| B2 | **CI** | 无 `.github/workflows` | PR/主分支自动跑 `go test ./... -count=1` + `go vet ./...` |
| B3 | **占位 URL/组织名** | `DEPLOYMENT.md` 等含 `your-org` 占位 | 换成真实仓库地址或通用占位说明 |
| B4 | **安全默认值警示** | 默认 `admin` / `admin123`、`jwt_secret` | README + DEPLOYMENT 强调**首次登录必改密码**、生产必换密钥 |
| B5 | **敏感配置隔离** | `config.yaml` 已在 `.gitignore` | 提供完整 `config.example.yaml`；可选 `.env.example`；文档说明勿提交 token |
| B6 | **文档入口统一** | 新用户不知从何上手 | README 增加「5 分钟 Mock 测试」路径 + 完整 Gitea 联调链接 |

### 勾选清单

- [x] B1 LICENSE 文件
- [x] B2 CI workflow
- [x] B3 占位 URL 清理
- [x] B4 安全默认值文档
- [x] B5 敏感配置与示例文件
- [x] B6 README 快速上手路径

---

## 强烈建议（首发前质量加固）

| # | 项 | 说明 | 验收 |
|---|-----|------|------|
| S1 | **v2 联调 Sign-off** | E2E 已覆盖 analyze/coder/review/writeback，缺 Merge→`done` | 补 E2E 场景或手工报告；对照 [archived/20260709-v2-gitea-integration-checklist.md](20260709-v2-gitea-integration-checklist.md) §2.4 |
| S2 | **Session / Sandbox `base_dir` 对齐** | P0.3 已知缺口：`workspace.base_dir` vs `sandbox.base_dir` | 统一路径约定；Session 与 task 级 workspace 行为一致 |
| S3 | **`loop_config` 参数校验** | sandbox-roadmap 14.5.8 未做 | 启动时校验 `max_iterations`（1–100）、`timeout` 范围；非法值报错 |
| S4 | **Dockerfile 落地或删文档** | 短期不做 compose/K8s；文档已澄清 | **文档澄清：示例暂未提供**（DEPLOYMENT 以二进制 + systemd 为主） |
| S5 | **Linux E2E 脚本** | `scripts/TESTING.md` 写明 Linux E2E 待补 | 提供 bash 版 setup/scenarios，或 README 说明当前以 Windows PS1 为主 |
| S6 | **文档同步** | 子文档落后于代码 | 更新 `sandbox-roadmap.md`、`todo-20260714-opencode-path-a.md`、内部能力验收状态 |
| S7 | **CONTRIBUTING + SECURITY** | 开源惯例 | `CONTRIBUTING.md`（贡献流程）、`SECURITY.md`（漏洞报告） |
| S8 | **首版 Release** | CHANGELOG 有 Unreleased | 打 `v0.10.0` tag（跳过已有 `v0.2`–`v0.7.0`）；附预编译二进制 |

### 勾选清单

- [x] S1 Merge→done 联调 Sign-off（Mock + E13 脚本；见 [20260717-v2-merge-signoff.md](20260717-v2-merge-signoff.md)）
- [x] S2 Session/Sandbox base_dir 对齐
- [x] S3 loop_config 参数校验
- [x] S4 Dockerfile：**文档澄清：示例暂未提供**（短期不做；见 [DEPLOYMENT.md](../DEPLOYMENT.md)#容器部署暂未提供）
- [x] S5 Linux E2E 或说明（`e2e-smoke.sh` + TESTING：完整 E2E 以 Windows/pwsh 为主）
- [x] S6 子文档状态同步
- [x] S7 CONTRIBUTING + SECURITY
- [x] S8 v0.10.0 Release **已发布**（tag `v0.10.0` 已推送；5 平台二进制已上传：https://github.com/jeeinn/ai-dev/releases/tag/v0.10.0）

---

## 建议节奏

| 阶段 | 焦点 | 对应项 |
|------|------|--------|
| 第 1 周 | 可公开最小集 | B1–B6 + S7 |
| 第 2 周 | 质量加固 | S1–S6 + S8 |
| 之后 | 社区驱动 backlog | 见 TASKS.md P1 遗留 / P2 / P4 |

---

## 相关文档

| 文档 | 用途 |
|------|------|
| [TASKS.md](../TASKS.md) | 现行任务（含 P3 摘要） |
| [archived/20260716-TASKS.md](20260716-TASKS.md) | P0–P2 完整交付记录 |
| [20260716-e2e-test-report.md](20260716-e2e-test-report.md) | 本机 E2E 13/13 PASS |
| [DEPLOYMENT.md](../DEPLOYMENT.md) | 部署说明（短期以二进制 + systemd 为主；容器示例暂未提供） |
| [scripts/TESTING.md](../../scripts/TESTING.md) | 测试与 E2E 复现 |
| [20260717-v2-merge-signoff.md](20260717-v2-merge-signoff.md) | S1 Merge→done |
| [RELEASE-v0.10.0.md](20260717-RELEASE-v0.10.0.md) | v0.10.0 tag / 二进制步骤 |
