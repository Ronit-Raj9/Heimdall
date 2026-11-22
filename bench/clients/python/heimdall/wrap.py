"""Auto-wrap an agent object for pull mode (``heimdall serve module:object``).

``load_object("app/agent.py:graph")`` imports the object; ``wrap(obj)`` picks
how to call it by its type and returns ``(fn, kind)``, where ``fn(turn)``
answers one Heimdall turn (heimdall.serve's contract) and ``kind`` names what
was recognised:

======================  ==========================================================
kind                    how a turn is answered
======================  ==========================================================
``langgraph``           ``graph.invoke({"messages": [...]})``, last message's text
``crewai``              ``crew.kickoff(inputs={...})``, the crew output's raw text
``openai-agents``       ``Runner.run_sync(agent, messages)``, ``final_output``
``pydantic-ai``         ``agent.run_sync(text)``, ``output``
``adk``                 an in-memory ADK runner, one session per Heimdall session
``smolagents``          ``agent.run(text, reset=...)``, one session at a time
``dspy``                ``module(<first input field>=text)``, the answer field
``function``            called with the turn, the messages or the text by signature
======================  ==========================================================

No framework is imported unless the object comes from it. The agent's own
tools run inside the framework; Heimdall's task tools are offered to plain
functions only (``turn.tools``). Point the framework's model client at your
provider as usual; with Bifrost, set ``OPENAI_BASE_URL`` to it so cost is
measured on your gateway.
"""

from __future__ import annotations

import asyncio
import importlib
import importlib.util
import inspect
import os
import sys
import threading
from typing import Any, Callable, Dict, List, Optional, Tuple

INPUT_KEYS = ("input", "query", "question", "message", "topic", "task", "prompt")


def load_object(ref: str, base_dir: Optional[str] = None) -> Any:
    """Imports ``module.path:attr`` or ``path/to/file.py:attr`` (attr may be
    dotted). The folder of the file and ``base_dir`` (default the current
    folder) are put on ``sys.path`` so the agent's own imports work."""
    if ":" not in ref:
        raise ValueError("an agent reference is module:object or file.py:object, got %r" % ref)
    mod_ref, _, attr = ref.rpartition(":")
    base = os.path.abspath(base_dir or os.getcwd())
    if base not in sys.path:
        sys.path.insert(0, base)
    if mod_ref.endswith(".py") or os.sep in mod_ref or "/" in mod_ref:
        path = mod_ref if os.path.isabs(mod_ref) else os.path.join(base, mod_ref)
        if not os.path.isfile(path):
            raise FileNotFoundError("no such file: %s" % path)
        folder = os.path.dirname(os.path.abspath(path))
        if folder not in sys.path:
            sys.path.insert(0, folder)
        name = "heimdall_agent_" + os.path.splitext(os.path.basename(path))[0]
        spec = importlib.util.spec_from_file_location(name, path)
        if spec is None or spec.loader is None:
            raise ImportError("can not load %s" % path)
        module = importlib.util.module_from_spec(spec)
        sys.modules[name] = module
        spec.loader.exec_module(module)
    else:
        module = importlib.import_module(mod_ref)
    obj: Any = module
    for part in attr.split("."):
        if not hasattr(obj, part):
            raise AttributeError("%s has no %s" % (mod_ref, attr))
        obj = getattr(obj, part)
    return obj


def _modules(obj: Any) -> List[str]:
    cls = obj if isinstance(obj, type) else type(obj)
    return [getattr(c, "__module__", "") or "" for c in getattr(cls, "__mro__", (cls,))]


def _from(obj: Any, *prefixes: str) -> bool:
    return any(m == p or m.startswith(p + ".") for m in _modules(obj) for p in prefixes)


def _class_names(obj: Any) -> List[str]:
    cls = obj if isinstance(obj, type) else type(obj)
    return [c.__name__ for c in getattr(cls, "__mro__", (cls,))]


def turn_text(turn: Any) -> str:
    """The text a turn asks the agent to answer: the customer's message in
    a chat, else the task instruction."""
    msg = getattr(turn, "message", None)
    if getattr(turn, "kind", "task") == "conversation" and msg:
        return msg
    return getattr(turn, "instruction", "") or msg or ""


def turn_messages(turn: Any) -> List[Dict[str, str]]:
    """The chat so far as ``{"role", "content"}`` dicts (text only)."""
    out: List[Dict[str, str]] = []
    for m in getattr(turn, "messages", None) or []:
        if not isinstance(m, dict):
            continue
        role, content = m.get("role"), m.get("content")
        if isinstance(content, list):
            content = "\n".join(p.get("text", "") for p in content if isinstance(p, dict))
        if role == "tool":
            role = "user"
        if role in ("system", "user", "assistant") and isinstance(content, str) and (content or role == "assistant"):
            out.append({"role": role, "content": content})
    if not out:
        out = [{"role": "user", "content": turn_text(turn)}]
    return out


def transcript(turn: Any) -> str:
    """The chat as plain text for frameworks that take one string: the
    earlier messages, then the latest one."""
    msgs = turn_messages(turn)
    if len(msgs) <= 1:
        return turn_text(turn)
    lines = ["Conversation so far:"]
    for m in msgs[:-1]:
        lines.append("%s: %s" % (m["role"], m["content"]))
    lines.append("")
    lines.append(msgs[-1]["content"])
    return "\n".join(lines)


def _run(value: Any) -> Any:
    if inspect.isawaitable(value):
        return asyncio.run(_await(value))
    return value


async def _await(value: Any) -> Any:
    return await value


def _content_text(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for p in content:
            if isinstance(p, str):
                parts.append(p)
            elif isinstance(p, dict) and isinstance(p.get("text"), str):
                parts.append(p["text"])
            elif hasattr(p, "text") and isinstance(p.text, str):
                parts.append(p.text)
        return "".join(parts)
    return "" if content is None else str(content)


def _last_message_text(out: Any) -> str:
    msgs = out.get("messages") if isinstance(out, dict) else getattr(out, "messages", None)
    if not msgs:
        return _content_text(out if isinstance(out, str) else str(out))
    last = msgs[-1]
    if isinstance(last, dict):
        return _content_text(last.get("content"))
    return _content_text(getattr(last, "content", last))


def _wrap_langgraph(graph: Any) -> Callable:
    stateful = getattr(graph, "checkpointer", None) not in (None, False)

    def call(turn: Any) -> str:
        msgs = turn_messages(turn)
        if stateful:
            msgs = msgs[-1:]  # the graph keeps the thread itself
        config = {"configurable": {"thread_id": getattr(turn, "session_id", "") or getattr(turn, "id", "")}}
        return _last_message_text(graph.invoke({"messages": msgs}, config=config))

    return call


def _wrap_crewai(crew: Any) -> Callable:
    if isinstance(crew, type):
        inst = crew()
        crew = inst.crew() if hasattr(inst, "crew") else inst
    lock = threading.Lock()

    def call(turn: Any) -> str:
        text = transcript(turn)
        inputs = {k: text for k in INPUT_KEYS}
        with lock:  # one Crew object: one kickoff at a time
            out = crew.kickoff(inputs=inputs)
        raw = getattr(out, "raw", None)
        return raw if isinstance(raw, str) else str(out)

    return call


def _wrap_openai_agents(agent: Any) -> Callable:
    from agents import Runner  # type: ignore  # the object came from this package

    def call(turn: Any) -> str:
        result = Runner.run_sync(agent, turn_messages(turn))
        out = getattr(result, "final_output", result)
        return out if isinstance(out, str) else str(out)

    return call


def _wrap_pydantic_ai(agent: Any) -> Callable:
    def call(turn: Any) -> str:
        result = agent.run_sync(transcript(turn))
        for attr in ("output", "data"):
            if hasattr(result, attr):
                out = getattr(result, attr)
                return out if isinstance(out, str) else str(out)
        return str(result)

    return call


def _wrap_adk(agent: Any) -> Callable:
    from google.adk.runners import InMemoryRunner  # type: ignore
    from google.genai import types  # type: ignore

    runner = InMemoryRunner(agent=agent, app_name="heimdall")
    sessions: Dict[str, str] = {}
    lock = threading.Lock()

    def session_for(key: str) -> str:
        with lock:
            if key in sessions:
                return sessions[key]
            s = _run(runner.session_service.create_session(app_name="heimdall", user_id="heimdall"))
            sessions[key] = s.id
            return s.id

    def call(turn: Any) -> str:
        sid = session_for(getattr(turn, "session_id", "") or getattr(turn, "id", ""))
        msg = types.Content(role="user", parts=[types.Part(text=turn_text(turn))])
        text = ""
        for event in runner.run(user_id="heimdall", session_id=sid, new_message=msg):
            final = event.is_final_response() if hasattr(event, "is_final_response") else False
            if final and event.content and event.content.parts:
                text = "".join(getattr(p, "text", "") or "" for p in event.content.parts)
        return text

    return call


def _wrap_smolagents(agent: Any) -> Callable:
    lock = threading.Lock()
    current = {"session": None}

    def call(turn: Any) -> str:
        sid = getattr(turn, "session_id", "") or getattr(turn, "id", "")
        with lock:  # one agent object keeps one memory: one session at a time
            reset = current["session"] != sid
            current["session"] = sid
            out = agent.run(turn_text(turn), reset=reset)
        return out if isinstance(out, str) else str(out)

    return call


def _dspy_signature(module: Any) -> Any:
    sig = getattr(module, "signature", None)
    if sig is not None:
        return sig
    preds = getattr(module, "predictors", None)
    if callable(preds):
        try:
            ps = preds()
            if ps:
                return getattr(ps[0], "signature", None)
        except Exception:  # noqa: BLE001 - best effort introspection
            return None
    return None


def _wrap_dspy(module: Any) -> Callable:
    sig = _dspy_signature(module)
    inputs = list(getattr(sig, "input_fields", {}) or {}) if sig is not None else []
    outputs = list(getattr(sig, "output_fields", {}) or {}) if sig is not None else []
    in_field = next((k for k in INPUT_KEYS if k in inputs), inputs[0] if inputs else "question")

    def call(turn: Any) -> str:
        kwargs = {in_field: transcript(turn)}
        if "history" in inputs and in_field != "history":
            kwargs["history"] = turn_messages(turn)[:-1]
        pred = module(**kwargs)
        for name in ["answer", "response", "output"] + outputs:
            if hasattr(pred, name):
                v = getattr(pred, name)
                return v if isinstance(v, str) else str(v)
        return str(pred)

    return call


def _wrap_function(fn: Callable) -> Callable:
    try:
        params = list(inspect.signature(fn).parameters.values())
    except (TypeError, ValueError):
        params = []
    first = params[0] if params else None
    ann = getattr(first, "annotation", None) if first else None
    ann_name = ann if isinstance(ann, str) else getattr(ann, "__name__", "")
    if first is None:
        def call(turn: Any) -> Any:
            return _run(fn())
    elif first.name == "turn" or ann_name == "Turn":
        def call(turn: Any) -> Any:
            return _run(fn(turn))
    elif first.name in ("messages", "history", "chat"):
        def call(turn: Any) -> Any:
            return _run(fn(turn_messages(turn)))
    else:
        def call(turn: Any) -> Any:
            return _run(fn(turn_text(turn)))
    return call


def wrap(obj: Any) -> Tuple[Callable, str]:
    """Picks how to call an agent object. Returns (fn(turn), kind)."""
    names = _class_names(obj)
    if _from(obj, "langgraph") and hasattr(obj, "invoke"):
        return _wrap_langgraph(obj), "langgraph"
    if _from(obj, "crewai") and (hasattr(obj, "kickoff") or isinstance(obj, type)):
        return _wrap_crewai(obj), "crewai"
    if _from(obj, "agents") and "Agent" in names and hasattr(sys.modules.get("agents"), "Runner"):
        return _wrap_openai_agents(obj), "openai-agents"
    if _from(obj, "pydantic_ai") and hasattr(obj, "run_sync"):
        return _wrap_pydantic_ai(obj), "pydantic-ai"
    if _from(obj, "google.adk"):
        return _wrap_adk(obj), "adk"
    if _from(obj, "smolagents") and hasattr(obj, "run"):
        return _wrap_smolagents(obj), "smolagents"
    if _from(obj, "dspy") and callable(obj):
        return _wrap_dspy(obj), "dspy"
    if inspect.isfunction(obj) or inspect.ismethod(obj) or (callable(obj) and not isinstance(obj, type)):
        return _wrap_function(obj), "function"
    raise TypeError(
        "do not know how to call %s: heimdall serve wraps LangGraph graphs, CrewAI crews, OpenAI Agents SDK agents, "
        "Pydantic AI agents, ADK agents, smolagents agents, DSPy modules and plain functions" % type(obj).__name__)


__all__ = ["load_object", "wrap", "turn_text", "turn_messages", "transcript"]
