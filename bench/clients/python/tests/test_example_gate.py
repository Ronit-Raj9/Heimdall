"""The CI gate example fails on invalid and regressed verdicts (QC2-22-03)."""

import importlib.util
import os
import unittest
from types import SimpleNamespace

_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "examples", "ci_gate.py")
_spec = importlib.util.spec_from_file_location("ci_gate", _PATH)
ci_gate = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(ci_gate)


def result(decision, action, policy=None):
    return SimpleNamespace(decision=decision, action=action, policy_decision=policy, blocked=policy == "block")


class GateTest(unittest.TestCase):
    def test_default_fail_on(self) -> None:
        fail_on = ci_gate.parse_fail_on(ci_gate.DEFAULT_FAIL_ON)
        for decision, action in (("invalid", "fix_harness"), ("regressed", "hold")):
            with self.subTest(decision=decision):
                self.assertIsNotNone(ci_gate.gate_failure(result(decision, action), fail_on))
                self.assertIsNotNone(ci_gate.gate_failure(result(decision, action, "allow"), fail_on))
        self.assertIsNone(ci_gate.gate_failure(result("improved", "ship", "allow"), fail_on))
        self.assertIsNone(ci_gate.gate_failure(result("regressed", "hold"), []))  # --policy-only
        self.assertIsNotNone(ci_gate.gate_failure(result("improved", "ship", "block"), fail_on))


if __name__ == "__main__":
    unittest.main()
