"""Serve your own agent function to Heimdall: pull mode (docs/pull-agents.md).

Your agent runs in your own process and connects out to Heimdall over
HTTPS; nothing connects in to your network. Register an agent with adapter
``pull`` (``pull: {name: support-bot}``), start an evaluation, and run::

    from heimdall.serve import serve

    def solve(turn):
        # turn.instruction, turn.message (a chat turn), turn.messages,
        # turn.tools, turn.repo (coding tasks) ...
        return "the answer"            # or Answer(text=..., tool_calls=..., patch=...)

    serve("support-bot", solve, url="https://heimdall.example.com",
          token=os.environ["HEIMDALL_TOKEN"], concurrency=4)

Each turn is one call of ``fn``. Return a string (the agent's text), an
:class:`Answer`, or a dict with ``text``, ``tool_calls`` (``[{"id",
"name", "arguments"}]``: Heimdall runs them through the task's tools and
sends their results back as the next turn, ``kind == "tool_results"``),
``patch`` (a unified diff, for coding tasks) and ``usage``
(``{"input_tokens", "output_tokens"}``). An exception fails the turn with
its message.

Standard library only. The token is an org API token (scope evaluate or
admin); on a local server, its write token.
"""

from __future__ import annotations

import io
import json
import os
import socket
import tarfile
import threading
import urllib.error
import urllib.request
import uuid
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Union

PULL_PREFIX = "/api/v1/pull"
MAX_REPO_BYTES = 48 << 20

# The routes serve() calls, as (method, OpenAPI path).
SERVE_ROUTES = (
    ("POST", "/api/v1/pull/next"),
    ("POST", "/api/v1/pull/turns/{id}/answer"),
    ("GET", "/api/v1/pull/agents"),
)


@dataclass
class ToolCall:
    """A tool call for Heimdall to run through the task's tools."""

    name: str
    arguments: Dict[str, Any] = field(default_factory=dict)
    id: str = ""

    def to_dict(self) -> Dict[str, Any]:
        return {"id": self.id or "call_" + uuid.uuid4().hex[:12], "name": self.name, "arguments": self.arguments}


@dataclass
class Answer:
    """What a turn answers. Set ``tool_calls`` to have Heimdall run tools,
    ``patch`` to return a unified diff for a coding task."""

    text: str = ""
    tool_calls: List[ToolCall] = field(default_factory=list)
    patch: Optional[str] = None
    usage: Optional[Dict[str, int]] = None
    version: Optional[str] = None

    def to_dict(self) -> Dict[str, Any]:
        out: Dict[str, Any] = {"text": self.text}
        if self.tool_calls:
            out["tool_calls"] = [c.to_dict() if isinstance(c, ToolCall) else c for c in self.tool_calls]
        if self.patch is not None:
            out["patch"] = self.patch
        if self.usage:
            out["usage"] = self.usage
        if self.version:
            out["version"] = self.version
        return out


class Turn:
    """One unit of work for your agent.

    kind: "task" (the task's first turn), "conversation" (a customer message
    when Heimdall drives a chat: answer with your reply) or "tool_results"
    (the results of the tool calls you asked for are the last messages).
    """

    def __init__(self, doc: Dict[str, Any]):
        p = doc.get("payload") or {}
        self.id: str = doc.get("id", "")
        self.agent: str = doc.get("agent", "")
        self.run_id: str = doc.get("run_id", "")
        self.deadline: str = doc.get("deadline", "")
        self.payload: Dict[str, Any] = p
        self.kind: str = p.get("kind", "task")
        self.task_id: str = p.get("task_id", "")
        self.session_id: str = p.get("session_id", "")
        self.session_index: int = int(p.get("session_index") or 1)
        self.output: str = p.get("output", "answer")
        self.instruction: str = p.get("instruction", "")
        self.message: Optional[str] = p.get("message")
        self.turn: int = int(p.get("turn") or 0)
        self.messages: List[Dict[str, Any]] = p.get("messages") or []
        self.tools: List[Dict[str, Any]] = p.get("tools") or []
        self.repo: Optional[Dict[str, Any]] = p.get("repo")

    def openai_tools(self) -> List[Dict[str, Any]]:
        """The task's tools as OpenAI chat function tools."""
        return [{"type": "function", "function": {"name": t["name"], "description": t.get("description", ""),
                                                   "parameters": t.get("input_schema") or {"type": "object", "properties": {}}}}
                for t in self.tools]

    def download_repo(self, dest: str) -> List[str]:
        """Downloads the task's repository tarball (coding tasks) and unpacks
        it into dest, refusing links and paths that leave dest. Returns the
        files written. Make your change there and answer
        ``Answer(patch=<git diff>)``."""
        if not self.repo or not self.repo.get("url"):
            raise ValueError("this turn carries no repository (not a coding task, or not its first turn)")
        req = urllib.request.Request(self.repo["url"], headers={"Accept": "application/gzip"})
        with urllib.request.urlopen(req, timeout=300) as resp:
            data = resp.read(MAX_REPO_BYTES + 1)
        if len(data) > MAX_REPO_BYTES:
            raise ValueError("the repository tarball is larger than %d bytes" % MAX_REPO_BYTES)
        return safe_extract(data, dest)


def safe_extract(data: bytes, dest: str) -> List[str]:
    """Unpacks a tar.gz into dest: regular files and folders only, nothing
    outside dest."""
    root = os.path.realpath(dest)
    os.makedirs(root, exist_ok=True)
    written = []
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tar:
        for m in tar.getmembers():
            if not (m.isfile() or m.isdir()):
                continue
            target = os.path.realpath(os.path.join(root, m.name))
            if target != root and not target.startswith(root + os.sep):
                continue
            if m.isdir():
                os.makedirs(target, exist_ok=True)
                continue
            os.makedirs(os.path.dirname(target), exist_ok=True)
            src = tar.extractfile(m)
            if src is None:
                continue
            with open(target, "wb") as f:
                f.write(src.read())
            os.chmod(target, 0o755 if m.mode & 0o111 else 0o644)
            written.append(os.path.relpath(target, root))
    return written


def to_answer(value: Any) -> Dict[str, Any]:
    if isinstance(value, Answer):
        return value.to_dict()
    if isinstance(value, str):
        return {"text": value}
    if value is None:
        return {"text": ""}
    if isinstance(value, dict):
        out = dict(value)
        if out.get("tool_calls"):
            out["tool_calls"] = [c.to_dict() if isinstance(c, ToolCall) else c for c in out["tool_calls"]]
        return out
    raise TypeError("an agent function returns a string, an Answer or a dict, not %s" % type(value).__name__)


class PullError(Exception):
    """Heimdall refused a pull request."""

    def __init__(self, status: int, message: str):
        super().__init__("HTTP %d: %s" % (status, message))
        self.status = status


class _Conn:
    def __init__(self, url: str, token: str, timeout: float):
        self.url, self.token, self.timeout = url.rstrip("/"), token, timeout

    def call(self, method: str, path: str, body: Optional[Dict[str, Any]] = None, timeout: Optional[float] = None):
        data = json.dumps(body).encode("utf-8") if body is not None else None
        headers = {"Authorization": "Bearer " + self.token, "Accept": "application/json", "User-Agent": "heimdall-serve-python"}
        if data is not None:
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.url + path, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=timeout or self.timeout) as resp:
                raw = resp.read()
                return resp.status, (json.loads(raw) if raw.strip() else {})
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                msg = (json.loads(raw).get("error") or {}).get("message") or raw[:300].decode("utf-8", "replace")
            except (ValueError, AttributeError):
                msg = raw[:300].decode("utf-8", "replace")
            raise PullError(e.code, msg) from None


def serve(
    agent: str,
    fn: Callable[[Turn], Union[str, Answer, Dict[str, Any], None]],
    url: Optional[str] = None,
    token: Optional[str] = None,
    concurrency: int = 1,
    max_turns: Optional[int] = None,
    stop: Optional[threading.Event] = None,
    on_error: Optional[Callable[[Turn, BaseException], None]] = None,
    log: Optional[Callable[[str], None]] = None,
) -> int:
    """Takes turns of ``agent`` from Heimdall and answers each with ``fn``
    until ``stop`` is set, ``max_turns`` turns were answered, or Ctrl-C.
    Returns the number of turns handled. ``url`` and ``token`` default to
    HEIMDALL_URL and HEIMDALL_TOKEN."""
    url = url or os.environ.get("HEIMDALL_URL", "")
    token = token or os.environ.get("HEIMDALL_TOKEN", "")
    if not url or not token:
        raise ValueError("serve needs the Heimdall URL and an API token (url=, token= or HEIMDALL_URL, HEIMDALL_TOKEN)")
    say = log or (lambda m: None)
    conn = _Conn(url, token, 60.0)
    stop = stop or threading.Event()
    client = "%s-%s" % (socket.gethostname()[:40], uuid.uuid4().hex[:8])
    handled = [0]
    lock = threading.Lock()

    def take_one() -> bool:
        try:
            status, doc = conn.call("POST", PULL_PREFIX + "/next", {"agent": agent, "client": client, "wait_s": 25}, timeout=60)
        except PullError as e:
            if e.status in (401, 403, 404):
                raise
            say("next: %s" % e)
            stop.wait(2)
            return False
        except OSError as e:
            say("next: %s" % e)
            stop.wait(2)
            return False
        if status == 204 or not doc.get("id"):
            return False
        turn = Turn(doc)
        try:
            answer: Dict[str, Any] = {"answer": to_answer(fn(turn))}
        except Exception as e:  # noqa: BLE001 - the agent's own failure fails the turn
            if on_error:
                on_error(turn, e)
            answer = {"error": "%s: %s" % (type(e).__name__, e)}
        for attempt in range(3):
            try:
                conn.call("POST", PULL_PREFIX + "/turns/%s/answer" % turn.id, answer, timeout=60)
                break
            except PullError as e:
                say("answer %s: %s" % (turn.id, e))
                break
            except OSError as e:
                say("answer %s: %s" % (turn.id, e))
                stop.wait(1 + attempt)
        with lock:
            handled[0] += 1
            if max_turns is not None and handled[0] >= max_turns:
                stop.set()
        return True

    errors: List[BaseException] = []

    def worker():
        while not stop.is_set():
            try:
                take_one()
            except PullError as e:
                errors.append(e)
                stop.set()

    threads = [threading.Thread(target=worker, daemon=True) for _ in range(max(1, concurrency))]
    say("serving %s with %d worker(s)" % (agent, len(threads)))
    for t in threads:
        t.start()
    try:
        while any(t.is_alive() for t in threads):
            for t in threads:
                t.join(0.5)
    except KeyboardInterrupt:
        stop.set()
    if errors:
        raise errors[0]
    return handled[0]


def agents(url: Optional[str] = None, token: Optional[str] = None) -> List[Dict[str, Any]]:
    """The organization's pull agents and whether a process serves each."""
    conn = _Conn(url or os.environ.get("HEIMDALL_URL", ""), token or os.environ.get("HEIMDALL_TOKEN", ""), 30.0)
    _, doc = conn.call("GET", PULL_PREFIX + "/agents")
    return doc.get("agents") or []


__all__ = ["Answer", "PullError", "SERVE_ROUTES", "ToolCall", "Turn", "agents", "safe_extract", "serve", "to_answer"]
