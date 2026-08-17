# scripts/spike — A0 前置验证 rig

一次性验证脚本（非业务代码），证据与结论见 [docs/20260817-a0-spike-results.md](../../docs/20260817-a0-spike-results.md)。

- `mock_llm.py` — 脚本化 OpenAI 兼容 LLM。按 `script.json` 的剧本向**真实 OpenCode** 依次下发 bash 工具调用，
  用于在无付费 LLM 的情况下端到端验证 git_sync 凭据注入与 clone/commit/push 机制。
  步进方式：无状态，按会话历史中 `role=tool` 消息数推进。

用法：

```bash
python mock_llm.py --port 18080 --script script.json
# script.json: {"sentinel": "EXECUTE GIT TASK",
#               "steps": [{"command": "..."}...],
#               "final": "done text"}
```

opencode 侧 workspace 需含 `opencode.json`：定义指向 mock 的 `openai-compatible` provider +
`permission: {bash, edit, read, write: allow}`（无人值守）。
