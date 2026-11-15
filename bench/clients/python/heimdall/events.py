"""Agent events: an agent reports its own internal steps to the Heimdall bench.

    from heimdall import events

    plan = events.emit("plan", "fix the failing test", output="read, edit, run the tests")
    with events.step("tool_call", "run_tests", parent_id=plan, input={"cmd": "pytest -q"}) as s:
        s.output = run_tests()

The wire format is spec/agent-event.schema.json. Delivery follows the
environment the bench sets in the agent container: BENCH_EVENTS_URL (POST,
JSON lines), else BENCH_EVENTS_FILE (one JSON object appended per line). A
failed POST falls back to the file when both are set. With neither set every
call is a no-op that still returns an event id, so instrumented code runs the
same outside the bench. Nothing here raises into the agent. ``stdout_line``
builds the ``@@heimdall-event `` line for an agent that can only print.

Everything an agent reports is its own word: the bench shows it as
agent-reported evidence, which never raises a trusted metric or decides an
outcome. Standard library only.
"""

import contextlib
import datetime
import json
import os
import threading
import time
import urllib.error
import urllib.request
import uuid
from typing import Any, Dict, Iterator, Optional

ENV_URL = "BENCH_EVENTS_URL"
ENV_FILE = "BENCH_EVENTS_FILE"
STDOUT_PREFIX = "@@heimdall-event "

TYPES = (
    "plan",
    "thought_summary",
    "tool_call",
    "tool_result",
    "subagent_start",
    "subagent_end",
    "handoff",
    "memory_read",
    "memory_write",
    "retrieval",
    "message",
    "error",
    "custom",
)
STATUSES = ("ok", "error", "started", "unknown")

# Input and output are sent as JSON while their text fits MAX_TEXT bytes, and
# as text cut to MAX_TEXT bytes otherwise. The bench keeps less (2 KiB).
MAX_TEXT = 8192
POST_TIMEOUT_S = 2.0
# After a failed POST the URL is skipped for this long, so an unreachable
# collector costs the agent one timeout, not one per event.
URL_RETRY_S = 30.0

_file_lock = threading.Lock()
_url_lock = threading.Lock()
_url_down_until = 0.0


def enabled() -> bool:
    """True when the bench asked for events (either variable is set)."""
    return bool(os.environ.get(ENV_URL) or os.environ.get(ENV_FILE))


def new_id() -> str:
    """A fresh event id: a uuid4 in hex."""
    return uuid.uuid4().hex


def now() -> str:
    """The current UTC time as RFC 3339 with milliseconds, e.g. 2026-11-15T13:04:05.123Z."""
    return _format_time(datetime.datetime.now(datetime.timezone.utc))


def _format_time(t: datetime.datetime) -> str:
    if t.tzinfo is None:
        t = t.replace(tzinfo=datetime.timezone.utc)
    return t.astimezone(datetime.timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _time(v: Any) -> Any:
    # datetime values are formatted; strings (RFC 3339) and numbers (Unix
    # milliseconds) are sent as given.
    if isinstance(v, datetime.datetime):
        return _format_time(v)
    return v


def _cap(v: Any) -> Any:
    """v as JSON while its text fits MAX_TEXT bytes, else its text cut to MAX_TEXT bytes."""
    if isinstance(v, str):
        text, value = v, v
    else:
        try:
            text = json.dumps(v, ensure_ascii=False, default=str)
            value = json.loads(text)
        except (TypeError, ValueError):
            text = value = str(v)
    raw = text.encode("utf-8")
    if len(raw) <= MAX_TEXT:
        return value
    return raw[:MAX_TEXT].decode("utf-8", errors="ignore")


def _scalar(v: Any) -> Any:
    if v is None or isinstance(v, (str, bool, int, float)):
        return v
    try:
        return json.dumps(v, ensure_ascii=False, default=str)
    except (TypeError, ValueError):
        return str(v)


def build(
    type: str,
    name: Optional[str] = None,
    *,
    id: Optional[str] = None,
    parent_id: Optional[str] = None,
    tool_call_id: Optional[str] = None,
    input: Any = None,
    output: Any = None,
    status: Optional[str] = None,
    start_time: Any = None,
    end_time: Any = None,
    attributes: Optional[Dict[str, Any]] = None,
) -> Dict[str, Any]:
    """The event object emit sends, with an id and a start_time filled in when missing.

    Fields left as None are omitted. A start_time is set to now when neither
    time is given, so an event read later from the file keeps its own time.
    """
    ev: Dict[str, Any] = {"type": str(type), "id": str(id) if id else new_id()}
    if parent_id:
        ev["parent_id"] = str(parent_id)
    if name is not None:
        ev["name"] = str(name)
    if tool_call_id:
        ev["tool_call_id"] = str(tool_call_id)
    if input is not None:
        ev["input"] = _cap(input)
    if output is not None:
        ev["output"] = _cap(output)
    if status:
        ev["status"] = str(status)
    if start_time is None and end_time is None:
        start_time = now()
    if start_time is not None:
        ev["start_time"] = _time(start_time)
    if end_time is not None:
        ev["end_time"] = _time(end_time)
    if attributes:
        ev["attributes"] = {str(k): _scalar(v) for k, v in attributes.items()}
    return ev


def emit(
    type: str,
    name: Optional[str] = None,
    *,
    id: Optional[str] = None,
    parent_id: Optional[str] = None,
    tool_call_id: Optional[str] = None,
    input: Any = None,
    output: Any = None,
    status: Optional[str] = None,
    start_time: Any = None,
    end_time: Any = None,
    attributes: Optional[Dict[str, Any]] = None,
) -> str:
    """Report one event and return its id (generated when not given).

    A no-op when neither BENCH_EVENTS_URL nor BENCH_EVENTS_FILE is set. Never
    raises: a failed delivery is dropped silently.
    """
    try:
        ev = build(type, name, id=id, parent_id=parent_id, tool_call_id=tool_call_id, input=input, output=output,
                   status=status, start_time=start_time, end_time=end_time, attributes=attributes)
    except Exception:
        return str(id) if id else new_id()
    if enabled():
        try:
            _send(json.dumps(ev, ensure_ascii=False, default=str, separators=(",", ":")))
        except Exception:
            pass
    return ev["id"]


def stdout_line(type: str, name: Optional[str] = None, **kw: Any) -> str:
    """The stdout line that carries one event: '@@heimdall-event ' then the JSON object, no newline.

    Takes the same arguments as emit and sends nothing itself: print the
    line on its own, for example print(events.stdout_line("plan", output=text), flush=True).
    """
    ev = build(type, name, **kw)
    return STDOUT_PREFIX + json.dumps(ev, ensure_ascii=False, default=str, separators=(",", ":"))


class Step:
    """The handle step() yields: the event id, and output, status and attributes to set before the block ends."""

    def __init__(self, id: str, output: Any = None, status: Optional[str] = None,
                 attributes: Optional[Dict[str, Any]] = None) -> None:
        self.id = id
        self.output = output
        self.status = status
        self.attributes: Dict[str, Any] = dict(attributes or {})


@contextlib.contextmanager
def step(type: str, name: Optional[str] = None, **kw: Any) -> Iterator[Step]:
    """Time a block and report it as one event when the block ends.

    Takes the same keyword arguments as emit. The event carries the block's
    start and end time and status ok, or status error when the block raises;
    the exception is re-raised. Nest steps by passing the outer handle's id
    as parent_id.
    """
    s = Step(kw.pop("id", None) or new_id(), kw.pop("output", None), kw.pop("status", None), kw.pop("attributes", None))
    start = kw.pop("start_time", None) or now()
    kw.pop("end_time", None)
    try:
        yield s
    except BaseException as e:
        out = s.output if s.output is not None else "%s: %s" % (e.__class__.__name__, e)
        emit(type, name, id=s.id, output=out, status="error", start_time=start, end_time=now(),
             attributes=s.attributes, **kw)
        raise
    emit(type, name, id=s.id, output=s.output, status=s.status or "ok", start_time=start, end_time=now(),
         attributes=s.attributes, **kw)


def _send(line: str) -> None:
    url = os.environ.get(ENV_URL)
    path = os.environ.get(ENV_FILE)
    if url and _post(url, line):
        return
    if path:
        _append(path, line)


def _post(url: str, line: str) -> bool:
    global _url_down_until
    with _url_lock:
        if time.monotonic() < _url_down_until:
            return False
    try:
        req = urllib.request.Request(url, data=(line + "\n").encode("utf-8"), method="POST",
                                     headers={"Content-Type": "application/x-ndjson"})
        with urllib.request.urlopen(req, timeout=POST_TIMEOUT_S) as resp:
            resp.read()
            if 200 <= resp.status < 300:
                return True
    except urllib.error.HTTPError as e:
        e.close()
    except Exception:
        pass
    with _url_lock:
        _url_down_until = time.monotonic() + URL_RETRY_S
    return False


def _append(path: str, line: str) -> None:
    # One os.write of the whole line on an O_APPEND descriptor: lines from
    # several threads or processes never interleave, and nothing is buffered.
    data = (line + "\n").encode("utf-8")
    with _file_lock:
        try:
            parent = os.path.dirname(path)
            if parent:
                os.makedirs(parent, exist_ok=True)
            fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o644)
            try:
                os.write(fd, data)
            finally:
                os.close(fd)
        except Exception:
            pass


def _reset() -> None:
    """Forget a failed URL (for tests)."""
    global _url_down_until
    with _url_lock:
        _url_down_until = 0.0
