"""Unit tests of heimdall.Client against a local fake server (stdlib only).

Run from bench/clients/python: python3 -m unittest
"""

import json
import os
import sys
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable, Dict, List, Optional, Tuple

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from heimdall import Client, HeimdallError, HeimdallTimeout, ModeError, mode_of  # noqa: E402

Reply = Tuple[int, Dict[str, str], bytes]


def json_reply(status: int, payload: Dict[str, Any], mode: str = "live") -> Reply:
    body = dict(payload)
    body.setdefault("mode", mode)
    return status, {"Content-Type": "application/json; charset=utf-8", "X-Bench-Mode": mode}, json.dumps(body).encode()


def error_reply(status: int, etype: str, message: str, mode: str = "live") -> Reply:
    return json_reply(status, {"error": {"message": message, "type": etype}}, mode)


class FakeServer:
    """Answers (method, path) with queued or fixed replies and records requests."""

    def __init__(self) -> None:
        self.routes: Dict[Tuple[str, str], List[Callable[[], Reply]]] = {}
        self.requests: List[Dict[str, Any]] = []
        fake = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args: Any) -> None:
                pass

            def _serve(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                body = self.rfile.read(length) if length else b""
                fake.requests.append({"method": self.command, "path": self.path, "headers": dict(self.headers), "body": body})
                key = (self.command, self.path)
                queue = fake.routes.get(key)
                if not queue:
                    status, headers, out = error_reply(404, "not_found", "route not found: " + self.path)
                else:
                    reply = queue.pop(0) if len(queue) > 1 else queue[0]
                    status, headers, out = reply()
                try:
                    self.send_response(status)
                    for k, v in headers.items():
                        self.send_header(k, v)
                    self.send_header("Content-Length", str(len(out)))
                    self.end_headers()
                    self.wfile.write(out)
                except (BrokenPipeError, ConnectionResetError):
                    pass  # the client gave up first (the timeout test)

            do_GET = _serve
            do_POST = _serve

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.httpd.daemon_threads = True
        self.thread = threading.Thread(target=self.httpd.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True)
        self.thread.start()
        self.url = "http://127.0.0.1:%d" % self.httpd.server_address[1]

    def on(self, method: str, path: str, *replies: Reply) -> None:
        self.routes[(method, path)] = [(lambda r=r: r) for r in replies]

    def on_call(self, method: str, path: str, fn: Callable[[], Reply]) -> None:
        self.routes[(method, path)] = [fn]

    def close(self) -> None:
        self.httpd.shutdown()
        self.httpd.server_close()


def status_payload(state: str) -> Dict[str, Any]:
    return {
        "exp_id": "e1", "name": "smoke", "state": state, "manifest_hash": "h", "frozen_at": "2026-11-15T00:00:00Z",
        "slots": {"total": 2, "done": 1, "running": 1, "failed": 0}, "spent_usd": {"state": "unknown", "reason": "x", "evidence": "gateway"},
    }


def verdict_payload(policy: Optional[str]) -> Dict[str, Any]:
    out: Dict[str, Any] = {
        "exp_id": "e1", "analysis_id": "a1", "grade_source": "original", "suite": "s", "naive": {}, "gates": [], "config_diff": [],
        "verdict": {"decision": "regressed", "action": "hold"},
    }
    if policy:
        out["policy"] = {"policy_name": "p", "policy_hash": "x", "decision": policy, "checks": [], "evaluated_at": "2026-11-15T00:00:00Z"}
    return out


class ClientTest(unittest.TestCase):
    def setUp(self) -> None:
        self.fake = FakeServer()
        self.client = Client(self.fake.url, token="tok", timeout=2, read_retries=2)

    def tearDown(self) -> None:
        self.fake.close()

    def test_responses_carry_mode(self) -> None:
        self.fake.on("GET", "/api/bench/health", json_reply(200, {"status": "ok", "version": "1"}, mode="recorded"))
        h = self.client.health()
        self.assertEqual(h["mode"], "recorded")
        self.assertEqual(mode_of(h), "recorded")
        self.assertEqual(self.client.last_mode, "recorded")

    def test_reads_send_token_and_accept(self) -> None:
        self.fake.on("GET", "/api/bench/suites", json_reply(200, {"suites": []}))
        self.client.suites()
        req = self.fake.requests[-1]
        self.assertEqual(req["headers"].get("Authorization"), "Bearer tok")
        self.assertEqual(req["headers"].get("Accept"), "application/json")

    def test_no_token_no_header(self) -> None:
        self.fake.on("GET", "/api/bench/agents", json_reply(200, {"agents": []}))
        Client(self.fake.url).agents()
        self.assertNotIn("Authorization", self.fake.requests[-1]["headers"])

    def test_error_carries_status_type_message(self) -> None:
        self.fake.on("GET", "/api/bench/experiments/nope", error_reply(404, "not_found", "experiment not found: nope", "recorded"))
        with self.assertRaises(HeimdallError) as cm:
            self.client.experiment("nope")
        err = cm.exception
        self.assertEqual((err.status, err.type, err.message, err.mode), (404, "not_found", "experiment not found: nope", "recorded"))
        self.assertIn("404", str(err))

    def test_plan_json_and_yaml(self) -> None:
        self.fake.on("POST", "/api/bench/experiments/plan", json_reply(200, {"slots": 4, "errors": [], "warnings": []}))
        plan = self.client.plan({"name": "x"})
        self.assertEqual(plan["slots"], 4)
        req = self.fake.requests[-1]
        self.assertEqual(req["headers"]["Content-Type"], "application/json")
        self.assertEqual(json.loads(req["body"]), {"name": "x"})
        self.client.plan("name: x\n")
        req = self.fake.requests[-1]
        self.assertEqual(req["headers"]["Content-Type"], "application/yaml")
        self.assertEqual(req["body"], b"name: x\n")

    def test_writes_never_retry(self) -> None:
        self.fake.on("POST", "/api/bench/experiments/e1/start", error_reply(502, "unavailable", "bad gateway"), json_reply(202, status_payload("running")))
        with self.assertRaises(HeimdallError) as cm:
            self.client.start_experiment("e1")
        self.assertEqual(cm.exception.status, 502)
        self.assertEqual(len([r for r in self.fake.requests if r["method"] == "POST"]), 1)

    def test_reads_retry_on_502(self) -> None:
        self.fake.on("GET", "/api/bench/experiments/e1", error_reply(502, "unavailable", "bad gateway"), json_reply(200, status_payload("done")))
        self.assertEqual(self.client.experiment("e1")["state"], "done")
        self.assertEqual(len(self.fake.requests), 2)

    def test_reads_do_not_retry_on_503(self) -> None:
        self.fake.on("GET", "/api/bench/doctor", error_reply(503, "unavailable", "doctor is not available"), json_reply(200, {"ok": True, "checks": []}))
        with self.assertRaises(HeimdallError) as cm:
            self.client.doctor()
        self.assertEqual(cm.exception.type, "unavailable")
        self.assertEqual(len(self.fake.requests), 1)

    def test_require_live_refuses_recorded(self) -> None:
        self.fake.on("GET", "/api/bench/experiments", json_reply(200, {"experiments": []}, mode="recorded"))
        strict = Client(self.fake.url, require_live=True)
        with self.assertRaises(ModeError) as cm:
            strict.experiments()
        self.assertEqual(cm.exception.mode, "recorded")
        self.fake.on("GET", "/api/bench/experiments", json_reply(200, {"experiments": []}, mode="live"))
        self.assertEqual(strict.experiments()["mode"], "live")

    def test_wait_for_experiment(self) -> None:
        self.fake.on("GET", "/api/bench/experiments/e1", *(json_reply(200, status_payload(s)) for s in ("running", "analyzing", "done")))
        seen: List[str] = []
        final = self.client.wait_for_experiment("e1", timeout=5, interval=0.01, on_status=lambda s: seen.append(s["state"]))
        self.assertEqual(final["state"], "done")
        self.assertEqual(seen, ["running", "analyzing", "done"])

    def test_wait_for_experiment_times_out(self) -> None:
        self.fake.on("GET", "/api/bench/experiments/e1", json_reply(200, status_payload("running")))
        start = time.monotonic()
        with self.assertRaises(HeimdallTimeout):
            self.client.wait_for_experiment("e1", timeout=0.2, interval=0.05)
        self.assertLess(time.monotonic() - start, 2)

    def test_check_unpacks_and_blocks(self) -> None:
        cases = [("block", True), ("warn", False), (None, False)]
        for policy, blocked in cases:
            with self.subTest(policy=policy):
                self.fake.on("GET", "/api/bench/experiments/e1/verdict", json_reply(200, verdict_payload(policy), mode="synthetic"))
                result = self.client.check("e1")
                decision, action, policy_result = result
                self.assertEqual((decision, action), ("regressed", "hold"))
                self.assertEqual(result.policy_decision, policy)
                self.assertEqual(result.blocked, blocked)
                self.assertEqual(policy_result is None, policy is None)
                self.assertEqual(result.mode, "synthetic")
                self.assertFalse(result.live)

    def test_grade_source_query(self) -> None:
        self.fake.on("GET", "/api/bench/experiments/e1/verdict?grade_source=r1", json_reply(200, verdict_payload(None)))
        self.client.verdict("e1", grade_source="r1")
        self.assertEqual(self.fake.requests[-1]["path"], "/api/bench/experiments/e1/verdict?grade_source=r1")

    def test_raw_patch_mode_from_header(self) -> None:
        self.fake.on("GET", "/api/bench/runs/r1/diff?format=text", (200, {"Content-Type": "text/plain", "X-Bench-Mode": "recorded"}, b"diff --git a b\n"))
        raw = self.client.run_patch("r1")
        self.assertEqual(raw.mode, "recorded")
        self.assertEqual(raw.text(), "diff --git a b\n")
        self.assertEqual(mode_of(raw), "recorded")

    def test_runs_from_grid(self) -> None:
        grid = {"exp_id": "e1", "arms": ["A", "B"], "conditions": ["clean"], "trials": 1, "rows": [
            {"task": "t1", "condition": "clean", "cells": [{"slot_id": "s1", "run_id": "r1"}, {"slot_id": "s2", "run_id": None}]},
        ]}
        self.fake.on("GET", "/api/bench/experiments/e1/grid", json_reply(200, grid))
        self.assertEqual([c["run_id"] for c in self.client.runs("e1")], ["r1"])

    def test_path_segments_are_quoted(self) -> None:
        self.fake.on("GET", "/api/bench/suites/a%2Fb/tasks/t%201", json_reply(200, {"suite": "a/b"}))
        self.client.suite_task("a/b", "t 1")
        self.assertEqual(self.fake.requests[-1]["path"], "/api/bench/suites/a%2Fb/tasks/t%201")

    def test_leaderboard_params(self) -> None:
        self.fake.on("GET", "/api/bench/leaderboard?suite=s&k=3", json_reply(200, {"entries": []}))
        self.client.leaderboard(suite="s", k=3, condition=None)
        self.assertEqual(self.fake.requests[-1]["path"], "/api/bench/leaderboard?suite=s&k=3")

    def test_create_release_drops_empty_fields(self) -> None:
        self.fake.on("POST", "/api/bench/releases", json_reply(201, {"release_id": "rel1"}))
        self.client.create_release("e1", "ci-bot", note="n")
        self.assertEqual(json.loads(self.fake.requests[-1]["body"]), {"exp_id": "e1", "approver": "ci-bot", "note": "n"})

    def test_request_timeout(self) -> None:
        def slow() -> Reply:
            time.sleep(1.0)
            return json_reply(200, {"status": "ok"})

        self.fake.on_call("GET", "/api/bench/health", slow)
        quick = Client(self.fake.url, timeout=0.2, read_retries=0)
        with self.assertRaises(HeimdallTimeout):
            quick.health()

    def test_connection_refused(self) -> None:
        dead = Client("http://127.0.0.1:9", timeout=1, read_retries=0)
        with self.assertRaises(HeimdallError) as cm:
            dead.health()
        self.assertEqual(cm.exception.status, 0)
        self.assertEqual(cm.exception.type, "connection")


if __name__ == "__main__":
    unittest.main()
