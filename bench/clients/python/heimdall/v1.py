"""Typed client for the public Heimdall API v1 (``/api/v1``, docs/api-v1.md).

Standard library only. ``V1Client`` covers the stable routes: agents and
their versions, compatibility, benchmarks, evaluations, estimates, report
cards, the agent leaderboard, runs and trace export. ``evaluate_and_wait``
starts an evaluation, waits for it and returns its report card as typed
dataclasses.

    from heimdall.v1 import V1Client
    hd = V1Client("https://heimdall.example.com", token=os.environ["HEIMDALL_TOKEN"])
    result = hd.evaluate_and_wait({
        "name": "nightly", "mode": "score",
        "arms": [{"label": "A", "agent": "support-bot", "model": "gpt-5.1"}],
        "pack": "support",
    })
    print(result.report.score("A"))

Every JSON answer still carries the server ``mode``; ``require_live=True``
refuses anything but ``live``.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Mapping, Optional, Tuple, TypedDict, Union, cast

from .client import Client, HeimdallError, HeimdallTimeout, RawResponse, _seg

API_V1_PREFIX = "/api/v1"
EVALUATION_TERMINAL_STATES = ("done", "failed", "cancelled")

# Every v1 route the client calls, as (method, OpenAPI path). The tests check
# each one against GET /api/v1/openapi.json of a real server.
V1_ROUTES: Tuple[Tuple[str, str], ...] = (
    ("GET", "/api/v1/agents"),
    ("POST", "/api/v1/agents"),
    ("GET", "/api/v1/agents/templates"),
    ("GET", "/api/v1/agents/{id}"),
    ("PUT", "/api/v1/agents/{id}"),
    ("POST", "/api/v1/agents/{id}/test"),
    ("GET", "/api/v1/agents/{id}/versions"),
    ("POST", "/api/v1/agents/{id}/versions"),
    ("GET", "/api/v1/agents/{id}/versions/{n}"),
    ("GET", "/api/v1/agents/{id}/compatibility"),
    ("GET", "/api/v1/agents/{id}/report-card"),
    ("GET", "/api/v1/benchmarks"),
    ("GET", "/api/v1/benchmarks/catalog"),
    ("GET", "/api/v1/benchmarks/packs"),
    ("GET", "/api/v1/benchmarks/{id}/tasks/{task}"),
    ("GET", "/api/v1/evaluations"),
    ("POST", "/api/v1/evaluations"),
    ("POST", "/api/v1/evaluations/estimate"),
    ("GET", "/api/v1/evaluations/{id}"),
    ("POST", "/api/v1/evaluations/{id}/cancel"),
    ("GET", "/api/v1/evaluations/{id}/report"),
    ("POST", "/api/v1/estimate"),
    ("GET", "/api/v1/experiments/{id}/triage"),
    ("GET", "/api/v1/leaderboard/agents"),
    ("GET", "/api/v1/runs"),
    ("GET", "/api/v1/runs/{id}"),
    ("GET", "/api/v1/runs/{id}/steps"),
    ("GET", "/api/v1/runs/{id}/trace"),
    ("GET", "/api/v1/runs/{id}/otlp"),
)


# ---- Request and resource shapes ----


class EvaluationArm(TypedDict, total=False):
    label: str
    agent: str
    version: int
    model: str
    provider: str


class EvaluationSpec(TypedDict, total=False):
    """Body of POST /api/v1/evaluations: one agent (or two arms for an A/B) across benchmarks."""

    name: str
    mode: str  # score | versions | models | opponent
    arms: List[EvaluationArm]
    benchmarks: List[str]
    pack: str
    tasks_per_benchmark: int
    trials: int
    faults: List[str]
    budget_usd: float


class EvaluationChild(TypedDict, total=False):
    benchmark: str
    experiment_id: str
    status: str
    skipped: str


class Evaluation(TypedDict, total=False):
    mode: str
    id: str
    org: str
    spec: EvaluationSpec
    status: str  # queued | running | done | failed | cancelled
    children: List[EvaluationChild]
    created_at: str
    created_by: str


class EvaluationList(TypedDict, total=False):
    mode: str
    evaluations: List[Evaluation]


class CostRange(TypedDict, total=False):
    low: float
    high: float
    known: bool


class EvaluationEstimate(TypedDict, total=False):
    mode: str
    runs: int
    est_cost_usd: CostRange
    est_minutes: CostRange
    per_benchmark: List[Dict[str, Any]]
    notes: List[str]


class AgentVersionEntry(TypedDict, total=False):
    version: int
    hash: str
    note: str
    created_at: str
    created_by: str
    runs: int


class AgentVersionList(TypedDict, total=False):
    mode: str
    agent: str
    builtin: bool
    versions: List[AgentVersionEntry]


class AgentCreated(TypedDict, total=False):
    """Answer of POST /api/v1/agents."""

    mode: str
    id: str
    path: str
    files: List[str]
    yaml: str
    dry_run: bool


class AgentConfigDoc(TypedDict, total=False):
    """Answer of GET and PUT /api/v1/agents/{id}: the agent file and its hash."""

    mode: str
    id: str
    path: str
    yaml: str
    sha256: str
    fields: Dict[str, Any]


class AgentTestResult(TypedDict, total=False):
    """Answer of POST /api/v1/agents/{id}/test. ``ok`` false says why in ``error``."""

    mode: str
    ok: bool
    adapter: str
    url: str
    status: int
    latency_ms: int
    answer: str
    error: str
    requests: List[Dict[str, Any]]
    raw_response: str
    reads: str
    trace: List[Dict[str, Any]]
    notes: List[str]


class AgentVersionDoc(TypedDict, total=False):
    mode: str
    version: int
    hash: str
    yaml: str


class BenchmarkCompatibility(TypedDict, total=False):
    id: str
    ok: bool
    requires: List[str]
    missing: List[str]
    reason: str


class AgentCompatibility(TypedDict, total=False):
    mode: str
    agent: str
    capabilities: List[str]
    defaulted: bool
    benchmarks: List[BenchmarkCompatibility]


class LeaderboardCapability(TypedDict, total=False):
    score: float
    ci_low: float
    ci_high: float
    runs: int
    benchmarks: List[str]


class LeaderboardRow(TypedDict, total=False):
    agent: str
    version: int
    model: str
    label: str
    config_hash: str
    baseline: bool
    capabilities: Dict[str, LeaderboardCapability]
    cost_usd: Optional[float]
    runs: int
    last_evaluation: str
    last_experiment: str


class AgentLeaderboard(TypedDict, total=False):
    mode: str
    capabilities: List[str]
    models: List[str]
    capability: str
    model: str
    rows: List[LeaderboardRow]
    generated_at: str


class RunRow(TypedDict, total=False):
    run_id: str
    exp_id: str
    pair_id: str
    suite: str
    task: str
    arm: str
    condition: str
    trial: int
    counted: bool
    sealed: bool
    outcome: str
    y: Any
    cost_usd: Any
    started: str
    sealed_at: str
    error: str


class RunList(TypedDict, total=False):
    mode: str
    runs: List[RunRow]
    total: int
    limit: int
    offset: int
    facets: Dict[str, List[str]]


class TriageGroup(TypedDict, total=False):
    pattern: str
    label: str
    description: str
    per_arm: Dict[str, Dict[str, Any]]
    runs: Dict[str, List[str]]


class Triage(TypedDict, total=False):
    mode: str
    exp_id: str
    groups: List[TriageGroup]


# ---- Report card models ----


def _f(v: Any) -> float:
    return float(v) if isinstance(v, (int, float)) else 0.0


def _i(v: Any) -> int:
    return int(v) if isinstance(v, (int, float)) else 0


def _opt_f(v: Any) -> Optional[float]:
    return float(v) if isinstance(v, (int, float)) else None


def _strs(v: Any) -> List[str]:
    return [str(x) for x in v] if isinstance(v, list) else []


def _map(v: Any) -> Dict[str, Any]:
    return v if isinstance(v, dict) else {}


@dataclass(frozen=True)
class ReportArm:
    label: str
    agent: str
    model: str
    version: int = 0

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "ReportArm":
        return cls(label=str(d.get("label", "")), agent=str(d.get("agent", "")), model=str(d.get("model", "")), version=_i(d.get("version")))


@dataclass(frozen=True)
class Metrics:
    """One arm's numbers: verified success rate with its 95% interval, and means per run."""

    score: float
    ci_low: float
    ci_high: float
    runs: int
    cost_usd: float = 0.0
    tokens: float = 0.0
    steps: float = 0.0
    tool_calls: float = 0.0
    wall_s: float = 0.0

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "Metrics":
        return cls(
            score=_f(d.get("score")), ci_low=_f(d.get("ci_low")), ci_high=_f(d.get("ci_high")), runs=_i(d.get("runs")),
            cost_usd=_f(d.get("cost_usd")), tokens=_f(d.get("tokens")), steps=_f(d.get("steps")),
            tool_calls=_f(d.get("tool_calls")), wall_s=_f(d.get("wall_s")),
        )


def _arms(d: Any) -> Dict[str, Metrics]:
    return {str(k): Metrics.from_dict(v) for k, v in _map(d).items() if isinstance(v, dict)}


@dataclass(frozen=True)
class BenchmarkVerdict:
    """A/B verdict on one benchmark (two-arm evaluations only)."""

    winner: str
    delta: float
    significant: bool
    summary: str
    tasks: int
    p_value: Optional[float] = None
    p_value_holm: Optional[float] = None

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "BenchmarkVerdict":
        return cls(
            winner=str(d.get("winner", "")), delta=_f(d.get("delta")), significant=bool(d.get("significant")),
            summary=str(d.get("summary", "")), tasks=_i(d.get("tasks")),
            p_value=_opt_f(d.get("p_value")), p_value_holm=_opt_f(d.get("p_value_holm")),
        )


@dataclass(frozen=True)
class OverallVerdict:
    winner: str
    wins: Dict[str, int]
    delta: float
    significant: bool
    tasks: int
    benchmarks: int
    summary: str
    p_value: Optional[float] = None

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "OverallVerdict":
        return cls(
            winner=str(d.get("winner", "")), wins={str(k): _i(v) for k, v in _map(d.get("wins")).items()},
            delta=_f(d.get("delta")), significant=bool(d.get("significant")), tasks=_i(d.get("tasks")),
            benchmarks=_i(d.get("benchmarks")), summary=str(d.get("summary", "")), p_value=_opt_f(d.get("p_value")),
        )


@dataclass(frozen=True)
class CapabilityScore:
    capability: str
    benchmarks: List[str]
    arms: Dict[str, Metrics]

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "CapabilityScore":
        return cls(capability=str(d.get("capability", "")), benchmarks=_strs(d.get("benchmarks")), arms=_arms(d.get("arms")))


@dataclass(frozen=True)
class BenchmarkScore:
    benchmark: str
    capability: str
    experiment_id: str
    arms: Dict[str, Metrics]
    verdict: Optional[BenchmarkVerdict] = None

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "BenchmarkScore":
        v = d.get("verdict")
        return cls(
            benchmark=str(d.get("benchmark", "")), capability=str(d.get("capability", "")),
            experiment_id=str(d.get("experiment_id", "")), arms=_arms(d.get("arms")),
            verdict=BenchmarkVerdict.from_dict(v) if isinstance(v, dict) else None,
        )


@dataclass(frozen=True)
class FaultScore:
    score: float
    drop: float
    runs: int


@dataclass(frozen=True)
class Reliability:
    """How each arm held up under one fault, against its clean score."""

    fault: str
    benchmarks: List[str]
    arms: Dict[str, FaultScore]

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "Reliability":
        arms = {
            str(k): FaultScore(score=_f(v.get("score")), drop=_f(v.get("drop")), runs=_i(v.get("runs")))
            for k, v in _map(d.get("arms")).items() if isinstance(v, dict)
        }
        return cls(fault=str(d.get("fault", "")), benchmarks=_strs(d.get("benchmarks")), arms=arms)


@dataclass(frozen=True)
class Safety:
    attack_resisted: Dict[str, float] = field(default_factory=dict)
    cheat_rate: Dict[str, float] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "Safety":
        return cls(
            attack_resisted={str(k): _f(v) for k, v in _map(d.get("attack_resisted")).items()},
            cheat_rate={str(k): _f(v) for k, v in _map(d.get("cheat_rate")).items()},
        )


@dataclass(frozen=True)
class ReportCard:
    """The report card of one evaluation (GET /api/v1/evaluations/{id}/report)."""

    evaluation: str
    status: str
    arms: List[ReportArm]
    capabilities: List[CapabilityScore]
    benchmarks: List[BenchmarkScore]
    reliability: List[Reliability]
    safety: Safety
    overall: Optional[OverallVerdict]
    server_mode: str
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "ReportCard":
        overall = d.get("overall")
        return cls(
            evaluation=str(d.get("evaluation", "")), status=str(d.get("status", "")),
            arms=[ReportArm.from_dict(a) for a in d.get("arms") or [] if isinstance(a, dict)],
            capabilities=[CapabilityScore.from_dict(c) for c in d.get("capabilities") or [] if isinstance(c, dict)],
            benchmarks=[BenchmarkScore.from_dict(b) for b in d.get("benchmarks") or [] if isinstance(b, dict)],
            reliability=[Reliability.from_dict(r) for r in d.get("reliability") or [] if isinstance(r, dict)],
            safety=Safety.from_dict(_map(d.get("safety"))),
            overall=OverallVerdict.from_dict(overall) if isinstance(overall, dict) else None,
            server_mode=str(d.get("mode", "")), raw=dict(d),
        )

    @property
    def live(self) -> bool:
        return self.server_mode == "live"

    def benchmark(self, name: str) -> Optional[BenchmarkScore]:
        return next((b for b in self.benchmarks if b.benchmark == name), None)

    def score(self, arm: str) -> Optional[float]:
        """The arm's verified success rate over every benchmark, weighted by runs; None without runs."""
        runs = 0
        total = 0.0
        for b in self.benchmarks:
            m = b.arms.get(arm)
            if m is not None and m.runs > 0:
                runs += m.runs
                total += m.score * m.runs
        return total / runs if runs else None


@dataclass(frozen=True)
class AgentReportCard:
    agent: str
    version: int
    model: str
    report: Optional[ReportCard]

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> "AgentReportCard":
        r = d.get("report")
        rep = None
        if isinstance(r, dict):
            rep = ReportCard.from_dict(dict(r, mode=d.get("mode", "")))
        return cls(agent=str(d.get("agent", "")), version=_i(d.get("version")), model=str(d.get("model", "")), report=rep)


@dataclass(frozen=True)
class EvaluationResult:
    """What evaluate_and_wait returns: the final evaluation and its report card."""

    evaluation: Evaluation
    report: ReportCard

    @property
    def status(self) -> str:
        return str(self.evaluation.get("status", ""))

    @property
    def succeeded(self) -> bool:
        return self.status == "done"


# ---- Client ----


def _yaml_id(doc: str) -> str:
    """The top-level ``id:`` of an agent YAML (standard library only, no YAML parser)."""
    for line in doc.splitlines():
        if line.startswith("id:"):
            v = line[3:].split("#", 1)[0].strip().strip("'\"")
            if v:
                return v
    raise ValueError("the YAML has no top-level id: pass agent=")


class _V1Transport(Client):
    api_prefix = API_V1_PREFIX


class V1Client:
    """Client for the public Heimdall API v1.

    ``base_url`` is the server root. ``token`` is an organization API token
    (hosted; its scope must cover the calls: read, evaluate or admin) or the
    local write token. Reads retry on connection errors and 502/504; writes
    never retry.
    """

    def __init__(
        self,
        base_url: str,
        token: Optional[str] = None,
        timeout: float = 30.0,
        require_live: bool = False,
        read_retries: int = 2,
        user_agent: str = "heimdall-python/0.2.0",
    ):
        self._t = _V1Transport(base_url, token=token, timeout=timeout, require_live=require_live, read_retries=read_retries, user_agent=user_agent)

    @property
    def base_url(self) -> str:
        return self._t.base_url

    @property
    def last_mode(self) -> Optional[str]:
        return self._t.last_mode

    def openapi(self) -> Dict[str, Any]:
        """The OpenAPI 3.1 document of API v1."""
        return self._t._get("/openapi.json")

    # Agents

    def agents(self) -> Dict[str, Any]:
        return self._t._get("/agents")

    def agent_templates(self) -> Dict[str, Any]:
        """The agent templates and their params (for ``create_agent(template=...)``)."""
        return self._t._get("/agents/templates")

    def get_agent(self, agent: str) -> AgentConfigDoc:
        """The agent's YAML, form fields and sha256."""
        return cast(AgentConfigDoc, self._t._get("/agents/%s" % _seg(agent)))

    def create_agent(
        self,
        yaml: Optional[str] = None,
        template: Optional[str] = None,
        id: Optional[str] = None,
        params: Optional[Mapping[str, str]] = None,
        model: Optional[Mapping[str, str]] = None,
        dry_run: bool = False,
    ) -> AgentCreated:
        """Create an agent from its YAML, or from a template and its params.

        A taken name raises HeimdallError with status 409; ``push_agent``
        creates or updates. ``dry_run`` validates and writes nothing.
        """
        if (yaml is None) == (template is None):
            raise ValueError("pass yaml or template, not both")
        body: Dict[str, Any] = {"dry_run": dry_run} if dry_run else {}
        if yaml is not None:
            body["yaml"] = yaml
            if id:
                body["id"] = id
        else:
            if not id:
                raise ValueError("a template needs an id")
            body.update({"template": template, "id": id})
            if params:
                body["params"] = dict(params)
            if model:
                body["model"] = dict(model)
        return cast(AgentCreated, self._t._post("/agents", body))

    def update_agent(self, agent: str, yaml: str, if_match: Optional[str] = None) -> AgentConfigDoc:
        """Replace the agent's YAML. ``if_match`` (a sha256 from get_agent) refuses a file changed since."""
        body: Dict[str, Any] = {"yaml": yaml}
        if if_match:
            body["if_match"] = if_match
        _, raw, _ = self._t._request("PUT", "/agents/%s" % _seg(agent), None, body)
        return cast(AgentConfigDoc, self._t._json(raw, "PUT", "/agents/%s" % _seg(agent)))

    def push_agent(self, yaml: str, agent: Optional[str] = None, note: str = "") -> Dict[str, Any]:
        """Create the agent, or update it when it exists, then record a version.

        ``agent`` is the id in the YAML (read from it when not given). Returns
        {"agent", "created", "version"}: created is True for a new agent;
        version is the answer of create_agent_version.
        """
        if agent is None:
            agent = _yaml_id(yaml)
        created = True
        try:
            self.create_agent(yaml=yaml, id=agent)
        except HeimdallError as e:
            if e.status != 409:
                raise
            created = False
            self.update_agent(agent, yaml)
        return {"agent": agent, "created": created, "version": self.create_agent_version(agent, note)}

    def test_agent(self, agent: str) -> AgentTestResult:
        """Send the saved agent one sample task. Check ``ok``; ``error`` says what failed."""
        return cast(AgentTestResult, self._t._post("/agents/%s/test" % _seg(agent), {}))

    def agent_versions(self, agent: str) -> AgentVersionList:
        return cast(AgentVersionList, self._t._get("/agents/%s/versions" % _seg(agent)))

    def agent_version(self, agent: str, version: int) -> AgentVersionDoc:
        return cast(AgentVersionDoc, self._t._get("/agents/%s/versions/%d" % (_seg(agent), int(version))))

    def create_agent_version(self, agent: str, note: str = "") -> Dict[str, Any]:
        """Register the agent's current config as a new version (no-op when unchanged: created false)."""
        return self._t._post("/agents/%s/versions" % _seg(agent), {"note": note})

    def compatibility(self, agent: str) -> AgentCompatibility:
        """Which benchmarks the agent can run, and what it lacks for the rest."""
        return cast(AgentCompatibility, self._t._get("/agents/%s/compatibility" % _seg(agent)))

    def agent_report_card(self, agent: str, version: Optional[int] = None, model: Optional[str] = None) -> AgentReportCard:
        q = {"version": None if version is None else str(version), "model": model}
        return AgentReportCard.from_dict(self._t._get("/agents/%s/report-card" % _seg(agent), q))

    # Benchmarks

    def benchmarks(self) -> Dict[str, Any]:
        return self._t._get("/benchmarks")

    def benchmark_catalog(self) -> Dict[str, Any]:
        return self._t._get("/benchmarks/catalog")

    def benchmark_packs(self) -> Dict[str, Any]:
        return self._t._get("/benchmarks/packs")

    def benchmark_task(self, benchmark: str, task: str) -> Dict[str, Any]:
        return self._t._get("/benchmarks/%s/tasks/%s" % (_seg(benchmark), _seg(task)))

    # Evaluations

    def evaluations(self) -> EvaluationList:
        return cast(EvaluationList, self._t._get("/evaluations"))

    def evaluation(self, evaluation_id: str) -> Evaluation:
        return cast(Evaluation, self._t._get("/evaluations/%s" % _seg(evaluation_id)))

    def create_evaluation(self, spec: EvaluationSpec) -> Evaluation:
        """Start an evaluation (202): one experiment per benchmark."""
        return cast(Evaluation, self._t._post("/evaluations", dict(spec)))

    def cancel_evaluation(self, evaluation_id: str) -> Evaluation:
        return cast(Evaluation, self._t._post("/evaluations/%s/cancel" % _seg(evaluation_id), None))

    def estimate_evaluation(self, spec: EvaluationSpec) -> EvaluationEstimate:
        """Runs, cost and minutes before launching; unknown prices stay unknown (known false)."""
        return cast(EvaluationEstimate, self._t._post("/evaluations/estimate", dict(spec)))

    def estimate(self, experiment: Union[Mapping[str, Any], str]) -> Dict[str, Any]:
        """Forecast one experiment's cost and time from sealed history."""
        return self._t._post("/estimate", experiment if isinstance(experiment, str) else dict(experiment))

    def report(self, evaluation_id: str) -> ReportCard:
        return ReportCard.from_dict(self._t._get("/evaluations/%s/report" % _seg(evaluation_id)))

    def triage(self, experiment_id: str) -> Triage:
        """Failure groups of one benchmark's experiment, per arm, with the runs showing each."""
        return cast(Triage, self._t._get("/experiments/%s/triage" % _seg(experiment_id)))

    # Leaderboard and runs

    def leaderboard(self, capability: Optional[str] = None, model: Optional[str] = None) -> AgentLeaderboard:
        return cast(AgentLeaderboard, self._t._get("/leaderboard/agents", {"capability": capability, "model": model}))

    def runs(self, **filters: Union[str, int, None]) -> RunList:
        """Runs newest first; filters: exp, suite, arm, task, condition, outcome, track, limit, offset."""
        return cast(RunList, self._t._get("/runs", {k: (None if v is None else str(v)) for k, v in filters.items()}))

    def run(self, run_id: str) -> Dict[str, Any]:
        return self._t._get("/runs/%s" % _seg(run_id))

    def run_steps(self, run_id: str) -> Dict[str, Any]:
        return self._t._get("/runs/%s/steps" % _seg(run_id))

    def trace(self, run_id: str, format: str = "json") -> RawResponse:
        """Export a run's trace: json (steps and events), jsonl (steps) or otlp."""
        return self._t._raw("/runs/%s/trace" % _seg(run_id), {"format": format})

    def otlp(self, run_id: str, payloads: bool = False) -> RawResponse:
        """A sealed run as an OpenTelemetry trace; payloads adds redacted, capped tool payloads."""
        return self._t._raw("/runs/%s/otlp" % _seg(run_id), {"payloads": "true" if payloads else None})

    # Helpers

    def wait_for_evaluation(
        self,
        evaluation_id: str,
        timeout: float = 6 * 3600.0,
        interval: float = 10.0,
        on_status: Optional[Callable[[Evaluation], None]] = None,
    ) -> Evaluation:
        """Poll until the evaluation is done, failed or cancelled; HeimdallTimeout after ``timeout`` seconds."""
        deadline = time.monotonic() + timeout
        last_status = "unknown"
        while True:
            left = deadline - time.monotonic()
            if left <= 0:
                raise HeimdallTimeout(0, "evaluation %s still %s after %.0fs" % (evaluation_id, last_status, timeout), "timeout", self.last_mode)
            ev = cast(Evaluation, self._t._get("/evaluations/%s" % _seg(evaluation_id), deadline=deadline))
            last_status = str(ev.get("status", "unknown"))
            if on_status is not None:
                on_status(ev)
            if last_status in EVALUATION_TERMINAL_STATES:
                return ev
            left = deadline - time.monotonic()
            if left <= 0:
                continue
            time.sleep(min(interval, left))

    def evaluate_and_wait(
        self,
        spec: EvaluationSpec,
        timeout: float = 6 * 3600.0,
        interval: float = 10.0,
        on_status: Optional[Callable[[Evaluation], None]] = None,
        cancel_on_timeout: bool = True,
    ) -> EvaluationResult:
        """Start an evaluation, wait for it to finish and return it with its report card.

        On timeout the evaluation is cancelled (``cancel_on_timeout``) and
        HeimdallTimeout is raised. A failed or cancelled evaluation still
        returns: check ``result.succeeded``.
        """
        created = self.create_evaluation(spec)
        eid = str(created.get("id", ""))
        if not eid:
            raise HeimdallError(0, "server returned no evaluation id", "decode", self.last_mode, "POST", "/evaluations")
        try:
            final = self.wait_for_evaluation(eid, timeout=timeout, interval=interval, on_status=on_status)
        except HeimdallTimeout:
            if cancel_on_timeout:
                try:
                    self.cancel_evaluation(eid)
                except HeimdallError:
                    pass
            raise
        return EvaluationResult(evaluation=final, report=self.report(eid))
