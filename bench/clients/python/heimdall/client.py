"""Thin client over the Heimdall bench HTTP API (``/api/bench``).

Standard library only. Every JSON method returns the server's object as a
dict, which always carries the server ``mode`` (live, recorded or
synthetic); raw downloads return :class:`RawResponse`, whose ``mode`` comes
from the ``X-Bench-Mode`` header. Pass ``require_live=True`` to make every
call raise :class:`ModeError` unless the server says ``live``, so a CI job can
never mistake a recorded demo for a real result.
"""

from __future__ import annotations

import json
import socket
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any, Callable, Dict, Iterator, List, Mapping, Optional, Sequence, Tuple, Union, cast

from . import models

API_PREFIX = "/api/bench"
MODE_HEADER = "X-Bench-Mode"
TERMINAL_STATES = ("done", "failed", "cancelled")

# Every route the client calls, as (method, OpenAPI path). The contract smoke
# test checks each one against GET /api/bench/openapi.json.
ROUTES: Tuple[Tuple[str, str], ...] = (
    ("GET", "/api/bench/health"),
    ("GET", "/api/bench/doctor"),
    ("GET", "/api/bench/catalog"),
    ("GET", "/api/bench/openapi.json"),
    ("GET", "/api/bench/suites"),
    ("GET", "/api/bench/suites/{id}/tasks/{task}"),
    ("GET", "/api/bench/agents"),
    ("GET", "/api/bench/experiments"),
    ("POST", "/api/bench/experiments"),
    ("POST", "/api/bench/experiments/plan"),
    ("POST", "/api/bench/experiments/{id}/start"),
    ("GET", "/api/bench/experiments/{id}"),
    ("GET", "/api/bench/experiments/{id}/events"),
    ("GET", "/api/bench/experiments/{id}/grid"),
    ("GET", "/api/bench/experiments/{id}/verdict"),
    ("GET", "/api/bench/experiments/{id}/diagnostics"),
    ("GET", "/api/bench/experiments/{id}/triage"),
    ("GET", "/api/bench/runs/{id}"),
    ("GET", "/api/bench/runs/{id}/events"),
    ("GET", "/api/bench/runs/{id}/diff"),
    ("GET", "/api/bench/runs/{id}/receipts"),
    ("GET", "/api/bench/runs/{id}/files/{name}"),
    ("GET", "/api/bench/pairs/{id}"),
    ("GET", "/api/bench/leaderboard"),
    ("POST", "/api/bench/audit"),
    ("GET", "/api/bench/releases"),
    ("POST", "/api/bench/releases"),
    ("GET", "/api/bench/releases/{id}"),
    ("GET", "/api/bench/releases/{id}/evidence"),
    ("GET", "/api/bench/imports"),
    ("GET", "/api/bench/workers"),
)

Experiment = Union[Mapping[str, Any], str]
"""An experiment as a dict (sent as JSON) or as YAML text (sent as application/yaml)."""


class HeimdallError(Exception):
    """A request the server refused or could not answer.

    ``status`` is the HTTP status (0 when no answer arrived), ``type`` the
    server's error type (``not_found``, ``unauthorized``, ``unavailable`` ...),
    ``message`` the server's message and ``mode`` the server mode, when known.
    """

    def __init__(self, status: int, message: str, type: str = "", mode: Optional[str] = None, method: str = "", url: str = ""):
        self.status = status
        self.type = type
        self.message = message
        self.mode = mode
        self.method = method
        self.url = url
        where = ("%s %s: " % (method, url)) if method else ""
        super().__init__("%s%s%s" % (where, ("%d %s: " % (status, type)) if status else "", message))


class HeimdallTimeout(HeimdallError):
    """A request or a wait ran out of time."""


class ModeError(HeimdallError):
    """The server answered in a mode the client was told not to accept."""


@dataclass(frozen=True)
class RawResponse:
    """A non-JSON answer: a patch, a run file or an evidence zip."""

    data: bytes
    content_type: str
    mode: str

    def text(self) -> str:
        return self.data.decode("utf-8", errors="replace")


@dataclass(frozen=True)
class CheckResult:
    """The release gate's answer for one experiment.

    Unpacks as ``decision, action, policy_result = client.check(exp_id)``.
    ``blocked`` is true when the experiment's release policy says block.
    """

    decision: str
    action: str
    policy_result: Optional[models.PolicyResult]
    mode: str
    verdict: models.VerdictPayloadResponse

    def __iter__(self) -> Iterator[Any]:
        return iter((self.decision, self.action, self.policy_result))

    @property
    def policy_decision(self) -> Optional[str]:
        """allow, warn or block; None when the experiment has no policy."""
        return self.policy_result["decision"] if self.policy_result else None

    @property
    def blocked(self) -> bool:
        return self.policy_decision == "block"

    @property
    def live(self) -> bool:
        return self.mode == "live"


def mode_of(response: Union[Mapping[str, Any], RawResponse]) -> str:
    """The server mode of any response this client returned."""
    if isinstance(response, RawResponse):
        return response.mode
    return str(response.get("mode", ""))


class Client:
    """Heimdall bench API client.

    ``base_url`` is the server root, for example ``http://127.0.0.1:8090``.
    ``token`` is sent as ``Authorization: Bearer <token>``: the server
    requires its write token (``BENCH_WRITE_TOKEN``) on every POST route; read
    routes need none. ``timeout`` bounds each request in seconds. Reads retry
    ``read_retries`` times on connection errors and 502/504; writes are never
    retried.
    """

    api_prefix = API_PREFIX  # heimdall.v1 serves the same transport under /api/v1

    def __init__(
        self,
        base_url: str,
        token: Optional[str] = None,
        timeout: float = 30.0,
        require_live: bool = False,
        read_retries: int = 2,
        user_agent: str = "heimdall-bench-python",
    ):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.require_live = require_live
        self.read_retries = read_retries
        self.user_agent = user_agent
        self.last_mode: Optional[str] = None

    # Health and catalog

    def health(self) -> models.HealthResponse:
        return cast(models.HealthResponse, self._get("/health"))

    def doctor(self) -> models.DoctorResponse:
        return cast(models.DoctorResponse, self._get("/doctor"))

    def catalog(self) -> models.CatalogResponse:
        return cast(models.CatalogResponse, self._get("/catalog"))

    def openapi(self) -> Dict[str, Any]:
        """The server's OpenAPI 3.1 description; its mode is under x-bench-mode."""
        return self._get("/openapi.json")

    # Suites and agents

    def suites(self) -> models.SuiteListResponse:
        return cast(models.SuiteListResponse, self._get("/suites"))

    def suite_task(self, suite: str, task: str) -> models.SuiteTaskDetailResponse:
        return cast(models.SuiteTaskDetailResponse, self._get("/suites/%s/tasks/%s" % (_seg(suite), _seg(task))))

    def agents(self) -> models.AgentListResponse:
        return cast(models.AgentListResponse, self._get("/agents"))

    # Experiments

    def experiments(self) -> models.ExperimentListResponse:
        return cast(models.ExperimentListResponse, self._get("/experiments"))

    def experiment(self, exp_id: str) -> models.ExperimentStatusResponse:
        """Status of one experiment: state, slot progress, spend, analysis id."""
        return cast(models.ExperimentStatusResponse, self._get("/experiments/%s" % _seg(exp_id)))

    def plan(self, experiment: Experiment) -> models.ExperimentPlanResponse:
        """Size an experiment; nothing runs. Planner errors come back in ``errors``."""
        return cast(models.ExperimentPlanResponse, self._post("/experiments/plan", experiment))

    def create_experiment(self, experiment: Experiment) -> models.ExperimentCreatedResponse:
        """Plan, freeze and create the experiment. Nothing runs until start."""
        return cast(models.ExperimentCreatedResponse, self._post("/experiments", experiment))

    def start_experiment(self, exp_id: str) -> models.ExperimentStatusResponse:
        """Start a created experiment's runs (202 with the status)."""
        return cast(models.ExperimentStatusResponse, self._post("/experiments/%s/start" % _seg(exp_id), None))

    def run_experiment(self, experiment: Experiment) -> models.ExperimentStatusResponse:
        """Create and start in one call; returns the started status."""
        created = self.create_experiment(experiment)
        return self.start_experiment(created["exp_id"])

    def events(self, exp_id: str, since: int = 0) -> models.EventsPollResponse:
        """Live events after ``since`` and the experiment status (polling form)."""
        return cast(models.EventsPollResponse, self._get("/experiments/%s/events" % _seg(exp_id), {"since": str(since)}))

    def grid(self, exp_id: str) -> models.GridResponse:
        return cast(models.GridResponse, self._get("/experiments/%s/grid" % _seg(exp_id)))

    def verdict(self, exp_id: str, grade_source: Optional[str] = None) -> models.VerdictPayloadResponse:
        """Verdict, gate checklist, config diff and release policy result."""
        return cast(models.VerdictPayloadResponse, self._get("/experiments/%s/verdict" % _seg(exp_id), {"grade_source": grade_source}))

    def analysis(self, exp_id: str, grade_source: Optional[str] = None) -> models.DiagnosticsPayloadResponse:
        """Diagnostics table, meters and per-arm metrics."""
        return cast(models.DiagnosticsPayloadResponse, self._get("/experiments/%s/diagnostics" % _seg(exp_id), {"grade_source": grade_source}))

    diagnostics = analysis

    def triage(self, exp_id: str) -> models.TriageResponse:
        return cast(models.TriageResponse, self._get("/experiments/%s/triage" % _seg(exp_id)))

    # Runs

    def runs(self, exp_id: str) -> List[models.GridCell]:
        """Every slot of an experiment that has a run, from the grid.

        The API has no run list route; the grid holds each slot's run id.
        """
        cells: List[models.GridCell] = []
        for row in self.grid(exp_id)["rows"]:
            cells.extend(c for c in row["cells"] if c.get("run_id"))
        return cells

    def run(self, run_id: str) -> models.RunDetailResponse:
        return cast(models.RunDetailResponse, self._get("/runs/%s" % _seg(run_id)))

    def run_events(self, run_id: str) -> models.RunEventsResponse:
        return cast(models.RunEventsResponse, self._get("/runs/%s/events" % _seg(run_id)))

    def run_diff(self, run_id: str) -> models.RunDiffResponse:
        return cast(models.RunDiffResponse, self._get("/runs/%s/diff" % _seg(run_id)))

    def run_patch(self, run_id: str) -> RawResponse:
        """The raw patch as text/plain."""
        return self._raw("/runs/%s/diff" % _seg(run_id), {"format": "text"})

    def run_receipts(self, run_id: str) -> models.RunReceiptsResponse:
        return cast(models.RunReceiptsResponse, self._get("/runs/%s/receipts" % _seg(run_id)))

    def run_file(self, run_id: str, name: str) -> RawResponse:
        """One allowlisted run file (run.json, metrics.json, diff.patch ...)."""
        return self._raw("/runs/%s/files/%s" % (_seg(run_id), _seg(name)))

    def pair(self, pair_id: str) -> models.PairResponse:
        return cast(models.PairResponse, self._get("/pairs/%s" % _seg(pair_id)))

    # Leaderboard, audit, releases

    def leaderboard(self, **params: Union[str, int, None]) -> models.LeaderboardResponse:
        """Leaderboard; params: suite, condition, scope, synthetic, grade_source, k."""
        return cast(models.LeaderboardResponse, self._get("/leaderboard", {k: (None if v is None else str(v)) for k, v in params.items()}))

    def audit(self, rows: Sequence[Mapping[str, Any]], alpha: Optional[float] = None) -> models.VerdictResponse:
        """Verdict for a published per-task table of two arms."""
        body: Dict[str, Any] = {"rows": list(rows)}
        if alpha is not None:
            body["alpha"] = alpha
        return cast(models.VerdictResponse, self._post("/audit", body))

    def releases(self) -> models.ReleaseListResponse:
        return cast(models.ReleaseListResponse, self._get("/releases"))

    def release(self, release_id: str) -> models.ReleaseRecordResponse:
        return cast(models.ReleaseRecordResponse, self._get("/releases/%s" % _seg(release_id)))

    def create_release(
        self, exp_id: str, approver: str, note: Optional[str] = None, override: Optional[str] = None, analysis_id: Optional[str] = None
    ) -> models.ReleaseRecordResponse:
        """Record a release decision. ``override`` is required to release against a block."""
        body = {"exp_id": exp_id, "approver": approver, "note": note, "override": override, "analysis_id": analysis_id}
        return cast(models.ReleaseRecordResponse, self._post("/releases", {k: v for k, v in body.items() if v is not None}))

    def release_evidence(self, release_id: str) -> RawResponse:
        """The release's evidence pack (zip bytes)."""
        return self._raw("/releases/%s/evidence" % _seg(release_id))

    # Imports and workers

    def imports(self) -> models.ImportListResponse:
        return cast(models.ImportListResponse, self._get("/imports"))

    def workers(self) -> models.WorkersListResponse:
        """Remote workers; ``enabled`` is false when worker mode is off."""
        return cast(models.WorkersListResponse, self._get("/workers"))

    # CI helpers

    def wait_for_experiment(
        self,
        exp_id: str,
        timeout: float = 3600.0,
        interval: float = 5.0,
        on_status: Optional[Callable[[models.ExperimentStatusResponse], None]] = None,
    ) -> models.ExperimentStatusResponse:
        """Poll until the experiment is done, failed or cancelled.

        Returns the final status (check its ``state``). Raises
        :class:`HeimdallTimeout` when ``timeout`` seconds pass first.
        """
        deadline = time.monotonic() + timeout
        last: Optional[models.ExperimentStatusResponse] = None

        def late() -> HeimdallTimeout:
            state = last["state"] if last is not None else "unknown"
            return HeimdallTimeout(0, "experiment %s still %s after %.0fs" % (exp_id, state, timeout), "timeout", self.last_mode)

        # Each status request is limited to the time left, and an answer
        # after the deadline is a timeout, never a late success (QC2-20-07).
        while True:
            if deadline - time.monotonic() <= 0:
                raise late()
            try:
                status = cast(models.ExperimentStatusResponse, self._get("/experiments/%s" % _seg(exp_id), deadline=deadline))
            except HeimdallTimeout:
                if time.monotonic() >= deadline:
                    raise late() from None
                raise
            if time.monotonic() > deadline:
                raise late()
            last = status
            if on_status is not None:
                on_status(status)
            if status["state"] in TERMINAL_STATES:
                return status
            left = deadline - time.monotonic()
            if left <= 0:
                raise late()
            time.sleep(min(interval, left))

    def check(self, exp_id: str, grade_source: Optional[str] = None) -> CheckResult:
        """The release decision for CI: verdict decision, action and policy result."""
        v = self.verdict(exp_id, grade_source)
        verdict = v["verdict"]
        return CheckResult(decision=verdict["decision"], action=verdict["action"], policy_result=v.get("policy"), mode=v["mode"], verdict=v)

    # Transport

    def _get(self, path: str, query: Optional[Mapping[str, Optional[str]]] = None, deadline: Optional[float] = None) -> Dict[str, Any]:
        _, body, _ = self._request("GET", path, query, deadline=deadline)
        return self._json(body, "GET", path)

    def _post(self, path: str, payload: Any) -> Dict[str, Any]:
        _, body, _ = self._request("POST", path, None, payload)
        return self._json(body, "POST", path)

    def _put(self, path: str, payload: Any) -> Dict[str, Any]:
        _, body, _ = self._request("PUT", path, None, payload)
        return self._json(body, "PUT", path)

    def _delete(self, path: str) -> Dict[str, Any]:
        _, body, _ = self._request("DELETE", path)
        return self._json(body, "DELETE", path)

    def _raw(self, path: str, query: Optional[Mapping[str, Optional[str]]] = None) -> RawResponse:
        _, body, headers = self._request("GET", path, query)
        mode = headers.get(MODE_HEADER, "") or ""
        self._check_mode(mode, "GET", path)
        return RawResponse(data=body, content_type=headers.get("Content-Type", "") or "", mode=mode)

    def _json(self, body: bytes, method: str, path: str) -> Dict[str, Any]:
        if not body.strip():
            return {"mode": self.last_mode or ""}
        try:
            data = json.loads(body.decode("utf-8"))
        except ValueError as err:
            raise HeimdallError(0, "response is not JSON: %s" % err, "decode", self.last_mode, method, path) from err
        if not isinstance(data, dict):
            raise HeimdallError(0, "response is not a JSON object", "decode", self.last_mode, method, path)
        mode = data.get("mode") or data.get("x-bench-mode") or self.last_mode or ""
        self._check_mode(str(mode), method, path)
        return data

    def _check_mode(self, mode: str, method: str, path: str) -> None:
        if self.require_live and mode != "live":
            raise ModeError(0, "server mode is %r, not live: refusing to treat it as a real result" % mode, "mode", mode, method, path)

    def _request(
        self, method: str, path: str, query: Optional[Mapping[str, Optional[str]]] = None, payload: Any = None, deadline: Optional[float] = None
    ) -> Tuple[int, bytes, Mapping[str, str]]:
        """Send one request. ``deadline`` (time.monotonic seconds), when
        given, caps every attempt's timeout and the waits between them."""
        url = self.base_url + self.api_prefix + path
        q = {k: v for k, v in (query or {}).items() if v is not None and v != ""}
        if q:
            url += "?" + urllib.parse.urlencode(q)
        headers = {"Accept": "application/json", "User-Agent": self.user_agent}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        data: Optional[bytes] = None
        if method in ("POST", "PUT"):
            if isinstance(payload, str):
                data, headers["Content-Type"] = payload.encode("utf-8"), "application/yaml"
            elif payload is not None:
                data, headers["Content-Type"] = json.dumps(payload).encode("utf-8"), "application/json"
            else:
                data = b""
        attempts = 1 + (self.read_retries if method == "GET" else 0)
        for attempt in range(attempts):
            timeout = self.timeout
            if deadline is not None:
                timeout = min(timeout, deadline - time.monotonic())
                if timeout <= 0:
                    raise HeimdallTimeout(0, "no answer within %.0fs" % self.timeout, "timeout", None, method, path)
            last = attempt == attempts - 1 or (deadline is not None and time.monotonic() + 0.5 * (attempt + 1) >= deadline)
            req = urllib.request.Request(url, data=data, method=method, headers=headers)
            try:
                with urllib.request.urlopen(req, timeout=timeout) as resp:
                    body = resp.read()
                    self.last_mode = resp.headers.get(MODE_HEADER) or self.last_mode
                    return resp.status, body, resp.headers
            except urllib.error.HTTPError as err:
                body = err.read()
                err.close()
                self.last_mode = err.headers.get(MODE_HEADER) or self.last_mode
                if err.code in (502, 504) and not last:
                    time.sleep(0.5 * (attempt + 1))
                    continue
                raise _http_error(err.code, body, err.headers.get(MODE_HEADER), method, path) from None
            except (socket.timeout, TimeoutError) as err:
                if last:
                    raise HeimdallTimeout(0, "no answer within %.0fs" % self.timeout, "timeout", None, method, path) from err
            except urllib.error.URLError as err:
                if isinstance(err.reason, (socket.timeout, TimeoutError)):
                    if last:
                        raise HeimdallTimeout(0, "no answer within %.0fs" % self.timeout, "timeout", None, method, path) from err
                elif last:
                    raise HeimdallError(0, "cannot reach %s: %s" % (self.base_url, err.reason), "connection", None, method, path) from err
            time.sleep(0.5 * (attempt + 1))
        raise HeimdallError(0, "request not sent", "connection", None, method, path)  # unreachable: the loop returns or raises


def _http_error(status: int, body: bytes, mode_header: Optional[str], method: str, path: str) -> HeimdallError:
    """Build the error from the server's {mode, error: {message, type}} body."""
    message, etype, mode = body.decode("utf-8", errors="replace").strip(), "", mode_header
    try:
        data = json.loads(body.decode("utf-8"))
        if isinstance(data, dict) and isinstance(data.get("error"), dict):
            message = str(data["error"].get("message", message))
            etype = str(data["error"].get("type", ""))
            mode = data.get("mode") or mode
    except ValueError:
        pass
    return HeimdallError(status, message or "HTTP %d" % status, etype, mode, method, path)


def _seg(value: str) -> str:
    """Quote one path segment."""
    return urllib.parse.quote(value, safe="")
