"""Heimdall bench client: drive the bench API from code and CI.

    from heimdall import Client
    c = Client("http://127.0.0.1:8090", token=os.environ.get("BENCH_WRITE_TOKEN"))
    exp = c.run_experiment(open("experiments/smoke.yaml").read())
    c.wait_for_experiment(exp["exp_id"])
    decision, action, policy = c.check(exp["exp_id"])

Every response carries the server ``mode`` (live, recorded or synthetic).

To benchmark an agent that runs in your own process (pull mode), serve it:

    import heimdall
    heimdall.serve("support-bot", lambda turn: my_agent(turn.instruction),
                   url="https://heimdall.example.com", token=os.environ["HEIMDALL_TOKEN"])

For the public, versioned API (``/api/v1``) use :class:`heimdall.v1.V1Client`:

    from heimdall import V1Client
    hd = V1Client("https://heimdall.example.com", token=os.environ["HEIMDALL_TOKEN"])
    result = hd.evaluate_and_wait({"name": "nightly", "mode": "score",
        "arms": [{"label": "A", "agent": "support-bot", "model": "gpt-5.1"}], "pack": "support"})
"""

from . import events
from . import models
from . import v1
from .client import (
    API_PREFIX,
    ROUTES,
    TERMINAL_STATES,
    CheckResult,
    Client,
    HeimdallError,
    HeimdallTimeout,
    ModeError,
    RawResponse,
    mode_of,
)
from .v1 import AgentReportCard, EvaluationResult, ReportCard, V1Client
from .faults import FaultSession, FaultControlError, InjectedFaultError, InjectedTimeoutError, hook
from .serve import Answer, ToolCall, Turn
from .serve import serve  # heimdall.serve(agent, fn): run your own agent function (pull mode, docs/pull-agents.md)

__version__ = "0.2.0"

__all__ = [
    "API_PREFIX",
    "ROUTES",
    "TERMINAL_STATES",
    "CheckResult",
    "Client",
    "HeimdallError",
    "HeimdallTimeout",
    "ModeError",
    "RawResponse",
    "mode_of",
    "models",
    "v1",
    "V1Client",
    "ReportCard",
    "AgentReportCard",
    "EvaluationResult",
    "__version__",
    "FaultSession", "FaultControlError", "InjectedFaultError", "InjectedTimeoutError", "hook",
    "serve", "Answer", "ToolCall", "Turn",
]
