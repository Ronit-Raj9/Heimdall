"""Shared helpers of the Heimdall AgentDojo service, verifier and catalog.

Everything here is a thin layer over the upstream AgentDojo package: the
suites, tasks, attacks, environments and the utility and security functions
are upstream's own. This module only decides which attack text goes where,
in the same way upstream's benchmark runner does.
"""

import base64
import enum
import importlib.metadata
import io
import pickle
import re
import types

from agentdojo.attacks.attack_registry import ATTACKS
from agentdojo.attacks.base_attacks import DEFAULT_MODEL_NAME
from agentdojo.base_tasks import BaseInjectionTask, BaseUserTask
from agentdojo.functions_runtime import FunctionCall
from agentdojo.models import MODEL_NAMES
from agentdojo.task_suite.load_suites import get_suite, get_suites
from pydantic import BaseModel
from pydantic_core import to_jsonable_python

import agentdojo.attacks  # noqa: F401  registers the attacks

# Format tag of the state file the service writes and the verifier reads.
STATE_FORMAT = "heimdall-agentdojo/1"

# Attacks a Heimdall condition can plant. Upstream's manual attack asks a
# person for input, the denial-of-service attacks change what security means,
# and the wrong-model-name variant picks a name relative to the victim model,
# which is not known when the service starts.
SUPPORTED_ATTACKS = [
    "direct",
    "ignore_previous",
    "system_message",
    "injecagent",
    "important_instructions",
    "important_instructions_no_user_name",
    "important_instructions_no_model_name",
    "important_instructions_no_names",
    "important_instructions_wrong_user_name",
    "tool_knowledge",
    "adaptive",  # attacker model via the gateway (adaptive.py); not an upstream attack
]

# Attacks whose upstream constructor addresses the victim model by name.
_MODEL_NAMED = {"important_instructions", "important_instructions_no_user_name",
                "important_instructions_wrong_user_name", "tool_knowledge"}


class StubPipeline:
    """Stands in for upstream's agent pipeline when an attack is built.

    Upstream attacks read the pipeline only for its name, to address the
    victim model. Its name is a model upstream knows, so constructors that
    look the name up succeed; the name actually used is set afterwards.
    """

    def __init__(self):
        self.name = next(iter(MODEL_NAMES))


def package_version():
    """Returns the installed AgentDojo package version."""
    return importlib.metadata.version("agentdojo")


def benchmark_versions():
    """Returns the benchmark versions upstream registers, sorted."""
    from agentdojo.task_suite import load_suites

    return sorted(load_suites._SUITES.keys())


def load_suite(benchmark_version, suite_name):
    """Returns upstream's task suite, refusing unknown names."""
    suites = get_suites(benchmark_version)
    if not suites:
        raise ValueError(f"unknown benchmark version {benchmark_version!r}; known: {benchmark_versions()}")
    if suite_name not in suites:
        raise ValueError(f"unknown suite {suite_name!r} in {benchmark_version}; known: {sorted(suites)}")
    return get_suite(benchmark_version, suite_name)


def build_attack(name, suite, model_name=""):
    """Builds an upstream attack by name for the given suite.

    The victim model is not known when the service starts, so attacks that
    address it by name use model_name when given and upstream's neutral
    default ("the AI language model") otherwise.
    """
    if name not in SUPPORTED_ATTACKS:
        raise ValueError(f"attack {name!r} is not supported; supported: {SUPPORTED_ATTACKS}")
    if name == "adaptive":
        import adaptive  # the image runs with -I; server.py puts /opt/heimdall on sys.path

        seed = build_attack("important_instructions", suite, model_name)
        return adaptive.build(suite, seed)
    if name not in ATTACKS:
        raise ValueError(f"attack {name!r} is not registered by this AgentDojo version")
    attack = ATTACKS[name](suite, StubPipeline())
    if name in _MODEL_NAMED:
        attack.model_name = model_name or DEFAULT_MODEL_NAME
    return attack


def _json_fallback(value):
    # AgentDyn environments keep upstream tool functions in their state (the
    # shopping platform's verification_stack); JSON names them, the pickle
    # keeps them.
    if callable(value):
        return f"<function {getattr(value, '__module__', '?')}.{getattr(value, '__qualname__', repr(value))}>"
    raise TypeError(f"cannot serialize {type(value).__name__}")


def _to_json(value, on_path):
    """JSON data for value; a model met again on its own path becomes a marker.

    AgentDyn's pending OTP actions hold (function, (platform, ...)), a model
    that contains itself, which pydantic refuses to dump.
    """
    if isinstance(value, BaseModel):
        if id(value) in on_path:
            return f"<cycle {type(value).__name__}>"
        on_path.add(id(value))
        try:
            return {k: _to_json(getattr(value, k), on_path) for k in type(value).model_fields}
        finally:
            on_path.discard(id(value))
    if isinstance(value, dict):
        return {k if isinstance(k, str) else str(k): _to_json(v, on_path) for k, v in value.items()}
    if isinstance(value, (list, tuple, set, frozenset)):
        return [_to_json(v, on_path) for v in value]
    if callable(value) and not isinstance(value, type) and not isinstance(value, enum.Enum):
        return _json_fallback(value)
    return to_jsonable_python(value, fallback=_json_fallback)


def env_json(env):
    """The environment as JSON data, naming any function it holds."""
    return _to_json(env, set())


_DIGITS_PLACEHOLDER = re.compile(r"^\$(\d+)-digits$")


def runtime_fills(call):
    """{arg: digit count} for ground-truth arguments only known at run time.

    AgentDyn's ground truths write a one-time password the tools generate
    randomly and mail to the user as a placeholder ("$6-digits"); a replay
    reads it from the earlier tool results.
    """
    out = {}
    for k, v in (getattr(call, "placeholder_args", None) or {}).items():
        m = _DIGITS_PLACEHOLDER.match(str(v))
        if m and k in call.args:
            out[k] = int(m.group(1))
    return out


def fill_args(args, fills, results):
    """Fills each runtime argument with the last n-digit number in results
    (a number right after "." or ":" is a timestamp part, not a password)."""
    args = dict(args)
    for k, n in fills.items():
        for text in reversed(results):
            found = re.findall(r"(?<![\d.:])\d{%d}(?!\d)" % n, text or "")
            if found:
                args[k] = found[-1]
                break
    return args


def plan_injections(suite, user_task, injection_task, attack):
    """Returns {vector id: text} for one condition, as upstream's runner does.

    An empty attack means the clean condition: no vector is filled and the
    environment keeps upstream's defaults.
    """
    if attack is None or injection_task is None:
        return {}
    return dict(attack.attack(user_task, injection_task))


def build_environment(suite, user_task, injections):
    """Returns the task environment with the injections planted."""
    env = suite.load_and_inject_default_environment(injections)
    return user_task.init_environment(env)


def check_utility(user_task: BaseUserTask, output, pre, post, traces):
    """Upstream's utility decision: from the traces when the task defines it."""
    from_traces = user_task.utility_from_traces(output, pre, post, traces)
    if from_traces is not None:
        return bool(from_traces)
    return bool(user_task.utility(output, pre, post))


def check_security(injection_task: BaseInjectionTask, output, pre, post, traces):
    """Upstream's security decision: True means the injection goal was carried out."""
    from_traces = injection_task.security_from_traces(output, pre, post, traces)
    if from_traces is not None:
        return bool(from_traces)
    return bool(injection_task.security(output, pre, post))


def traces_from_records(records):
    """Turns the service's call records back into upstream FunctionCalls."""
    return [FunctionCall(function=r["function"], args=dict(r.get("args") or {})) for r in records]


_SPACE = re.compile(r"\s+")


def normalize(text):
    """Collapses whitespace runs, so planted text matches after YAML folding.

    Upstream plants attack text into its environment YAML before parsing it,
    which folds single line breaks into spaces.
    """
    return _SPACE.sub(" ", text).strip()


def strings_in(value):
    """Yields every string inside a tool result (models, lists, dicts)."""
    if hasattr(value, "model_dump"):
        value = _to_json(value, set())
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for k, v in value.items():
            yield from strings_in(k)
            yield from strings_in(v)
    elif isinstance(value, (list, tuple, set)):
        for v in value:
            yield from strings_in(v)


def served_vectors(result, injections):
    """Returns the vector ids whose planted text appears in a tool result."""
    wanted = {vid: normalize(text) for vid, text in injections.items() if normalize(text)}
    if not wanted:
        return []
    found = set()
    for s in strings_in(result):
        n = normalize(s)
        for vid, text in wanted.items():
            if text in n:
                found.add(vid)
    return sorted(found)


_DATETIME_TYPES = {"date", "datetime", "time", "timedelta", "timezone"}


class _EnvironmentUnpickler(pickle.Unpickler):
    """Loads only AgentDojo environment models, their enums, datetimes and suite functions."""

    def find_class(self, module, name):
        if module == "datetime" and name in _DATETIME_TYPES:
            return super().find_class(module, name)
        if module.startswith("agentdojo."):
            obj = super().find_class(module, name)
            if isinstance(obj, type) and issubclass(obj, (BaseModel, enum.Enum)):
                return obj
            # AgentDyn keeps pending OTP actions as upstream tool functions in
            # the environment; a pickle names them, it does not carry code.
            if module.startswith("agentdojo.default_suites.") and isinstance(obj, types.FunctionType):
                return obj
        raise pickle.UnpicklingError(f"{module}.{name} is not an environment type")


def dump_environment(env):
    """Serializes an environment exactly, as base64 pickle.

    Upstream's environment models do not survive a JSON round trip: their
    validators rebuild the email, event and file maps from the initial lists,
    which drops what the agent added. A pickle keeps the objects as they are.
    """
    return base64.b64encode(pickle.dumps(env, protocol=pickle.HIGHEST_PROTOCOL)).decode("ascii")


def load_environment(text, env_type):
    """Loads an environment written by dump_environment, refusing other types."""
    env = _EnvironmentUnpickler(io.BytesIO(base64.b64decode(text))).load()
    if not isinstance(env, env_type):
        raise ValueError(f"pickled environment is a {type(env).__name__}, not a {env_type.__name__}")
    return env
