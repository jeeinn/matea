# scripts/spike — A0/A8 验证 rig

一次性验证脚本（非业务代码）。A0 证据见 [docs/20260817-a0-spike-results.md](../../docs/20260817-a0-spike-results.md)，A8 验收证据见 [docs/20260817-a8-acceptance.md](../../docs/20260817-a8-acceptance.md)。

- `mock_llm.py` — 脚本化 OpenAI 兼容 LLM。向**真实 OpenCode** 依次下发 bash 工具调用（或本地代执行），
  用于在无付费 LLM 的情况下端到端验证 git_sync 凭据注入与 clone/commit/push 机制。
  步进方式：无状态，按会话历史中 `role=tool` 消息数推进。
- `a8-script.json` — A8 剧本：经 `captures` 正则从 Matea 注入的 prompt 动态提取
  base64 私钥 / clone URL / 草稿分支 / footer，步骤命令模板化替换（`{VAR}`），
  因此每任务全新 deploy key 无需改剧本。

用法：

```bash
python mock_llm.py --port 18080 --script a8-script.json [--mode tools|local] [--workdir DIR] [--chunk-delay-ms N]
# script.json: {"sentinel": "EXECUTE GIT TASK",
#               "captures": {"VAR": "regex-with-one-group"},
#               "steps": [{"command": "...{VAR}..."}...],
#               "final": "done text {VAR}"}
```

- `--mode tools`（默认）：把步骤作为 bash 工具调用发给 OpenCode 执行（A8 验收所用路径）。
- `--mode local`：mock 进程本地代执行（`--workdir` 指定工作区），然后直接回 final 文本；调试/兜底用。
  ⚠️ 本地模式必须用 Git bash 全路径（代码内已处理）：裸 `bash` 在原生 Windows PATH 下命中 WSL，
  DrvFS 上 `chmod` 无效，OpenSSH 会以 0777 拒收私钥。

opencode 侧 workspace 需含 `opencode.json`：定义指向 mock 的 `openai-compatible` provider +
`permission: {bash, edit, read, write: allow}`（无人值守）。

**SSE 硬知识（血泪）**：响应**不要**带 `Connection: keep-alive`——无 Content-Length 的 HTTP/1.0
响应靠连接关闭作终止符；keep-alive 会让客户端永远等不到流结束，ai-sdk 的 finish 回调不触发，
OpenCode agent loop 在首个工具调用后卡死（A0 观察到的「同步 POST 挂起」根因）。

## A8 复现配方（要点）

1. Gitea：`docker run -d --name gitea-a8 -p 13000:3000 -p 12222:22 -e GITEA__security__INSTALL_LOCK=true -e GITEA__server__ROOT_URL=http://localhost:13000/ -e GITEA__server__SSH_PORT=12222 gitea/gitea:1.22`
   （CLI 建用户须 `docker exec -u git`；token scope：`write:repository,write:issue,write:user,read:user`）
2. OpenCode：`opencode serve --port 14096`（cwd = 含 opencode.json 的工作区）。
3. Matea：`agents.backends.<name>.workspace_transport: git_sync` + `type: hub-opencode`；
   agent 行 `backend` 指向该 name，**创建时**带 `gitea_token`（updateAgent 不支持改 token）。
4. 触发：构造 `issues/assigned` webhook（X-Gitea-Signature = HMAC-SHA256(secret, body)），
   assignee.login = agent 的 `gitea_username`。
