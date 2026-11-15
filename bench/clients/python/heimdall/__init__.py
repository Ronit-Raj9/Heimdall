"""Heimdall bench client: drive the bench API from code and CI.

    from heimdall import Client
    c = Client("http://127.0.0.1:8090", token=os.environ.get("BENCH_WRITE_TOKEN"))
    exp = c.run_experiment(open("experiments/smoke.yaml").read())
    c.wait_for_experiment(exp["exp_id"])
    decision, action, policy = c.check(exp["exp_id"])

Every response carries the server ``mode`` (live, recorded or synthetic).
"""

from . import models
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

__version__ = "0.1.0"

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
    "__version__",
]
