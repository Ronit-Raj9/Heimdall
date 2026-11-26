"""Finished checks of the remote_http launcher's polls.

Run from bench/: python3 -m unittest adapters/test_remotehttp_w4b08.py
The launcher is loaded as a module; its network calls are replaced by
fakes, so nothing leaves the process.
"""

import contextlib
import importlib.util
import io
import json
import os
import types
import unittest
import urllib.parse

_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "remotehttp.py")
_spec = importlib.util.spec_from_file_location("remotehttp_w4b08", _PATH)
rh = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(rh)
rh.time.sleep = lambda s: None


class _Resp:
    def __init__(self, status, headers=None):
        self.status = status
        self._headers = headers or {}

    def getheader(self, name):
        return self._headers.get(name)


def _mapped(answers, **fields):
    """Runs poll_mapped against answers (one JSON document per poll, the
    last one repeated). Returns (exit code or None, answer, report, stdout)."""
    state = {"n": 0}

    def send(kind, req):
        doc = answers[min(state["n"], len(answers) - 1)]
        state["n"] += 1
        return _Resp(200), json.dumps(doc).encode()

    pl = dict({"url": "/job", "answer_path": "text"}, **fields)
    report = {}
    out = io.StringIO()
    code, answer = None, None
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
        try:
            answer = rh.poll_mapped(pl, b"{}", types.SimpleNamespace(reply_id=""), lambda *a: None, send, {}, lambda: 1e9, report)
        except SystemExit as e:
            code = e.code
    return code, answer, report, out.getvalue()


class MappedPollTest(unittest.TestCase):
    def test_mapped_poll_timeout_is_incomplete(self):
        answers = [{"text": "part %d" % i} for i in range(rh.MAX_POLLS + 5)]
        code, answer, _, out = _mapped(answers)
        self.assertEqual(code, 1, "a half answer was returned: %r" % answer)
        self.assertIn("incomplete answer: the agent did not finish within the time limit", out)

    def test_mapped_poll_stable_text_still_finishes(self):
        code, answer, _, _ = _mapped([{"text": "READ"}, {"text": "READY"}, {"text": "READY"}])
        self.assertIsNone(code)
        self.assertEqual(answer, "READY")

    def test_mapped_poll_waits_for_done_state(self):
        answers = [{"text": "READY", "status": "running"}] * 3 + [{"text": "READY", "status": "Succeeded"}]
        code, answer, report, _ = _mapped(answers, status_path="status", done_states=["succeeded"])
        self.assertIsNone(code)
        self.assertEqual(answer, "READY")
        self.assertEqual(report.get("terminal"), {"state": "succeeded", "poll": 4})

    def test_mapped_poll_complete_path(self):
        answers = [{"text": "REA", "is_final": False}, {"text": "READY", "is_final": True}]
        code, answer, report, _ = _mapped(answers, complete_path="is_final")
        self.assertIsNone(code)
        self.assertEqual(answer, "READY")
        self.assertEqual(report.get("terminal"), {"state": "complete", "poll": 2})

    def test_mapped_poll_fail_state(self):
        code, _, _, out = _mapped([{"text": "half", "status": "FAILED"}], status_path="status", done_states=["done"], fail_states=["failed"])
        self.assertEqual(code, 1)
        self.assertIn("the agent said failed", out)


class _Remote:
    def __init__(self, answers):
        self.answers = answers
        self.n = 0
        self.url = urllib.parse.urlsplit("http://agent.example/run")

    def same_origin(self, target):
        return target

    def do(self, method, path, body, headers, timeout):
        status, doc = self.answers[min(self.n, len(self.answers) - 1)]
        self.n += 1
        return _Resp(status), json.dumps(doc).encode()


def _async(answers):
    remote = _Remote(answers)
    out = io.StringIO()
    code, got = None, None
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
        try:
            got = rh.poll(remote, _Resp(202, {"Location": "/status/1"}), b"{}", 1e12, {})
        except SystemExit as e:
            code = e.code
    return code, got, remote, out.getvalue()


class AsyncPollTest(unittest.TestCase):
    def test_202_in_progress_is_pending(self):
        code, got, remote, _ = _async([(200, {"status": "in_progress"}), (200, {"status": "processing"}), (200, {"status": "done", "answer": "x"})])
        self.assertIsNone(code)
        self.assertEqual(remote.n, 3)
        self.assertEqual(json.loads(got[1])["status"], "done")

    def test_202_failed_status_fails(self):
        code, _, _, out = _async([(200, {"status": "running"}), (200, {"status": "Cancelled"})])
        self.assertEqual(code, 1)
        self.assertIn("the agent's run ended cancelled", out)


if __name__ == "__main__":
    unittest.main()
