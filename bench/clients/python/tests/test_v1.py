"""Unit tests of heimdall.v1.V1Client against a local fake server (stdlib only).

Run from bench/clients/python: python3 -m unittest
"""

import json
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from heimdall import HeimdallError, HeimdallTimeout, ModeError, ReportCard, V1Client  # noqa: E402
from heimdall.v1 import V1_ROUTES  # noqa: E402
from tests.test_client import FakeServer, error_reply, json_reply  # noqa: E402

REPORT = {
    "evaluation": "ev_1",
    "status": "done",
    "mode": "live",
    "arms": [{"label": "A", "agent": "bot", "version": 2, "model": "m1"}, {"label": "B", "agent": "bot", "version": 3, "model": "m1"}],
    "capabilities": [{"capability": "coding", "benchmarks": ["c1"], "arms": {"A": {"score": 0.5, "ci_low": 0.2, "ci_high": 0.8, "runs": 4}}}],
    "benchmarks": [
        {"benchmark": "c1", "capability": "coding", "experiment_id": "e_1",
         "arms": {"A": {"score": 0.5, "ci_low": 0.2, "ci_high": 0.8, "runs": 4, "cost_usd": 0.1}, "B": {"score": 1.0, "ci_low": 0.4, "ci_high": 1, "runs": 4}},
         "verdict": {"winner": "B", "delta": 0.5, "significant": False, "summary": "B ahead", "tasks": 4, "p_value": 0.25}},
        {"benchmark": "s1", "capability": "support", "experiment_id": "e_2",
         "arms": {"A": {"score": 1.0, "ci_low": 0.5, "ci_high": 1, "runs": 12}}},
    ],
    "reliability": [{"fault": "model-down", "benchmarks": ["c1"], "arms": {"A": {"score": 0.25, "drop": 0.25, "runs": 4}}}],
    "safety": {"attack_resisted": {"A": 0.9}, "cheat_rate": {"A": 0.0}},
    "overall": {"winner": "B", "wins": {"B": 1}, "delta": 0.5, "significant": False, "tasks": 4, "benchmarks": 1, "summary": "unclear"},
}

SPEC = {"name": "nightly", "mode": "score", "arms": [{"label": "A", "agent": "bot", "model": "m1"}], "benchmarks": ["c1"]}


class V1ClientTest(unittest.TestCase):
    def setUp(self) -> None:
        self.srv = FakeServer()
        self.hd = V1Client(self.srv.url, token="hm_tok", timeout=5)

    def tearDown(self) -> None:
        self.srv.close()

    def test_paths_and_auth(self) -> None:
        self.srv.on("GET", "/api/v1/agents", json_reply(200, {"agents": []}))
        self.srv.on("GET", "/api/v1/agents/my%20bot/compatibility", json_reply(200, {"agent": "my bot", "benchmarks": [{"id": "c1", "ok": True}]}))
        self.srv.on("GET", "/api/v1/runs?exp=e_1&limit=5", json_reply(200, {"runs": [], "total": 0}))
        self.srv.on("GET", "/api/v1/leaderboard/agents?capability=coding", json_reply(200, {"rows": []}))
        self.srv.on("GET", "/api/v1/benchmarks", json_reply(200, {"suites": []}))
        self.srv.on("POST", "/api/v1/agents/bot/versions", json_reply(201, {"version": 3, "created": True}))
        self.hd.agents()
        self.assertTrue(self.hd.compatibility("my bot")["benchmarks"][0]["ok"])
        self.hd.runs(exp="e_1", limit=5, suite=None)
        self.hd.leaderboard(capability="coding")
        self.hd.benchmarks()
        self.assertEqual(self.hd.create_agent_version("bot", note="ci")["version"], 3)
        for r in self.srv.requests:
            self.assertEqual(r["headers"].get("Authorization"), "Bearer hm_tok")
        self.assertEqual(json.loads(self.srv.requests[-1]["body"]), {"note": "ci"})

    def test_report_card_models(self) -> None:
        self.srv.on("GET", "/api/v1/evaluations/ev_1/report", json_reply(200, REPORT))
        rc = self.hd.report("ev_1")
        self.assertIsInstance(rc, ReportCard)
        self.assertTrue(rc.live)
        self.assertEqual([a.label for a in rc.arms], ["A", "B"])
        self.assertEqual(rc.arms[0].version, 2)
        c1 = rc.benchmark("c1")
        assert c1 is not None and c1.verdict is not None
        self.assertEqual(c1.verdict.winner, "B")
        self.assertEqual(c1.verdict.p_value, 0.25)
        self.assertIsNone(c1.verdict.p_value_holm)
        self.assertAlmostEqual(c1.arms["A"].cost_usd, 0.1)
        self.assertAlmostEqual(rc.score("A") or 0, (0.5 * 4 + 1.0 * 12) / 16)
        self.assertIsNone(rc.score("Z"))
        self.assertEqual(rc.reliability[0].arms["A"].drop, 0.25)
        self.assertEqual(rc.safety.attack_resisted["A"], 0.9)
        assert rc.overall is not None
        self.assertEqual(rc.overall.wins, {"B": 1})

    def test_report_card_tolerates_missing_parts(self) -> None:
        rc = ReportCard.from_dict({"evaluation": "ev_2", "benchmarks": [{"benchmark": "x", "arms": None}], "safety": None})
        self.assertEqual(rc.benchmarks[0].arms, {})
        self.assertIsNone(rc.overall)
        self.assertEqual(rc.safety.cheat_rate, {})

    def test_agent_report_card(self) -> None:
        self.srv.on("GET", "/api/v1/agents/bot/report-card?version=2", json_reply(200, {"agent": "bot", "version": 2, "report": REPORT}))
        self.srv.on("GET", "/api/v1/agents/new/report-card", json_reply(200, {"agent": "new", "report": None}))
        card = self.hd.agent_report_card("bot", version=2)
        assert card.report is not None
        self.assertEqual(card.report.evaluation, "ev_1")
        self.assertIsNone(self.hd.agent_report_card("new").report)

    def test_evaluate_and_wait(self) -> None:
        self.srv.on("POST", "/api/v1/evaluations", json_reply(202, {"id": "ev_1", "status": "queued", "children": []}))
        self.srv.on(
            "GET", "/api/v1/evaluations/ev_1",
            json_reply(200, {"id": "ev_1", "status": "running"}),
            json_reply(200, {"id": "ev_1", "status": "done"}),
        )
        self.srv.on("GET", "/api/v1/evaluations/ev_1/report", json_reply(200, REPORT))
        seen = []
        result = self.hd.evaluate_and_wait(SPEC, interval=0.01, on_status=lambda ev: seen.append(ev["status"]))
        self.assertTrue(result.succeeded)
        self.assertEqual(seen, ["running", "done"])
        self.assertEqual(result.report.evaluation, "ev_1")
        posted = [r for r in self.srv.requests if r["method"] == "POST"]
        self.assertEqual(json.loads(posted[0]["body"])["name"], "nightly")

    def test_evaluate_and_wait_times_out_and_cancels(self) -> None:
        self.srv.on("POST", "/api/v1/evaluations", json_reply(202, {"id": "ev_9", "status": "queued"}))
        self.srv.on("GET", "/api/v1/evaluations/ev_9", json_reply(200, {"id": "ev_9", "status": "running"}))
        self.srv.on("POST", "/api/v1/evaluations/ev_9/cancel", json_reply(202, {"id": "ev_9", "status": "cancelled"}))
        with self.assertRaises(HeimdallTimeout):
            self.hd.evaluate_and_wait(SPEC, timeout=0.2, interval=0.05)
        self.assertIn(("POST", "/api/v1/evaluations/ev_9/cancel"), [(r["method"], r["path"]) for r in self.srv.requests])

    def test_failed_evaluation_still_returns(self) -> None:
        self.srv.on("POST", "/api/v1/evaluations", json_reply(202, {"id": "ev_3", "status": "queued"}))
        self.srv.on("GET", "/api/v1/evaluations/ev_3", json_reply(200, {"id": "ev_3", "status": "failed"}))
        self.srv.on("GET", "/api/v1/evaluations/ev_3/report", json_reply(200, dict(REPORT, evaluation="ev_3", status="failed")))
        result = self.hd.evaluate_and_wait(SPEC, interval=0.01)
        self.assertFalse(result.succeeded)

    def test_errors_and_scope_refusal(self) -> None:
        self.srv.on("POST", "/api/v1/evaluations", error_reply(403, "forbidden", "this API token's scopes (read) do not allow this request: scope evaluate required"))
        with self.assertRaises(HeimdallError) as cm:
            self.hd.create_evaluation(SPEC)
        self.assertEqual(cm.exception.status, 403)
        self.assertIn("scope evaluate", cm.exception.message)

    def test_require_live(self) -> None:
        self.srv.on("GET", "/api/v1/evaluations", json_reply(200, {"evaluations": []}, mode="recorded"))
        with self.assertRaises(ModeError):
            V1Client(self.srv.url, require_live=True).evaluations()

    def test_trace_export(self) -> None:
        self.srv.on("GET", "/api/v1/runs/r_1/trace?format=jsonl", (200, {"Content-Type": "application/x-ndjson", "X-Bench-Mode": "live"}, b'{"step":1}\n'))
        self.srv.on("GET", "/api/v1/runs/r_1/otlp?payloads=true", (200, {"Content-Type": "application/json", "X-Bench-Mode": "live"}, b"{}"))
        t = self.hd.trace("r_1", format="jsonl")
        self.assertEqual(t.mode, "live")
        self.assertIn("step", t.text())
        self.assertEqual(self.hd.otlp("r_1", payloads=True).data, b"{}")

    def test_routes_are_v1(self) -> None:
        for method, path in V1_ROUTES:
            self.assertIn(method, ("GET", "POST"))
            self.assertTrue(path.startswith("/api/v1/"), path)


if __name__ == "__main__":
    unittest.main()
