"""Unit test of modal_helper.py's image choice (fix wave 3, N216 fix 2).

Run: python3 -m unittest discover -s sandbox -p '*_test.py'
"""

import importlib.util
import os
import unittest

_HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("modal_helper", os.path.join(_HERE, "modal_helper.py"))
helper = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(helper)


class FakeModal:
    """Records from_registry and Secret.from_dict calls like the modal SDK."""

    def __init__(self):
        self.calls = []
        self.secrets = []
        outer = self

        class Image:
            @staticmethod
            def from_registry(ref, **kw):
                outer.calls.append((ref, kw))
                return ("image", ref)

        class Secret:
            @staticmethod
            def from_dict(d):
                outer.secrets.append(dict(d))
                return ("secret", tuple(sorted(d)))

        self.Image = Image
        self.Secret = Secret


class RegistryImageTest(unittest.TestCase):
    def test_no_registry_auth_pulls_without_secret(self):
        m = FakeModal()
        helper.registry_image(m, {"image": "docker.io/library/alpine@sha256:feed"})
        self.assertEqual(m.calls, [("docker.io/library/alpine@sha256:feed", {})])
        self.assertEqual(m.secrets, [])

    def test_registry_auth_passes_a_secret(self):
        m = FakeModal()
        req = {"image": "ghcr.io/acme/heimdall/x@sha256:feed", "registry_auth": {"username": "acme-bot", "password": "ghp_TOKEN"}}
        helper.registry_image(m, req)
        self.assertEqual(m.secrets, [{"REGISTRY_USERNAME": "acme-bot", "REGISTRY_PASSWORD": "ghp_TOKEN"}])
        ref, kw = m.calls[0]
        self.assertEqual(ref, "ghcr.io/acme/heimdall/x@sha256:feed")
        self.assertIn("secret", kw)

    def test_empty_registry_auth_is_ignored(self):
        m = FakeModal()
        helper.registry_image(m, {"image": "a@sha256:1", "registry_auth": {}})
        self.assertEqual(m.secrets, [])


if __name__ == "__main__":
    unittest.main()
