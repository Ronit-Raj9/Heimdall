"""Tests of abench.py and personas.py on the upstream package (run in the
service image: python -m unittest abench_test, from /opt/abench)."""

import json
import os
import tempfile
import unittest

import abench
import personas

WORLD = {
    "meta": {"current_time": "2026-03-14T09:00:00Z"},
    "gmail": {"messages": [], "labels": [], "drafts": []},
    "slack": {"channels": [{"id": "D_DANA", "name": "dana", "channel_type": "dm", "member_ids": ["U_ME", "U_DANA"]},
                           {"id": "C_FIN", "name": "finance", "member_ids": ["U_ME", "U_DANA"]}],
              "messages": [], "users": [{"id": "U_DANA", "name": "Dana Lee"}, {"id": "U_ME", "name": "Me"}]},
}
PEOPLE = {"mode": "scripted", "personas": [{
    "id": "dana", "name": "Dana Lee", "email": "dana@acme.example.com", "slack_user_id": "U_DANA",
    "rules": [{"if_contains": ["budget"], "reply": "The Q3 budget is $40,000."}], "default_reply": "Noted.", "max_replies": 2}]}


def session(tmp, people=PEOPLE):
    pp = os.path.join(tmp, "personas.json")
    with open(pp, "w") as f:
        json.dump(people, f)
    scen = {"task_name": "t", "initial_state": WORLD, "zapier_tools": [], "allowed_services": ["gmail", "slack"], "toolset": "api"}
    return abench.Session(scen, os.path.join(tmp, "st"), pp)


class PersonaTest(unittest.TestCase):
    def test_email_reply_in_thread(self):
        with tempfile.TemporaryDirectory() as tmp:
            s = session(tmp)
            raw = s.call("base64_encode", {"text": "To: dana@acme.example.com\r\nSubject: Q3\r\n\r\nWhat is the budget?"})[0]
            text, err = s.call("api_fetch", {"method": "POST", "url": "https://gmail.googleapis.com/gmail/v1/users/me/messages/send", "body": json.dumps({"raw": raw})})
            self.assertFalse(err, text)
            replies = [m for m in s.world.gmail.messages if m.from_ == "dana@acme.example.com"]
            self.assertEqual(len(replies), 1)
            self.assertIn("$40,000", replies[0].body_plain)
            self.assertTrue(replies[0].subject.startswith("Re: "))
            with open(os.path.join(tmp, "st", "calls.json")) as f:
                led = json.load(f)["personas"]["persona_messages"]
            self.assertEqual(led[0]["persona"], "dana")

    def test_slack_dm_and_mention_and_cap(self):
        with tempfile.TemporaryDirectory() as tmp:
            s = session(tmp)
            post = lambda ch, t: s.call("api_fetch", {"method": "POST", "url": "https://slack.com/api/chat.postMessage", "body": json.dumps({"channel": ch, "text": t})})
            post("D_DANA", "hello")
            post("C_FIN", "unrelated note")
            post("C_FIN", "<@U_DANA> budget please")
            post("D_DANA", "one more")
            mine = [m for m in s.world.slack.messages if m.user_id == "U_DANA"]
            self.assertEqual([m.text for m in mine], ["Noted.", "The Q3 budget is $40,000."])  # max_replies 2

    def test_scripted_rules(self):
        p = PEOPLE["personas"][0]
        self.assertEqual(personas.scripted_reply(p, "BUDGET?"), "The Q3 budget is $40,000.")
        self.assertEqual(personas.scripted_reply(p, "hi"), "Noted.")

    def test_no_personas(self):
        self.assertIsNone(personas.load("/nonexistent/personas.json"))


class ClockTest(unittest.TestCase):
    def test_bench_now_fills_missing_current_time(self):
        os.environ["BENCH_NOW"] = "2026-08-04T09:00:00Z"
        try:
            with tempfile.TemporaryDirectory() as tmp:
                scen = {"task_name": "t", "initial_state": {"gmail": {"messages": []}}, "zapier_tools": [], "allowed_services": ["gmail"]}
                s = abench.Session(scen, tmp, None)
                self.assertEqual(s.world.meta.current_time.isoformat(), "2026-08-04T09:00:00+00:00")
                scen["initial_state"]["meta"] = {"current_time": "2026-01-01T00:00:00Z"}
                s = abench.Session(scen, tmp, None)
                self.assertEqual(s.world.meta.current_time.year, 2026)
                self.assertEqual(s.world.meta.current_time.month, 1)
        finally:
            del os.environ["BENCH_NOW"]


if __name__ == "__main__":
    unittest.main()
