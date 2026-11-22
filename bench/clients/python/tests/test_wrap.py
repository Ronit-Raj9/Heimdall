"""Unit tests of heimdall.wrap and heimdall.cli (stdlib only).

Framework objects are stand-ins whose classes claim the framework's module
name, so no framework needs to be installed.

Run from bench/clients/python: python3 -m unittest tests.test_wrap
"""

import json
import os
import sys
import tempfile
import threading
import types
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from heimdall import cli, wrap  # noqa: E402


class FakeTurn:
    def __init__(self, instruction="", message=None, kind="task", messages=None, session_id="s1", turn_id="t1"):
        self.instruction, self.message, self.kind = instruction, message, kind
        self.messages = messages or []
        self.session_id, self.id = session_id, turn_id


def framework_class(module, name, **methods):
    cls = type(name, (), methods)
    cls.__module__ = module
    return cls


class WrapTests(unittest.TestCase):
    def test_langgraph_graph_gets_messages(self):
        seen = {}

        def invoke(self, state, config=None):
            seen["state"], seen["config"] = state, config
            return {"messages": state["messages"] + [types.SimpleNamespace(content=[{"type": "text", "text": "the reply"}])]}

        Graph = framework_class("langgraph.graph.state", "CompiledStateGraph", invoke=invoke, checkpointer=None)
        fn, kind = wrap.wrap(Graph())
        self.assertEqual(kind, "langgraph")
        turn = FakeTurn(kind="conversation", message="and now?", messages=[
            {"role": "user", "content": "hi"}, {"role": "assistant", "content": "hello"}, {"role": "user", "content": "and now?"}])
        self.assertEqual(fn(turn), "the reply")
        self.assertEqual(len(seen["state"]["messages"]), 3)
        self.assertEqual(seen["config"]["configurable"]["thread_id"], "s1")

    def test_stateful_langgraph_gets_latest_only(self):
        seen = {}

        def invoke(self, state, config=None):
            seen["n"] = len(state["messages"])
            return {"messages": [{"role": "assistant", "content": "ok"}]}

        Graph = framework_class("langgraph.pregel", "Pregel", invoke=invoke, checkpointer=object())
        fn, _ = wrap.wrap(Graph())
        fn(FakeTurn(kind="conversation", message="b", messages=[{"role": "user", "content": "a"}, {"role": "user", "content": "b"}]))
        self.assertEqual(seen["n"], 1)

    def test_crewai_kickoff_inputs(self):
        seen = {}

        def kickoff(self, inputs=None):
            seen.update(inputs)
            return types.SimpleNamespace(raw="crew answer")

        Crew = framework_class("crewai.crew", "Crew", kickoff=kickoff)
        fn, kind = wrap.wrap(Crew())
        self.assertEqual(kind, "crewai")
        self.assertEqual(fn(FakeTurn(instruction="refund order 7")), "crew answer")
        self.assertEqual(seen["input"], "refund order 7")
        self.assertEqual(seen["question"], "refund order 7")

    def test_smolagents_resets_per_session(self):
        calls = []

        def run(self, task, reset=True):
            calls.append((task, reset))
            return "done"

        Agent = framework_class("smolagents.agents", "CodeAgent", run=run)
        fn, kind = wrap.wrap(Agent())
        self.assertEqual(kind, "smolagents")
        fn(FakeTurn(instruction="a", session_id="s1"))
        fn(FakeTurn(kind="conversation", message="b", session_id="s1"))
        fn(FakeTurn(instruction="c", session_id="s2"))
        self.assertEqual(calls, [("a", True), ("b", False), ("c", True)])

    def test_dspy_module_uses_signature_fields(self):
        sig = types.SimpleNamespace(input_fields={"question": 1}, output_fields={"answer": 1})

        def call(self, **kw):
            return types.SimpleNamespace(answer="42 because " + kw["question"])

        Mod = framework_class("dspy.predict.chain_of_thought", "ChainOfThought", __call__=call, signature=sig)
        fn, kind = wrap.wrap(Mod())
        self.assertEqual(kind, "dspy")
        self.assertEqual(fn(FakeTurn(instruction="why")), "42 because why")

    def test_pydantic_ai_output(self):
        Agent = framework_class("pydantic_ai.agent", "Agent", run_sync=lambda self, text: types.SimpleNamespace(output="py " + text))
        fn, kind = wrap.wrap(Agent())
        self.assertEqual(kind, "pydantic-ai")
        self.assertEqual(fn(FakeTurn(instruction="hi")), "py hi")

    def test_user_package_named_agents_is_not_openai(self):
        Agent = framework_class("agents.support", "Agent", __call__=lambda self, text: "mine " + text)
        fn, kind = wrap.wrap(Agent())
        self.assertEqual(kind, "function")

    def test_plain_functions_by_signature(self):
        fn, kind = wrap.wrap(lambda text: text.upper())
        self.assertEqual(kind, "function")
        self.assertEqual(fn(FakeTurn(instruction="hi")), "HI")

        def by_messages(messages):
            return str(len(messages))

        fn, _ = wrap.wrap(by_messages)
        self.assertEqual(fn(FakeTurn(instruction="x")), "1")

        async def by_turn(turn):
            return {"text": turn.instruction + "!"}

        fn, _ = wrap.wrap(by_turn)
        self.assertEqual(fn(FakeTurn(instruction="ok")), {"text": "ok!"})

    def test_unknown_object_says_what_it_wraps(self):
        with self.assertRaises(TypeError) as cm:
            wrap.wrap(42)
        self.assertIn("LangGraph", str(cm.exception))

    def test_load_object_from_file(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "app"))
            with open(os.path.join(d, "app", "helper.py"), "w") as f:
                f.write("PREFIX = 'got: '\n")
            with open(os.path.join(d, "app", "bot.py"), "w") as f:
                f.write("from helper import PREFIX\n\ndef answer(text):\n    return PREFIX + text\n")
            obj = wrap.load_object("app/bot.py:answer", d)
            self.assertEqual(obj("x"), "got: x")
            with self.assertRaises(ValueError):
                wrap.load_object("app/bot.py", d)


class CliTests(unittest.TestCase):
    def test_resolve_reads_the_login_file(self):
        with tempfile.TemporaryDirectory() as d:
            os.environ["HEIMDALL_CONFIG_DIR"] = d
            old = {k: os.environ.pop(k, None) for k in ("HEIMDALL_URL", "HEIMDALL_TOKEN")}
            try:
                with open(os.path.join(d, "credentials.json"), "w") as f:
                    json.dump({"default": "https://h.example", "servers": {"https://h.example": {"token": "hm_x"}}}, f)
                self.assertEqual(cli.resolve(None), ("https://h.example", "hm_x"))
                os.environ["HEIMDALL_TOKEN"] = "hm_env"
                self.assertEqual(cli.resolve("https://h.example/"), ("https://h.example", "hm_env"))
            finally:
                del os.environ["HEIMDALL_CONFIG_DIR"]
                for k, v in old.items():
                    os.environ.pop(k, None)
                    if v is not None:
                        os.environ[k] = v

    def test_agent_name(self):
        self.assertEqual(cli.agent_name_from("bots/Support_Bot.py:graph"), "support_bot")
        self.assertEqual(cli.agent_name_from("myapp.triage:crew"), "triage")

    def test_url_target_points_to_the_binary(self):
        self.assertEqual(cli.main(["serve", "http://bot.internal:8080"]), 2)


try:
    from heimdall import serve as _serve_mod  # noqa: F401  (Y11 pull mode)
    HAVE_SERVE = hasattr(sys.modules.get("heimdall.serve"), "serve")
except ImportError:
    HAVE_SERVE = False


@unittest.skipUnless(HAVE_SERVE, "heimdall.serve (pull mode, lane Y11) is not in this tree")
class ServeEndToEnd(unittest.TestCase):
    def test_cli_serves_a_wrapped_function(self):
        answers = {}
        turns = [{"id": "t1", "agent": "bot", "payload": {"kind": "task", "instruction": "ping"}}]

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_POST(self):  # noqa: N802
                n = int(self.headers.get("Content-Length") or 0)
                body = json.loads(self.rfile.read(n) or b"{}")
                if self.path == "/api/v1/pull/next":
                    if turns:
                        out = json.dumps(turns.pop(0)).encode()
                        self.send_response(200)
                        self.send_header("Content-Type", "application/json")
                        self.end_headers()
                        self.wfile.write(out)
                    else:
                        self.send_response(204)
                        self.end_headers()
                    return
                answers[self.path] = body
                self.send_response(204)
                self.end_headers()

        srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        try:
            with tempfile.TemporaryDirectory() as d:
                with open(os.path.join(d, "bot.py"), "w") as f:
                    f.write("def answer(text):\n    return 'pong to ' + text\n")
                os.environ["HEIMDALL_TOKEN"] = "hm_t"
                code = cli.main(["serve", "bot.py:answer", "--agent", "bot", "--max-turns", "1", "--dir", d,
                                 "--url", "http://127.0.0.1:%d" % srv.server_address[1]])
            self.assertEqual(code, 0)
            self.assertEqual(answers["/api/v1/pull/turns/t1/answer"]["answer"]["text"], "pong to ping")
        finally:
            srv.shutdown()
            os.environ.pop("HEIMDALL_TOKEN", None)


if __name__ == "__main__":
    unittest.main()
