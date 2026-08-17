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
              "final": "text emitted after the last step"}
"""
import argparse
import json
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
        self.send_header("Connection", "keep-alive")
        self.end_headers()
        for c in chunks:
            self.wfile.write(sse_chunk(c))
        self.wfile.write(b"data: [DONE]\n\n")

    def do_GET(self):
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
        if not self.path.rstrip("/").endswith("/chat/completions"):
            return self._send_json({"error": "unknown path"}, 404)

        messages = req.get("messages", [])
        stream = bool(req.get("stream"))
        sentinel = self.script["sentinel"]
        engaged = any(sentinel in (m.get("content") or "")
                      for m in messages if isinstance(m.get("content"), str))

        if not engaged:
            return self._respond("ok", "stop", stream)

        n_tool_results = sum(1 for m in messages if m.get("role") == "tool")
        steps = self.script["steps"]
        print(f"[mock] engaged, tool_results={n_tool_results}, stream={stream}", flush=True)
        if n_tool_results < len(steps):
            cmd = steps[n_tool_results]["command"]
            call = {"index": 0, "id": f"call_{n_tool_results}", "type": "function",
                    "function": {"name": "bash",
                                 "arguments": json.dumps({"command": cmd, "description": f"spike step {n_tool_results}"})}}
            return self._respond_tool_call(call, stream)
        return self._respond(self.script["final"], "stop", stream)

    def _respond(self, text, finish, stream):
        if not stream:
            return self._send_json({
                "id": chat_id(), "object": "chat.completion", "created": int(time.time()),
                "model": "spike-model",
                "choices": [{"index": 0, "finish_reason": finish,
                             "message": {"role": "assistant", "content": text}}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})
        rid = chat_id()
        self._send_sse([
            _chunk(rid, {"role": "assistant", "content": text}),
            _chunk(rid, {}, finish),
            _usage_chunk(rid),
        ])

    def _respond_tool_call(self, call, stream):
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
        self._send_sse([
            _chunk(rid, {"role": "assistant", "tool_calls": [head]}),
            _chunk(rid, {"tool_calls": [args]}),
            _chunk(rid, {}, "tool_calls"),
            _usage_chunk(rid),
        ])


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18080)
    ap.add_argument("--script", required=True)
    args = ap.parse_args()
    Handler.script = json.load(open(args.script, encoding="utf-8"))
    srv = QuietHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"mock LLM on 127.0.0.1:{args.port}, {len(Handler.script['steps'])} steps")
    srv.serve_forever()


if __name__ == "__main__":
    main()
