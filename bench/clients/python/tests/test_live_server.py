"""Smoke test of the client against a real bench server.

Skipped unless HEIMDALL_URL is set. bench/clients/contract_test.go starts the
real server in recorded mode over a copy of bench/testdata/recorded and runs
this file with HEIMDALL_URL, HEIMDALL_TOKEN and HEIMDALL_EXPECT_MODE set, so
the client cannot drift from the server silently. Read routes only, plus
write routes that a recorded server refuses (no launcher), so nothing changes.
"""

import os
import re
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from heimdall import ROUTES, Client, HeimdallError, mode_of  # noqa: E402

URL = os.environ.get("HEIMDALL_URL", "")
TOKEN = os.environ.get("HEIMDALL_TOKEN") or None
EXPECT_MODE = os.environ.get("HEIMDALL_EXPECT_MODE", "")
DECISIONS = {"improved", "regressed", "inconclusive", "invalid"}
ACTIONS = {"ship", "hold", "no_decision", "fix_harness"}


@unittest.skipUnless(URL, "HEIMDALL_URL not set")
class LiveServerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.client = Client(URL, token=TOKEN, timeout=60)
        cls.mode = EXPECT_MODE or cls.client.health()["mode"]

    def assertMode(self, response) -> None:
        self.assertEqual(mode_of(response), self.mode)

    def test_openapi_lists_every_client_route(self) -> None:
        doc = self.client.openapi()
        self.assertEqual(doc["openapi"], "3.1.0")
        self.assertEqual(doc["x-bench-mode"], self.mode)
        for method, path in ROUTES:
            with self.subTest(route="%s %s" % (method, path)):
                self.assertIn(method.lower(), doc["paths"].get(path, {}), "client calls a route the server does not describe")

    def test_health_catalog_suites_agents(self) -> None:
        self.assertEqual(self.client.health()["status"], "ok")
        self.assertMode(self.client.catalog())
        suites = self.client.suites()
        self.assertMode(suites)
        with_tasks = [s for s in suites["suites"] if s.get("tasks")]
        if with_tasks:
            s = with_tasks[0]
            task = self.client.suite_task(s["id"], s["tasks"][0]["id"])
            self.assertMode(task)
        self.assertMode(self.client.agents())
        self.assertMode(self.client.imports())
        workers = self.client.workers()
        self.assertMode(workers)
        self.assertIsInstance(workers["enabled"], bool)

    def test_experiments_runs_and_verdicts(self) -> None:
        listing = self.client.experiments()
        self.assertMode(listing)
        self.assertTrue(listing["experiments"], "the recorded bundle has experiments")
        for exp in listing["experiments"]:
            exp_id = exp["exp_id"]
            with self.subTest(exp=exp_id):
                status = self.client.experiment(exp_id)
                self.assertMode(status)
                self.assertEqual(self.client.wait_for_experiment(exp_id, timeout=5, interval=0.1)["state"], status["state"])
                cells = self.client.runs(exp_id)
                self.assertTrue(cells)
                run_id = cells[0]["run_id"]
                detail = self.client.run(run_id)
                self.assertMode(detail)
                self.assertEqual(detail["run"]["run_id"], run_id)
                self.assertMode(self.client.run_diff(run_id))
                self.assertMode(self.client.run_receipts(run_id))
                self.assertMode(self.client.run_events(run_id))
                self.assertEqual(self.client.run_patch(run_id).mode, self.mode)
                self.assertEqual(self.client.run_file(run_id, "run.json").mode, self.mode)
                self.assertMode(self.client.pair(cells[0]["pair_id"]))
                self.assertMode(self.client.events(exp_id))
                self.assertMode(self.client.analysis(exp_id))
                self.assertMode(self.client.triage(exp_id))
                result = self.client.check(exp_id)
                decision, action, policy = result
                self.assertIn(decision, DECISIONS)
                self.assertIn(action, ACTIONS)
                self.assertEqual(result.mode, self.mode)
                if policy is not None:
                    self.assertIn(policy["decision"], {"allow", "warn", "block"})

    def test_leaderboard_and_releases(self) -> None:
        self.assertMode(self.client.leaderboard())
        releases = self.client.releases()
        self.assertMode(releases)
        for rec in releases["releases"][:1]:
            self.assertMode(self.client.release(rec["release_id"]))

    def test_errors_carry_server_message(self) -> None:
        with self.assertRaises(HeimdallError) as cm:
            self.client.experiment("no-such-experiment")
        self.assertEqual((cm.exception.status, cm.exception.type, cm.exception.mode), (404, "not_found", self.mode))
        self.assertRegex(cm.exception.message, re.compile("not found"))
        with self.assertRaises(HeimdallError) as cm:
            Client(URL).plan({"name": "x"})
        self.assertEqual((cm.exception.status, cm.exception.type), (401, "unauthorized"))
        if self.mode != "live" and TOKEN:
            # A recorded server has no launcher: the token passes, the route answers 503.
            with self.assertRaises(HeimdallError) as cm:
                self.client.plan("name: x\n")
            self.assertEqual((cm.exception.status, cm.exception.type), (503, "unavailable"))


if __name__ == "__main__":
    unittest.main()
