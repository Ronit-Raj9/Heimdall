#!/usr/bin/env python3
"""A small coding agent built with Pydantic AI.

It is written the way the Pydantic AI docs show: one Agent with instructions
and plain-function tools, an OpenAIChatModel on an OpenAIProvider, and
run_sync with UsageLimits(request_limit=...) as the step cap. The openai
client under the provider reads OPENAI_BASE_URL and OPENAI_API_KEY, so
pointing those at any OpenAI-compatible server (the bench points them at its
Bifrost gateway) is all it takes. Nothing here knows about Heimdall or
imports it.

OpenTelemetry: when the standard OTEL_EXPORTER_OTLP_TRACES_ENDPOINT is set,
the agent exports Pydantic AI's built-in spans (Agent.instrument_all) there
over OTLP/HTTP, as the Pydantic AI docs show for OpenTelemetry without
Logfire. The bench sets it to its per-run collector. Unset, nothing is sent.

Usage: fw-agent <task-file>
Env:   OPENAI_BASE_URL, OPENAI_API_KEY      the endpoint and its key
       AGENT_MODEL       model id sent to the endpoint (default gpt-5.4-mini)
       AGENT_MAX_STEPS   step cap: model requests (default 40)
       AGENT_SESSION_ID  optional; sent as the x-bf-session-id header
       BENCH_GATEWAY_HEADERS optional JSON object; extra headers on every model call
Prints the final answer last on stdout. Exit 0 when the run ends (solved or
not, step cap included), 1 when the model call fails.
"""
import json
import os
import subprocess
import sys

from openai import AsyncOpenAI
from pydantic_ai import Agent, UsageLimitExceeded, UsageLimits
from pydantic_ai.models.openai import OpenAIChatModel
from pydantic_ai.providers.openai import OpenAIProvider

WORKSPACE = "/workspace"
MODEL = os.environ.get("AGENT_MODEL", "gpt-5.4-mini")
MAX_STEPS = int(os.environ.get("AGENT_MAX_STEPS", "40"))
SESSION_ID = os.environ.get("AGENT_SESSION_ID", "")
COMMAND_TIMEOUT_S = 120
OUTPUT_MAX = 8000
SYSTEM = (
    "You are a coding agent working in the repository at /workspace. Use the tools to look at files, "
    "change them and run the tests. When the task is finished, reply with a short summary and no tool call."
)


def _inside(path):
    full = os.path.realpath(os.path.join(WORKSPACE, path or "."))
    if full != WORKSPACE and not full.startswith(WORKSPACE + "/"):
        raise ValueError(f"{path} is outside {WORKSPACE}")
    return full


def list_files(path: str = ".") -> str:
    """List the files under a directory of the repository (relative to /workspace)."""
    try:
        root = _inside(path)
        out = []
        for d, dirs, files in os.walk(root):
            dirs[:] = sorted(x for x in dirs if x != ".git")
            out += [os.path.relpath(os.path.join(d, f), WORKSPACE) for f in sorted(files)]
        return "\n".join(out[:500]) or "(no files)"
    except (OSError, ValueError) as e:
        return f"error: {e}"


def read_file(path: str) -> str:
    """Read a text file of the repository (path relative to /workspace)."""
    try:
        with open(_inside(path), errors="replace") as f:
            return f.read()[:20000]
    except (OSError, ValueError) as e:
        return f"error: {e}"


def write_file(path: str, content: str) -> str:
    """Write a whole file (path relative to /workspace), creating folders as needed."""
    try:
        full = _inside(path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w") as f:
            f.write(content)
        return f"wrote {len(content)} characters to {path}"
    except (OSError, ValueError) as e:
        return f"error: {e}"


def run_command(command: str) -> str:
    """Run a shell command in /workspace (for example the tests) and return its exit code and output."""
    env = {k: v for k, v in os.environ.items() if k != "OPENAI_API_KEY"}  # the key never reaches the model's commands
    try:
        p = subprocess.run(command or "true", shell=True, cwd=WORKSPACE, capture_output=True, text=True,
                           timeout=COMMAND_TIMEOUT_S, env=env)
        return f"exit {p.returncode}\n{p.stdout}{p.stderr}"[-OUTPUT_MAX:]
    except subprocess.TimeoutExpired:
        return f"timed out after {COMMAND_TIMEOUT_S}s"


def system_prompt():
    notes = os.path.join(WORKSPACE, "AGENTS.md")
    if os.path.isfile(notes):
        with open(notes, errors="replace") as f:
            return SYSTEM + "\n\nProject notes from AGENTS.md:\n" + f.read()
    return SYSTEM


def setup_tracing():
    """Export Pydantic AI's spans over OTLP/HTTP when the standard endpoint variable is set."""
    if not os.environ.get("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"):
        return None
    from opentelemetry import trace
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    provider = TracerProvider()  # service name and resource attributes come from OTEL_* variables
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)
    Agent.instrument_all()
    return provider


def main():
    with open(sys.argv[1] if len(sys.argv) > 1 else "/tmp/bench/instruction.md") as f:
        task = f.read()
    tracer = setup_tracing()
    headers = {"x-bf-session-id": SESSION_ID, "x-bf-session-affinity": "off"} if SESSION_ID else {}
    # BENCH_GATEWAY_HEADERS: the run's extra gateway headers (heimdall plugin fault plan and run tags, cache headers).
    headers.update(json.loads(os.environ.get("BENCH_GATEWAY_HEADERS") or "{}"))
    headers = headers or None
    # AsyncOpenAI reads OPENAI_BASE_URL and OPENAI_API_KEY; the headers only add the session id.
    model = OpenAIChatModel(MODEL, provider=OpenAIProvider(openai_client=AsyncOpenAI(default_headers=headers)))
    agent = Agent(model, instructions=system_prompt(), tools=[list_files, read_file, write_file, run_command])
    code = 0
    try:
        result = agent.run_sync(task, usage_limits=UsageLimits(request_limit=MAX_STEPS))
        final = result.output
    except UsageLimitExceeded:
        final = f"Stopped: reached the step cap of {MAX_STEPS} model requests without a final answer."
    except Exception as e:  # a failed model call ends the run; the bench reads the exit code
        print(f"Stopped: model call failed: {type(e).__name__}: {e}", file=sys.stderr)
        final, code = f"Stopped: model call failed: {type(e).__name__}", 1
    if tracer is not None:
        tracer.shutdown()  # flush spans before exit
    print(final)
    return code


if __name__ == "__main__":
    sys.exit(main())
