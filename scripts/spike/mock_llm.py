#!/usr/bin/env python3
"""mock_llm.py — A0.1 spike rig: scripted OpenAI-compatible LLM for OpenCode.

Drives a REAL OpenCode server through a deterministic bash script so the
git_sync credential-injection mechanics (GIT_SSH_COMMAND, clone/commit/push)
can be verified end-to-end without a paid LLM.

Protocol notes:
  - OpenCode talks OpenAI chat-completions (stream + non-stream).
  - The script position is derived STATELESSLY from the request: opencode
    appends one `role=tool` message per executed tool call, so
    step = number of tool messages in the conversation.
  - Requests without the sentinel (e.g. title generation) get a trivial
    text reply.

Usage: python mock_llm.py --port 18080 --script script.json
script.json: {"sentinel": "EXECUTE GIT TASK", "steps": [{"command": "..."} ...],
              "final": "text emitted after the last step",
              "captures": {"VAR": "regex-with-one-group", ...}  (optional)}

Captures (A8): each regex is applied to the FULL transcript (user messages AND
tool results, in order); the first match's group(1) is bound to VAR. Step
commands and the final text may reference {VAR}; substitution happens just
before the reply is emitted, so a VAR produced by a tool result (e.g. the
40-char sha printed by a `git rev-parse HEAD` step) is available in "final".
A missing capture substitutes empty and prints a loud warning (the resulting
command will fail in the sandbox, which is the desired visible failure).
"""
import argparse
import json
import re
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def chat_id():
    return "chatcmpl-" + uuid.uuid4().hex[:12]


def sse_chunk(payload):
    return f"data: {json.dumps(payload)}\n\n".encode()


class QuietHTTPServer(ThreadingHTTPServer):
    def handle_error(self, request, client_address):  # suppress reset noise
        pass


def _chunk(rid, delta, finish=None):
    return {"id": rid, "object": "chat.completion.chunk", "created": int(time.time()),
            "model": "spike-model",
            "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}


def _usage_chunk(rid):
    return {"id": rid, "object": "chat.completion.chunk", "created": int(time.time()),
            "model": "spike-model", "choices": [],
            "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}}


class Handler(BaseHTTPRequestHandler):
    script = None  # {"sentinel": str, "steps": [...], "final": str}
    chunk_delay = 0.0  # seconds between SSE chunks (and before the first)

    def log_message(self, fmt, *args):  # quieter logs
        pass

    def _send_json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_sse(self, chunks):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        # NO "Connection: keep-alive" and NO Content-Length: on HTTP/1.0 the
        # server closes the socket when the handler returns, and that close IS
        # the response terminator the client needs to finish the SSE stream.
        # (Sending keep-alive without a length leaves the stream unterminated:
        # chunks arrive but "end of stream" never fires — this was the true
        # cause of the observed opencode agent-loop wedge after a tool call.)
        self.end_headers()
        # Drip-feed: an instant response can race opencode's agent-loop event
        # subscription (observed as a wedge after the first tool call on both
        # 1.18.4 and 1.18.18). A small inter-chunk delay sidesteps the race.
        for c in chunks:
            if self.chunk_delay:
                time.sleep(self.chunk_delay)
            self.wfile.write(sse_chunk(c))
            self.wfile.flush()
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()

    def do_GET(self):
        print(f"[mock] GET {self.path}", flush=True)
        if self.path.rstrip("/").endswith("/models"):
            self._send_json({"object": "list", "data": [
                {"id": "spike-model", "object": "model", "created": 0, "owned_by": "spike"}]})
        else:
            self._send_json({"ok": True})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        try:
            req = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            return self._send_json({"error": "bad json"}, 400)
        print(f"[mock] POST {self.path} (pre-check)", flush=True)
        if not self.path.rstrip("/").endswith("/chat/completions"):
            return self._send_json({"error": "unknown path"}, 404)

        messages = req.get("messages", [])
        stream = bool(req.get("stream"))
        sentinel = self.script["sentinel"]
        engaged = any(sentinel in (m.get("content") or "")
                      for m in messages if isinstance(m.get("content"), str))
        print(f"[mock] POST {self.path} msgs={len(messages)} engaged={engaged} stream_options={req.get('stream_options')}", flush=True)

        if not engaged:
            return self._respond("ok", "stop", stream, self._wants_usage(req))

        bound = self._bind_captures(messages)
        n_tool_results = sum(1 for m in messages if m.get("role") == "tool")
        steps = self.script["steps"]
        print(f"[mock] engaged, tool_results={n_tool_results}, stream={stream}", flush=True)

        if self.script.get("execute_locally"):
            # A8 mode: the mock acts as the hub itself — it runs the scripted
            # git steps locally (real subprocess, real credentials from the
            # captured prompt) and answers with the final text immediately.
            # No tool calls ⇒ no opencode agent-loop rounds ⇒ the 1.18.x
            # after-tool wedge never comes into play. OpenCode is the message
            # carrier; everything Matea-side (Prepare/inject/Approve/PR/
            # Cleanup) and everything Gitea-side (deploy key auth) stays real.
            # Guard on `tools`: only the real agent-loop request carries tool
            # definitions; title/summary generation must not re-run the steps.
            if n_tool_results == 0 and req.get("tools"):
                text = self._execute_steps_and_final(bound)
                return self._respond(text, "stop", stream, self._wants_usage(req))

        if n_tool_results < len(steps):
            cmd = self._subst(steps[n_tool_results]["command"], bound)
            call = {"index": 0, "id": f"call_{n_tool_results}", "type": "function",
                    "function": {"name": "bash",
                                 "arguments": json.dumps({"command": cmd, "description": f"spike step {n_tool_results}"})}}
            return self._respond_tool_call(call, stream, self._wants_usage(req))
        return self._respond(self._subst(self.script["final"], bound), "stop", stream, self._wants_usage(req))

    def _wants_usage(self, req):
        # Only emit the trailing usage chunk when the client asked for it
        # (stream_options.include_usage). An unsolicited empty-choices usage
        # chunk after finish_reason can confuse strict SSE consumers.
        return bool((req.get("stream_options") or {}).get("include_usage"))

    def _execute_steps_and_final(self, bound):
        """Run every scripted step locally via bash; return the final text.

        HEAD_SHA is taken from the last step's stdout (the rev-parse step),
        so the trailer line reports the actual pushed draft head.
        """
        import subprocess
        workdir = self.script.get("workdir") or "."
        # Prefer the full Git-for-Windows bash path: a bare "bash" resolves to
        # WSL (C:\Windows\System32\bash.exe) on a native-Windows PATH, where
        # chmod is a DrvFS no-op and OpenSSH then rejects the key as 0777.
        bash = "bash"
        from shutil import which
        import os
        for cand in (r"C:\Program Files\Git\usr\bin\bash.exe",
                     r"C:\Program Files\Git\bin\bash.exe", "bash"):
            if os.path.exists(cand) or which(cand):
                bash = cand
                break
        print(f"[mock] execute_locally via bash={bash} workdir={workdir}", flush=True)
        last_out = ""
        for i, step in enumerate(self.script["steps"]):
            cmd = self._subst(step["command"], bound)
            print(f"[mock] exec step {i} (len={len(cmd)}): {cmd}", flush=True)
            p = subprocess.run([bash, "-c", cmd], cwd=workdir,
                               capture_output=True, text=True, timeout=120)
            last_out = (p.stdout or "").strip()
            if p.returncode != 0:
                err = (p.stderr or "")[-400:]
                out = (p.stdout or "")[-800:]
                print(f"[mock] step {i} FAILED rc={p.returncode}:\nstdout: {out}\nstderr: {err}", flush=True)
                return f"hub execution failed at step {i}: {err or out}"
            print(f"[mock] step {i} ok: {last_out[-110:]}", flush=True)
        sha = ""
        m = re.search(r"([0-9a-f]{40})", last_out)
        if m:
            sha = m.group(1)
        bound = dict(bound)
        bound["HEAD_SHA"] = sha
        return self._subst(self.script["final"], bound)

    def _bind_captures(self, messages):
        """Apply configured capture regexes to the whole transcript.

        Tool results may carry content as a string or as a list of parts;
        both are flattened into the haystack so step outputs (e.g. a sha
        printed by `git rev-parse HEAD`) can be captured for later use.
        """
        captures = self.script.get("captures") or {}
        if not captures:
            return {}
        parts = []
        for m in messages:
            c = m.get("content")
            if isinstance(c, str):
                parts.append(c)
            elif isinstance(c, list):
                for p in c:
                    if isinstance(p, dict) and isinstance(p.get("text"), str):
                        parts.append(p["text"])
        haystack = "\n".join(parts)
        bound = {}
        for name, pattern in captures.items():
            m = re.search(pattern, haystack)
            if m:
                bound[name] = m.group(1)
            else:
                bound[name] = ""
                print(f"[mock] WARNING: capture {name} (/ {pattern} /) not found in transcript", flush=True)
        return bound

    def _subst(self, text, bound):
        for name, value in bound.items():
            text = text.replace("{" + name + "}", value)
        return text

    def _respond(self, text, finish, stream, with_usage=True):
        if not stream:
            return self._send_json({
                "id": chat_id(), "object": "chat.completion", "created": int(time.time()),
                "model": "spike-model",
                "choices": [{"index": 0, "finish_reason": finish,
                             "message": {"role": "assistant", "content": text}}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        rid = chat_id()
        chunks = [
            _chunk(rid, {"role": "assistant", "content": text}),
            _chunk(rid, {}, finish),
        ]
        if with_usage:
            chunks.append(_usage_chunk(rid))
        self._send_sse(chunks)

    def _respond_tool_call(self, call, stream, with_usage=True):
        if not stream:
            return self._send_json({
                "id": chat_id(), "object": "chat.completion", "created": int(time.time()),
                "model": "spike-model",
                "choices": [{"index": 0, "finish_reason": "tool_calls",
                             "message": {"role": "assistant", "content": None, "tool_calls": [call]}}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        # Canonical streamed tool call: header chunk (id/name), arguments delta,
        # finish chunk, usage chunk — all sharing one response id.
        rid = chat_id()
        head = {"index": 0, "id": call["id"], "type": "function",
                "function": {"name": call["function"]["name"], "arguments": ""}}
        args = {"index": 0, "function": {"arguments": call["function"]["arguments"]}}
        chunks = [
            _chunk(rid, {"role": "assistant", "tool_calls": [head]}),
            _chunk(rid, {"tool_calls": [args]}),
            _chunk(rid, {}, "tool_calls"),
        ]
        if with_usage:
            chunks.append(_usage_chunk(rid))
        self._send_sse(chunks)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18080)
    ap.add_argument("--script", required=True)
    ap.add_argument("--chunk-delay-ms", type=int, default=0,
                    help="delay between SSE chunks; sidesteps an opencode loop race")
    ap.add_argument("--workdir", default=None,
                    help="cwd for execute_locally steps (the hub workspace)")
    ap.add_argument("--mode", choices=["tools", "local"], default=None,
                    help="override script execute_locally: tools = drive opencode's "
                         "bash tool (default script behavior), local = mock executes")
    args = ap.parse_args()
    Handler.script = json.load(open(args.script, encoding="utf-8"))
    if args.workdir:
        Handler.script["workdir"] = args.workdir
    if args.mode:
        Handler.script["execute_locally"] = (args.mode == "local")
    Handler.chunk_delay = args.chunk_delay_ms / 1000.0
    srv = QuietHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"mock LLM on 127.0.0.1:{args.port}, {len(Handler.script['steps'])} steps")
    srv.serve_forever()


if __name__ == "__main__":
    main()
