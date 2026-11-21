"""CL-Bench stream service, written by `bifrost-bench import clbench`.

One Continual Learning Bench stream (a task family run on one named schedule)
served to the agent over MCP (streamable HTTP at /mcp). The upstream task
object is built exactly as `clbench run <family> --schedule <id>` builds it
for run index 0 (the canonical order), and every response the agent submits
goes through the upstream `task.step`, so rewards are upstream's.

The stream is split into sessions, one per schedule stage. The agent starts a
session with start_session(n); after the session's instances are complete,
submit refuses further work until the next session is started. Gating is
soft: nothing stops an agent from starting session n+1 itself, but each
outcome records the session it was completed in, and the verifier reports how
many sessions were used.

POST /finalize (no MCP tool reaches it) returns the run's outcomes as JSON.
It is called by the bench's state export inside this container after the
agent has exited. GET /healthz is the readiness check.

Environment: CLBENCH_FAMILY, CLBENCH_SCHEDULE, CLBENCH_STAGES (JSON list of
{"label", "instances"}), CLBENCH_REFERENCE_TOKEN (optional), CLBENCH_PORT.
"""

from __future__ import annotations

import hmac
import importlib
import inspect
import json
import math
import os
import sys
import threading
import traceback
from typing import Any

sys.path.insert(0, os.environ.get("CLBENCH_ROOT", "/opt/clbench"))
os.chdir(os.environ.get("CLBENCH_ROOT", "/opt/clbench"))

FAMILY = os.environ["CLBENCH_FAMILY"]
SCHEDULE = os.environ["CLBENCH_SCHEDULE"]
STAGES = json.loads(os.environ["CLBENCH_STAGES"])
TOKEN = os.environ.get("CLBENCH_REFERENCE_TOKEN", "")
PORT = int(os.environ.get("CLBENCH_PORT", "8000"))


def _jsonable(v: Any) -> Any:
    try:
        json.dumps(v)
        return v
    except TypeError:
        if isinstance(v, dict):
            return {str(k): _jsonable(x) for k, x in v.items()}
        if isinstance(v, (list, tuple)):
            return [_jsonable(x) for x in v]
        return str(v)


class Stream:
    """The upstream task and the session bookkeeping around it."""

    def __init__(self) -> None:
        from src.interface import Response, observation_marks_instance_complete
        from src.registry import TASKS_REGISTRY

        self._Response = Response
        self._complete = observation_marks_instance_complete
        modname = f"src.tasks.{FAMILY}.task"
        importlib.import_module(modname)
        classes = [c for c in TASKS_REGISTRY.values() if c.__module__ == modname]
        if not classes:
            raise RuntimeError(f"no task registered by {modname}")
        self.cls = classes[0]
        sig = inspect.signature(self.cls.__init__).parameters
        params = {k: v for k, v in {"schedule": SCHEDULE}.items() if k in sig}
        self.task = self.cls(**params)
        self.r_max = float(getattr(self.cls, "r_max", 1.0) or 1.0)
        self.total = sum(int(s["instances"]) for s in STAGES)
        self.bounds = []
        acc = 0
        for s in STAGES:
            acc += int(s["instances"])
            self.bounds.append(acc)
        self.session = 0
        self.sessions_started: list[int] = []
        self.completed = 0
        self.outcomes: dict[tuple[str, int], dict[str, Any]] = {}
        self.steps = 0
        self.invalid = 0
        self.lock = threading.Lock()
        self.query = self.task.reset()
        self.done = bool((self.query.metadata or {}).get("done"))

    # --- views -----------------------------------------------------------

    def limit(self) -> int:
        return self.bounds[self.session - 1] if self.session else 0

    def progress(self) -> dict[str, Any]:
        return {
            "session": self.session,
            "sessions_total": len(STAGES),
            "stage_label": STAGES[self.session - 1].get("label", "") if self.session else "",
            "instances_completed": self.completed,
            "instances_total": self.total,
            "session_instance_limit": self.limit(),
            "stream_done": self.done,
        }

    def query_view(self) -> dict[str, Any]:
        q = self.query
        view = self.progress()
        if self.done:
            view["status"] = "stream_complete"
            view["message"] = "The stream is complete. Stop now."
            return view
        if not self.session:
            view["status"] = "no_session"
            view["message"] = "Call start_session with this session's number first."
            return view
        if self.completed >= self.limit():
            view["status"] = "session_complete"
            view["message"] = "This session's instances are complete. Save what you learned to your memory and end the session; the next session continues the stream."
            return view
        view["status"] = "awaiting_response"
        view["instance_id"] = q.instance_id
        view["instance_index"] = q.instance_index
        view["feedback"] = q.feedback.content if q.feedback is not None else None
        view["prompt"] = q.prompt
        view["response_schema"] = q.response_schema.model_json_schema()
        return view

    def brief(self) -> dict[str, Any]:
        out: dict[str, Any] = {"family": FAMILY, "schedule": SCHEDULE, "description": ""}
        try:
            out["description"] = self.task.description()
        except Exception:  # noqa: BLE001 - optional upstream text
            pass
        try:
            b = self.task.get_agent_brief()
            if b is not None:
                out["brief"] = b.to_dict()
        except Exception:  # noqa: BLE001
            pass
        out["stages"] = [{"session": i + 1, "label": s.get("label", ""), "instances": s["instances"]} for i, s in enumerate(STAGES)]
        out.update(self.progress())
        return out

    # --- actions ---------------------------------------------------------

    def start_session(self, n: int) -> dict[str, Any]:
        if not isinstance(n, int) or n < 1 or n > len(STAGES):
            return {"error": f"session must be 1..{len(STAGES)}"}
        if n < self.session:
            return {"error": f"session {self.session} already started; sessions only move forward"}
        self.session = n
        self.sessions_started.append(n)
        return self.query_view()

    def reference_action(self):
        if FAMILY == "exploitable_poker":
            from src.tasks.exploitable_poker.oracle import expert_exploit_action

            atype, total = expert_exploit_action(self.task, self.task.game)
            return self.query.response_schema.model_validate(
                {"thinking": "reference", "action": atype.name, "amount": total}
            )
        raise RuntimeError(f"no reference policy for {FAMILY}")

    def submit(self, response: Any) -> dict[str, Any]:
        if self.done:
            return self.query_view()
        if not self.session:
            return {"error": "no session started; call start_session first"}
        if self.completed >= self.limit():
            return self.query_view()
        if isinstance(response, str):
            try:
                response = json.loads(response)
            except json.JSONDecodeError as exc:
                self.invalid += 1
                return {"error": f"response is not JSON: {exc}"}
        is_ref = isinstance(response, dict) and set(response) == {"__reference__"}
        try:
            if is_ref:
                if not TOKEN or not hmac.compare_digest(str(response["__reference__"]), TOKEN):
                    self.invalid += 1
                    return {"error": "response does not match response_schema"}
                action = self.reference_action()
            else:
                action = self.query.response_schema.model_validate(response)
        except Exception as exc:  # noqa: BLE001 - pydantic ValidationError and friends
            self.invalid += 1
            return {"error": f"response does not match response_schema: {exc}"}
        q = self.query
        try:
            result = self.task.step(self._Response(action=action))
        except Exception as exc:  # noqa: BLE001
            self.invalid += 1
            traceback.print_exc()
            return {"error": f"the task rejected the response: {exc}"}
        self.steps += 1
        obs = result.observation
        # Mirror upstream's runner (_collect_step_outcomes): the task's own
        # outcome list plus the step's, so instances that end between agent
        # turns (an opponent folding) are counted too.
        seen = []
        try:
            seen = list(self.task.get_instance_outcomes())
        except Exception:  # noqa: BLE001
            pass
        if result.instance_outcome is not None:
            seen.append(result.instance_outcome)
        for o in seen:
            key = (str(o.instance_id), int(o.instance_index))
            if key not in self.outcomes:
                self.outcomes[key] = {
                    "instance_id": key[0],
                    "instance_index": key[1],
                    "reward": float(o.reward),
                    "success": o.success,
                    "session": self.session,
                }
        instance_done = self._complete(obs)
        self.completed = max(self.completed + (1 if instance_done and not seen else 0), len(self.outcomes))
        if result.done or result.next_query is None:
            self.done = True
        else:
            nq = result.next_query
            if nq.feedback is None:
                nq.feedback = obs
            self.query = nq
            if (nq.metadata or {}).get("done"):
                self.done = True
        out = {"observation": obs.content, "instance_complete": instance_done, "previous_instance_id": q.instance_id}
        out["next"] = self.query_view()
        return out

    def finalize(self) -> dict[str, Any]:
        outcomes = dict(self.outcomes)
        rows = sorted(outcomes.values(), key=lambda r: r["instance_index"])
        rows = [r for r in rows if math.isfinite(r["reward"])]
        doc: dict[str, Any] = {
            "benchmark": "CL-Bench",
            "family": FAMILY,
            "schedule": SCHEDULE,
            "r_max": self.r_max,
            "instances_total": self.total,
            "instances_completed": self.completed,
            "outcomes": rows,
            "reward_sum": sum(r["reward"] for r in rows),
            "done": self.done,
            "steps": self.steps,
            "invalid_submissions": self.invalid,
            "sessions_started": self.sessions_started,
            "sessions_total": len(STAGES),
        }
        if self.done:
            try:
                res = self.task.evaluate()
                doc["evaluate"] = {"summary": res.summary, "metrics": _jsonable(res.metrics)}
            except Exception as exc:  # noqa: BLE001
                doc["evaluate_error"] = str(exc)
        return doc


STREAM = Stream()


def _locked(fn):
    def run(*a, **k):
        with STREAM.lock:
            return fn(*a, **k)

    return run


def main() -> None:
    from fastmcp import FastMCP
    from starlette.responses import JSONResponse

    mcp = FastMCP("clbench")

    @mcp.tool()
    def get_brief() -> dict:
        """The stream's objective, reward and stages (one per session), and progress so far."""
        return _locked(STREAM.brief)()

    @mcp.tool()
    def start_session(session: int) -> dict:
        """Start session number `session` (1-based, given in your instructions). Returns the current query."""
        return _locked(STREAM.start_session)(session)

    @mcp.tool()
    def get_query() -> dict:
        """The current turn: prompt, JSON schema your response must follow, feedback on your previous response, and progress."""
        return _locked(STREAM.query_view)()

    @mcp.tool()
    def submit(response: dict | str) -> dict:
        """Submit your response to the current turn, as a JSON object matching response_schema. Returns the feedback and the next turn."""
        return _locked(STREAM.submit)(response)

    @mcp.custom_route("/healthz", methods=["GET"])
    async def healthz(_request):
        return JSONResponse({"ok": True})

    @mcp.custom_route("/finalize", methods=["POST"])
    async def finalize(_request):
        return JSONResponse(_locked(STREAM.finalize)())

    mcp.run(transport="http", host="0.0.0.0", port=PORT, path="/mcp")


if __name__ == "__main__":
    main()
