"""heimdall.serve (pull mode): answer shapes, the safe repository unpack,
and a serve loop against a fake server."""

import io
import json
import os
import sys
import tarfile
import tempfile
import threading
import unittest
from unittest import mock
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from heimdall.serve import Answer, PullError, ToolCall, safe_extract, serve, to_answer  # noqa: E402


def tarball(members):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for name, data, kind in members:
            info = tarfile.TarInfo(name)
            if kind == "sym":
                info.type, info.linkname = tarfile.SYMTYPE, "/etc/passwd"
                tar.addfile(info)
            else:
                info.size = len(data)
                tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


class AnswerTest(unittest.TestCase):
    def test_shapes(self):
        self.assertEqual(to_answer("hi"), {"text": "hi"})
        self.assertEqual(to_answer(None), {"text": ""})
        a = to_answer(Answer(text="x", tool_calls=[ToolCall("lookup", {"id": 1}, id="c1")], patch="diff", usage={"input_tokens": 1}))
        self.assertEqual(a["tool_calls"], [{"id": "c1", "name": "lookup", "arguments": {"id": 1}}])
        self.assertEqual(a["patch"], "diff")
        d = to_answer({"text": "y", "tool_calls": [ToolCall("t")]})
        self.assertTrue(d["tool_calls"][0]["id"].startswith("call_"))
        with self.assertRaises(TypeError):
            to_answer(42)


class ExtractTest(unittest.TestCase):
    def test_refuses_escapes_and_links(self):
        data = tarball([("ok/a.txt", b"a", "file"), ("../evil.txt", b"x", "file"), ("link", b"", "sym")])
        with tempfile.TemporaryDirectory() as d:
            dest = os.path.join(d, "dest")
            self.assertEqual(safe_extract(data, dest), [os.path.join("ok", "a.txt")])
            self.assertFalse(os.path.exists(os.path.join(d, "evil.txt")))
            self.assertFalse(os.path.lexists(os.path.join(dest, "link")))


class Fake(BaseHTTPRequestHandler):
    turns, answers, status = [], [], 200

    def log_message(self, *a):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        if Fake.status != 200:
            self.send_response(Fake.status)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":{"message":"bad token"}}')
            return
        if self.path == "/api/v1/pull/next":
            assert self.headers["Authorization"] == "Bearer tok"
            if Fake.turns:
                doc = Fake.turns.pop(0)
                out = json.dumps(doc).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(out)))
                self.end_headers()
                self.wfile.write(out)
            else:
                self.send_response(204)
                self.end_headers()
            return
        Fake.answers.append((self.path, body))
        self.send_response(204)
        self.end_headers()


class ServeTest(unittest.TestCase):
    def setUp(self):
        Fake.turns = [{"id": "turn_1", "agent": "bot", "payload": {"kind": "task", "instruction": "2+2"}},
                      {"id": "turn_2", "agent": "bot", "payload": {"kind": "conversation", "message": "boom"}}]
        Fake.answers, Fake.status = [], 200
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), Fake)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = "http://127.0.0.1:%d" % self.srv.server_port

    def tearDown(self):
        self.srv.shutdown()
        self.srv.server_close()

    def test_answers_and_errors(self):
        def fn(turn):
            if turn.kind == "conversation":
                raise ValueError("cannot")
            return "4"
        self.assertEqual(serve("bot", fn, url=self.url, token="tok", max_turns=2), 2)
        got = dict(Fake.answers)
        self.assertEqual(got["/api/v1/pull/turns/turn_1/answer"], {"answer": {"text": "4"}})
        self.assertEqual(got["/api/v1/pull/turns/turn_2/answer"], {"error": "ValueError: cannot"})

    def test_bad_token_stops(self):
        Fake.status = 401
        with self.assertRaises(PullError):
            serve("bot", lambda t: "x", url=self.url, token="tok")

    def test_needs_url_and_token(self):
        # Empty url and token fall back to HEIMDALL_URL and HEIMDALL_TOKEN,
        # which the contract test sets: clear them, or serve runs forever.
        with mock.patch.dict(os.environ, {"HEIMDALL_URL": "", "HEIMDALL_TOKEN": ""}):
            with self.assertRaises(ValueError):
                serve("bot", lambda t: "x", url="", token="")


if __name__ == "__main__":
    unittest.main()
