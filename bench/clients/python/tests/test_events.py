"""Unit tests of heimdall.events against a local collector and a temporary file (stdlib only).

Run from bench/clients/python: python3 -B -m unittest
"""

import json
import os
import re
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Dict, List
from unittest import mock

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from heimdall import events  # noqa: E402

RFC3339_MS = re.compile(r"^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$")


class Collector:
    """A fake event collector: records POST bodies and answers 202, or a fixed status."""

    def __init__(self, status: int = 202) -> None:
        self.status = status
        self.requests: List[Dict[str, Any]] = []
        col = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args: Any) -> None:
                pass

            def do_POST(self) -> None:
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                col.requests.append({"path": self.path, "content_type": self.headers.get("Content-Type"), "body": body})
                lines = [ln for ln in body.decode().splitlines() if ln.strip()]
                out = json.dumps({"accepted": len(lines), "dropped": 0}).encode()
                self.send_response(col.status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = "http://127.0.0.1:%d/events" % self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def events(self) -> List[Dict[str, Any]]:
        out = []
        for r in self.requests:
            out.extend(json.loads(ln) for ln in r["body"].decode().splitlines() if ln.strip())
        return out

    def close(self) -> None:
        self.server.shutdown()
        self.server.server_close()


def read_lines(path: str) -> List[Dict[str, Any]]:
    if not os.path.exists(path):
        return []
    with open(path, encoding="utf-8") as f:
        return [json.loads(ln) for ln in f if ln.strip()]


class EventsTest(unittest.TestCase):
    def setUp(self) -> None:
        events._reset()
        self.tmp = tempfile.TemporaryDirectory()
        self.file = os.path.join(self.tmp.name, "events", "events.jsonl")
        env = {k: v for k, v in os.environ.items() if k not in (events.ENV_URL, events.ENV_FILE)}
        self.env = mock.patch.dict(os.environ, env, clear=True)
        self.env.start()

    def tearDown(self) -> None:
        self.env.stop()
        self.tmp.cleanup()
        events._reset()

    def test_noop_when_unset(self) -> None:
        self.assertFalse(events.enabled())
        with mock.patch("urllib.request.urlopen") as urlopen, mock.patch("os.open") as osopen:
            eid = events.emit("plan", "p", output="x")
            with events.step("tool_call", "t") as s:
                s.output = "y"
        urlopen.assert_not_called()
        osopen.assert_not_called()
        self.assertRegex(eid, r"^[0-9a-f]{32}$")
        self.assertRegex(s.id, r"^[0-9a-f]{32}$")
        self.assertNotEqual(eid, s.id)
        self.assertEqual(events.emit("plan", id="mine"), "mine")

    def test_file(self) -> None:
        os.environ[events.ENV_FILE] = self.file
        plan = events.emit("plan", "fix", output="read, edit, test", attributes={"turn": 1, "ok": True, "tags": ["a"]})
        events.emit("tool_call", "run_tests", parent_id=plan, tool_call_id="call_1", input={"cmd": "pytest"})
        got = read_lines(self.file)
        self.assertEqual(len(got), 2)
        self.assertEqual(got[0]["type"], "plan")
        self.assertEqual(got[0]["id"], plan)
        self.assertEqual(got[0]["output"], "read, edit, test")
        self.assertEqual(got[0]["attributes"], {"turn": 1, "ok": True, "tags": '["a"]'})
        self.assertRegex(got[0]["start_time"], RFC3339_MS)
        self.assertNotIn("end_time", got[0])
        self.assertEqual(got[1]["parent_id"], plan)
        self.assertEqual(got[1]["tool_call_id"], "call_1")
        self.assertEqual(got[1]["input"], {"cmd": "pytest"})
        self.assertNotIn("status", got[1])

    def test_url(self) -> None:
        col = Collector()
        self.addCleanup(col.close)
        os.environ[events.ENV_URL] = col.url
        os.environ[events.ENV_FILE] = self.file
        eid = events.emit("message", "final", output="done", status="ok")
        self.assertEqual(len(col.requests), 1)
        self.assertEqual(col.requests[0]["path"], "/events")
        self.assertEqual(col.requests[0]["content_type"], "application/x-ndjson")
        self.assertTrue(col.requests[0]["body"].endswith(b"\n"))
        got = col.events()
        self.assertEqual(got[0]["id"], eid)
        self.assertEqual(got[0]["status"], "ok")
        self.assertEqual(read_lines(self.file), [], "a delivered POST must not also write the file")

    def test_url_failure_falls_back_to_file(self) -> None:
        col = Collector(status=500)
        self.addCleanup(col.close)
        os.environ[events.ENV_URL] = col.url
        os.environ[events.ENV_FILE] = self.file
        events.emit("error", "boom")
        events.emit("error", "again")
        self.assertEqual(len(col.requests), 1, "a failed URL is skipped for a while")
        self.assertEqual([e["name"] for e in read_lines(self.file)], ["boom", "again"])

    def test_unreachable_url_never_raises(self) -> None:
        col = Collector()
        url = col.url
        col.close()
        os.environ[events.ENV_URL] = url
        self.assertTrue(events.emit("plan", "x"))
        blocker = os.path.join(self.tmp.name, "blocker")
        with open(blocker, "w"):
            pass
        os.environ[events.ENV_FILE] = os.path.join(blocker, "x.jsonl")
        self.assertTrue(events.emit("plan", "y"))  # the parent is a file: the append fails silently

    def test_step(self) -> None:
        os.environ[events.ENV_FILE] = self.file
        with events.step("subagent_start", "researcher") as outer:
            with events.step("tool_call", "search", parent_id=outer.id, input={"q": "heimdall"}) as inner:
                inner.output = ["r1", "r2"]
                inner.attributes["hits"] = 2
        with self.assertRaises(ValueError):
            with events.step("tool_call", "write", parent_id=outer.id):
                raise ValueError("disk full")
        got = read_lines(self.file)
        self.assertEqual([e["name"] for e in got], ["search", "researcher", "write"])
        search, researcher, write = got
        self.assertEqual(search["parent_id"], outer.id)
        self.assertEqual(search["id"], inner.id)
        self.assertEqual(search["output"], ["r1", "r2"])
        self.assertEqual(search["status"], "ok")
        self.assertEqual(search["attributes"], {"hits": 2})
        for e in got:
            self.assertRegex(e["start_time"], RFC3339_MS)
            self.assertRegex(e["end_time"], RFC3339_MS)
            self.assertLessEqual(e["start_time"], e["end_time"])
        self.assertEqual(researcher["id"], outer.id)
        self.assertEqual(write["status"], "error")
        self.assertEqual(write["output"], "ValueError: disk full")

    def test_cap(self) -> None:
        os.environ[events.ENV_FILE] = self.file
        events.emit("tool_result", "big", output="é" * 10000, input={"blob": "x" * 20000})
        events.emit("tool_result", "obj", output=object())
        got = read_lines(self.file)
        self.assertIsInstance(got[0]["output"], str)
        self.assertLessEqual(len(got[0]["output"].encode()), events.MAX_TEXT)
        self.assertIsInstance(got[0]["input"], str, "an input over the cap is sent as cut text")
        self.assertLessEqual(len(got[0]["input"].encode()), events.MAX_TEXT)
        self.assertTrue(got[0]["input"].startswith('{"blob": "xxx'))
        self.assertIn("object object at", got[1]["output"])

    def test_stdout_line(self) -> None:
        line = events.stdout_line("plan", "p", id="e1", output={"steps": 3})
        self.assertTrue(line.startswith("@@heimdall-event "))
        self.assertNotIn("\n", line)
        ev = json.loads(line[len("@@heimdall-event "):])
        self.assertEqual(ev["id"], "e1")
        self.assertEqual(ev["output"], {"steps": 3})
        self.assertRegex(ev["start_time"], RFC3339_MS)

    def test_threads_never_interleave(self) -> None:
        os.environ[events.ENV_FILE] = self.file

        def work(n: int) -> None:
            for i in range(50):
                events.emit("custom", "t%d" % n, output="z" * 3000, attributes={"i": i})

        threads = [threading.Thread(target=work, args=(n,)) for n in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(len(read_lines(self.file)), 200)


if __name__ == "__main__":
    unittest.main()
