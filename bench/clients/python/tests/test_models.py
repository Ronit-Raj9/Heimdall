"""heimdall.models agrees with the contract key lists the server and UI share.

The key lists live in bench/handlers/testdata/contract; the test is skipped
when the package is used outside the repository.
"""

import os
import sys
import typing
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, ".."))

from heimdall import models  # noqa: E402

CONTRACT = os.path.join(HERE, "..", "..", "..", "handlers", "testdata", "contract")


def read_keys(path):
    required, optional = [], []
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            if line.endswith("?"):
                optional.append(line[:-1])
            else:
                required.append(line)
    return required, optional


class ModelsTest(unittest.TestCase):
    def test_every_name_resolves(self) -> None:
        for name in models.__all__:
            obj = getattr(models, name)
            if isinstance(obj, type) and hasattr(obj, "__required_keys__"):
                typing.get_type_hints(obj)  # raises on a dangling forward reference

    def test_response_types_add_mode(self) -> None:
        self.assertIn("mode", models.ExperimentStatusResponse.__required_keys__)
        self.assertIn("mode", models.VerdictPayloadResponse.__required_keys__)
        self.assertIn("policy", models.VerdictPayload.__optional_keys__)

    @unittest.skipUnless(os.path.isdir(CONTRACT), "contract key lists not found (package used outside the repository)")
    def test_required_keys_match_contract(self) -> None:
        checked = 0
        for fname in sorted(os.listdir(CONTRACT)):
            if not fname.endswith(".keys"):
                continue
            name = fname[: -len(".keys")]
            td = getattr(models, name, None)
            if td is None or not hasattr(td, "__required_keys__") or not td.__required_keys__:
                continue  # not a Go type name, or a functional TypedDict with all keys optional
            checked += 1
            required, optional = read_keys(os.path.join(CONTRACT, fname))
            with self.subTest(type=name):
                self.assertTrue(set(required) <= set(td.__required_keys__), "contract requires %s" % sorted(set(required) - td.__required_keys__))
                extra = set(td.__required_keys__) - set(required) - set(optional)
                self.assertFalse(extra, "model requires keys not in the contract: %s" % sorted(extra))
        self.assertGreaterEqual(checked, 10)


if __name__ == "__main__":
    unittest.main()
