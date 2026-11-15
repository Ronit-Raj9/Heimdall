"""Unit tests of tau2svc.py that need no upstream package: the scripted user,
the JSON-RPC handling and the communicate scorer. The integration test runs
them in a plain Python container: python -m unittest tau2svc_test."""

import types
import unittest

import tau2svc


def instructions(reason, known=None, unknown=None, how="."):
    return types.SimpleNamespace(reason_for_call=reason, known_info=known, unknown_info=unknown, task_instructions=how)


class ScriptedUserTest(unittest.TestCase):
    def setUp(self):
        self.user = tau2svc.ScriptedUser(instructions(
            "You want to cancel reservation EHGLP3.",
            known="You are Emma Kim.\nYour user id is emma_kim_9957.",
            unknown="You do not remember your email address.",
            how="You don't want to cancel if you don't get a refund.",
        ), max_turns=8)

    def test_opening_states_everything(self):
        first = self.user.reply("Hi! How can I help you today?")
        for want in ("cancel reservation EHGLP3", "emma_kim_9957", "email address", "don't get a refund", '"you" in them means me'):
            self.assertIn(want, first)

    def test_dot_task_instructions_are_left_out(self):
        user = tau2svc.ScriptedUser(instructions("You want a refund.", how="."))
        self.assertNotIn("How I want this handled", user.reply("Hello"))

    def test_turns(self):
        u = self.user
        u.reply("Hello")
        cases = [
            ("Could you please provide your user ID?", "Your user id is emma_kim_9957."),
            ("What is your email?", "I do not know that, sorry."),
            ("Which flight number was it?", "I do not know. Everything I know is in my first message."),
            ("I will cancel EHGLP3 with no refund. Do you want me to proceed?", "Yes, I confirm. Please go ahead."),
            ("Let me look that up.", "Okay, thank you."),
            ("Is there anything else I can help you with?", "No, that is all. Thank you. ###STOP###"),
        ]
        for msg, want in cases:
            self.assertEqual(u.reply(msg), want, msg)

    def test_transfer(self):
        self.user.reply("Hello")
        self.assertEqual(self.user.reply("YOU ARE BEING TRANSFERRED TO A HUMAN AGENT. PLEASE HOLD ON."), tau2svc.TRANSFER)

    def test_max_turns(self):
        for _ in range(8):
            self.user.reply("Let me check.")
        self.assertTrue(self.user.reply("Still checking.").endswith(tau2svc.STOP))

    def test_plain_string_instructions(self):
        user = tau2svc.ScriptedUser("You continue an earlier chat.")
        self.assertIn("You continue an earlier chat.", user.reply("Hello"))
        self.assertEqual(user.reply("What is your name?"), "I do not know. Everything I know is in my first message.")


class WordsTest(unittest.TestCase):
    def test_words(self):
        self.assertEqual(tau2svc.words("Your user id is emma_kim_9957."), {"user", "emma_kim_9957"})
        self.assertEqual(tau2svc.sentences("A b. C d?\nE"), ["A b.", "C d?", "E"])


class CommunicateTest(unittest.TestCase):
    def test_substring_rule(self):
        msgs = ["The total refund is $1,628 to your card."]
        self.assertTrue(tau2svc.substring_met("1628", msgs))
        self.assertTrue(tau2svc.substring_met("REFUND", msgs))
        self.assertFalse(tau2svc.substring_met("1,628", msgs))
        self.assertFalse(tau2svc.substring_met("5244", msgs))


class FakeSession:
    domain = "airline"
    tools = [{"name": "respond_to_user", "description": "", "inputSchema": {"type": "object"}}]

    def call(self, name, args):
        return f"{name}:{sorted(args)}", name == "boom"


class RPCTest(unittest.TestCase):
    def rpc(self, msg):
        return tau2svc.handle_rpc(FakeSession(), msg)

    def test_initialize_negotiates(self):
        r = self.rpc({"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-03-26"}})
        self.assertEqual(r["result"]["protocolVersion"], "2025-03-26")
        self.assertIn("tools", r["result"]["capabilities"])
        r = self.rpc({"jsonrpc": "2.0", "id": 2, "method": "initialize", "params": {"protocolVersion": "1999-01-01"}})
        self.assertEqual(r["result"]["protocolVersion"], tau2svc.PROTOCOL_VERSIONS[0])

    def test_notification_has_no_answer(self):
        self.assertIsNone(self.rpc({"jsonrpc": "2.0", "method": "notifications/initialized"}))

    def test_tools(self):
        r = self.rpc({"jsonrpc": "2.0", "id": 3, "method": "tools/list"})
        self.assertEqual(r["result"]["tools"][0]["name"], "respond_to_user")
        r = self.rpc({"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "x", "arguments": {"a": 1}}})
        self.assertEqual(r["result"], {"content": [{"type": "text", "text": "x:['a']"}], "isError": False})
        r = self.rpc({"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": {"name": "boom"}})
        self.assertTrue(r["result"]["isError"])
        r = self.rpc({"jsonrpc": "2.0", "id": 6, "method": "tools/call", "params": {"name": "x", "arguments": [1]}})
        self.assertEqual(r["error"]["code"], -32602)

    def test_errors(self):
        self.assertEqual(self.rpc({"jsonrpc": "2.0", "id": 7, "method": "nope"})["error"]["code"], -32601)
        self.assertEqual(self.rpc({"id": 8, "method": "ping"})["error"]["code"], -32600)
        self.assertEqual(self.rpc([1])["error"]["code"], -32600)


if __name__ == "__main__":
    unittest.main()
