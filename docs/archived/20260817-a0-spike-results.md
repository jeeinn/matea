# A0 前置验证 spike 结果（2026-08-17）

> 关联：[20260815-git-sync-3phase-plan.md](20260815-git-sync-3phase-plan.md) 阶段 A0  
> 结论：**A0.1 ✅ 通过 / A0.2 ✅ 通过 → A0.3 决策：OpenCode 作为阶段 A pilot**  
> 环境：Windows 11 + Docker（gitea/gitea:1.22 → Gitea 1.22.6）+ OpenCode 1.18.4 + 脚本化 mock LLM（[scripts/spike/mock_llm.py](../scripts/spike/mock_llm.py)）

---

## A0.2 Gitea deploy key API spike ✅

对真实 Gitea 1.22.6（docker）用 admin/repo-scope token 逐项实测：

| 验证项 | 结果 |
|---|---|
| `POST /api/v1/repos/{owner}/{repo}/keys` 创建 rw key | ✅ 201，响应含 `id`/`fingerprint`/`read_only:false` |
| rw key 实测 clone + push（SSH，`ssh://git@localhost:12222/owner/repo.git`） | ✅ 均可 |
| `read_only:true` key：clone | ✅ |
| `read_only:true` key：push | ✅ 被拒，服务端明确报 `Deploy Key: 2:ro-key is not authorized to write to owner/repo` |
| `GET .../keys` 列表 | ✅ 含 id/title/fingerprint/read_only |
| `DELETE .../keys/{id}` 回收 | ✅ 204，**立即生效**（回收后 ls-remote 即失败） |
| DELETE 幂等性（回收重试策略前提） | ✅ 删除不存在的 id 仍返回 204 → **回收可安全重试，无需先查存在性** |
| 最小 token scope | ✅ **`write:repository` 即可**管理 deploy keys，**不需要 admin** |
| 重复 key 材料 | 422 `This key has already been added to this repository` → 每任务必须生成全新密钥对（与方案一致） |
| key title 格式 `matea-hub-task-{taskID}` | ✅ 无限制 |

**对设计的落地确认**：
- A6 的凭据生命周期可用 `POST/DELETE keys` 实现；回收失败重试策略简单（DELETE 幂等）。
- Matea 日常 token 只需 `write:repository` scope，**无需 admin token 即可签发 deploy key**（比计划假设的权限更小，利好最小权限）。
- 回收定位：创建响应的 `id` 应持久化到 `hub_handles`（A2 加列），回收时直接 `DELETE {id}`；无需按 title 搜索。

## A0.1 OpenCode git 能力 spike ✅

用**真实 OpenCode 1.18.4**（`opencode serve`）+ 脚本化 mock LLM（OpenAI 兼容、按剧本发 bash 工具调用）驱动，复现 Matea 的 API 调用面（`POST /session` + `POST /session/{id}/message`，`X-Opencode-Directory` 头）：

| 验证项 | 结果 |
|---|---|
| HTTP API 传入 clone_url + 凭据 | ✅ 经 prompt part 注入（base64 单行携带私钥，防 LLM 转述错版） |
| 凭据落盘 + 校验 | ✅ `base64 -d > task_key && chmod 600 && ssh-keygen -y` 正确还原公钥 |
| `git clone`（SSH 带凭据，`GIT_SSH_COMMAND`） | ✅ |
| `git checkout -b matea/hub-200` 草稿分支 | ✅ |
| `git commit` 带 footer `matea-task-id: 200` | ✅ commit `c32cccde` |
| `git push` 草稿分支（持 deploy key 自 push） | ✅ Gitea 远端 `matea/hub-200` HEAD == `c32cccde`，消息含 footer |
| bash 工具默认状态（1.18.4） | ✅ **未在 `tools` 白名单中显式启用也会执行**（Matea 现有 `tools:{search,read,write,edit}` 配置不妨碍 git_sync） |
| 无人值守权限 | ✅ workspace `opencode.json` 写 `permission:{bash,edit,read,write: allow}` 后全程无交互 |

### 发现的坑（进 A1/A4 前必须处理）

1. **Windows 路径语义**：`X-Opencode-Directory` 传 POSIX 路径（`/tmp/...`）会被 opencode 按当前盘符解析成 `X:\tmp\...` 并在**首次 message 时** 500（session 创建不校验目录）。→ Matea `Prepare` 必须传原生路径并先 stat 校验。
2. **同步 POST 的可靠性**：sync `POST /message` 在工具轮次间可能挂起（本次为 mock LLM 的 SSE 细节所致，真实 provider 无碍）。→ Matea 现有「POST + 轮询 GET /message」的读路径是正确姿势，写路径沿用即可；**不要把同步 POST 的返回当作唯一完成信号**。
3. **私钥经 prompt 注入 ⇒ 会过境 LLM provider**。任务级短期 deploy key 的设计已消化该风险；B 阶段可在 Hermes/OpenCode 侧评估更隐蔽的注入位（env / config 预置）。
4. 无人值守需要 `permission.*=allow` 配置（本次放在 workspace `opencode.json`）。→ A4 的 `Prepare` 需在 GitSyncInfo/文档中给出该配置要求。
5. opencode 内置 zen provider 无支付不可用于 CI——与 Matea 无关（用户自配 provider），仅记录。

## A0.3 决策：OpenCode 作为阶段 A pilot ✅

A0.1 通过 → 按预案 **OpenCode 当 pilot**，阶段 A（A1–A8）按 TASKS.md 顺序推进；Hermes 在阶段 B 对齐同一契约。

## 复现

```bash
# Gitea（一次性容器）
docker run -d --name gitea-spike -p 13000:3000 -p 12222:22 \
  -e GITEA__security__INSTALL_LOCK=true \
  -e GITEA__server__ROOT_URL=http://localhost:13000/ \
  -e GITEA__server__SSH_PORT=12222 gitea/gitea:1.22
# mock LLM（剧本驱动真实 OpenCode）
python scripts/spike/mock_llm.py --port 18080 --script script.json
opencode serve --port 14096 --hostname 127.0.0.1   # workspace 内置 opencode.json（provider+permission）
```

spike 现场（容器/密钥/工作区）已按一次性原则清理；证据即本文表格与 [scripts/spike/](../scripts/spike/)。
