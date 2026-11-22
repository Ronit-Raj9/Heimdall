"""Simulated colleagues for AutomationBench worlds (Z17): people who reply.

A task may name personas (/task/personas.json). Each persona is a person in
the task's Gmail and Slack: when the agent emails one of them (to or cc), or
writes to one in Slack (a DM channel they are in, a mention <@ID>, or their
name in a channel they are a member of), the persona answers in the same
place: an email reply in the same thread, or a Slack reply in the same
thread. The reply lands in the world, so the agent reads it with the normal
tools, and every reply is a ledger row in calls.json (persona_messages).

Modes:
  scripted  (default) rules in order: the first rule whose if_contains words
            all appear in the agent's message (case folded) gives the reply;
            else default_reply. Deterministic.
  llm       a pinned model plays the persona from its role, facts and
            behaviour, through the gateway on the service's own virtual key
            (BENCH_SVC_GATEWAY_URL, BENCH_SVC_GATEWAY_KEY, BENCH_SVC_MODEL),
            never the agent's. A failed call adds no reply and is counted in
            persona_errors.

personas.json:
  {"mode": "scripted",
   "personas": [{"id": "dana", "name": "Dana Lee", "email": "dana@acme.example.com",
                 "slack_user_id": "U_DANA", "role": "Finance lead",
                 "facts": ["Q3 budget is $40,000"], "behaviour": "terse",
                 "rules": [{"if_contains": ["budget"], "reply": "Q3 budget is $40,000."}],
                 "default_reply": "Thanks, noted.", "max_replies": 5}]}
"""

import json
import os
import time
import urllib.error
import urllib.request

MAX_REPLY_TOKENS = 400
CALL_TIMEOUT_S = 60


def load(path):
    """The personas document at path, or None when the task has none."""
    if not path or not os.path.exists(path):
        return None
    with open(path, encoding="utf-8") as f:
        doc = json.load(f)
    mode = doc.get("mode") or os.environ.get("BENCH_SIM_MODE") or "scripted"
    if mode not in ("scripted", "llm"):
        raise SystemExit(f"personas: mode {mode!r} is not scripted or llm")
    seen = set()
    for p in doc.get("personas") or []:
        if not p.get("id") or not p.get("name") or p["id"] in seen:
            raise SystemExit("personas: every persona needs a unique id and a name")
        if not p.get("email") and not p.get("slack_user_id"):
            raise SystemExit(f"personas: {p['id']} needs an email or a slack_user_id")
        seen.add(p["id"])
    doc["mode"] = mode
    return doc


def _lower(s):
    return (s or "").lower()


def scripted_reply(persona, text):
    low = _lower(text)
    for rule in persona.get("rules") or []:
        words = [w.lower() for w in rule.get("if_contains") or []]
        if words and all(w in low for w in words):
            return rule.get("reply") or ""
    return persona.get("default_reply") or ""


class LLM:
    """Chat completions through the gateway on the service's own key."""

    def __init__(self):
        url, self.key, self.model = (os.environ.get("BENCH_SVC_GATEWAY_URL"), os.environ.get("BENCH_SVC_GATEWAY_KEY"),
                                     os.environ.get("BENCH_SVC_MODEL"))
        if not url or not self.key or not self.model:
            raise SystemExit("personas: llm mode needs BENCH_SVC_GATEWAY_URL, BENCH_SVC_GATEWAY_KEY and BENCH_SVC_MODEL")
        self.url = url.rstrip("/") + "/v1/chat/completions"
        os.environ.pop("BENCH_SVC_GATEWAY_KEY", None)

    def reply(self, persona, history, text):
        system = (
            f"You are {persona['name']}, {persona.get('role') or 'a colleague'}, replying to a coworker at work. "
            "Answer only from the facts below; if they do not cover the question, say you do not know. "
            "Never invent numbers, names or dates. Reply in at most four sentences, no greeting line.\n"
            "Facts:\n- " + "\n- ".join(persona.get("facts") or ["(none)"]) + "\n"
            f"How you behave: {persona.get('behaviour') or 'helpful and brief'}"
        )
        msgs = [{"role": "system", "content": system}]
        for h in history[-6:]:
            msgs.append({"role": "user", "content": h["in"]})
            msgs.append({"role": "assistant", "content": h["out"]})
        msgs.append({"role": "user", "content": "<message>\n" + text + "\n</message>"})
        body = {"model": self.model, "messages": msgs, "temperature": 0, "max_tokens": MAX_REPLY_TOKENS}
        req = urllib.request.Request(self.url, data=json.dumps(body).encode(), method="POST",
                                     headers={"Content-Type": "application/json", "x-bf-vk": self.key})
        started = time.monotonic()
        try:
            with urllib.request.urlopen(req, timeout=CALL_TIMEOUT_S) as r:
                out = json.loads(r.read(1 << 20))
            content = out["choices"][0]["message"]["content"]
        except (urllib.error.URLError, OSError, ValueError, KeyError, IndexError, TypeError) as e:
            raise RuntimeError(str(e).replace(self.key, "[key]")) from None
        if isinstance(content, list):
            content = "".join(p.get("text", "") for p in content if isinstance(p, dict))
        return str(content or "").strip(), {"model_served": out.get("model", ""), "latency_ms": int((time.monotonic() - started) * 1000)}


class Colleagues:
    """Watches the world after every tool call and adds the personas' replies."""

    def __init__(self, doc, world):
        self.doc = doc
        self.people = doc.get("personas") or []
        self.llm = LLM() if doc["mode"] == "llm" else None
        self.ledger = []
        self.errors = 0
        self.history = {p["id"]: [] for p in self.people}
        self.count = {p["id"]: 0 for p in self.people}
        self.own_ids = set()
        g, s = getattr(world, "gmail", None), getattr(world, "slack", None)
        self.seen_mail = {m.id for m in (g.messages if g else [])}
        self.seen_slack = {(m.channel_id, m.ts) for m in (s.messages if s else [])}

    def _answer(self, persona, text):
        if self.count[persona["id"]] >= int(persona.get("max_replies") or 5):
            return None, {}
        if self.llm is None:
            out, meta = scripted_reply(persona, text), {}
        else:
            try:
                out, meta = self.llm.reply(persona, self.history[persona["id"]], text)
            except RuntimeError as e:
                self.errors += 1
                self.ledger.append({"persona": persona["id"], "ok": False, "error": str(e)[:300]})
                return None, {}
        if not out:
            return None, {}
        self.count[persona["id"]] += 1
        self.history[persona["id"]].append({"in": text, "out": out})
        return out, meta

    def after_call(self, world, seq):
        """Answer every new message the agent sent to a persona."""
        by_email = {_lower(p.get("email")): p for p in self.people if p.get("email")}
        by_slack = {p["slack_user_id"]: p for p in self.people if p.get("slack_user_id")}
        g = getattr(world, "gmail", None)
        if g is not None:
            from automationbench.schema.gmail.message import Message as Mail

            for m in list(g.messages):
                if m.id in self.seen_mail:
                    continue
                self.seen_mail.add(m.id)
                if m.id in self.own_ids or _lower(m.from_) in by_email:
                    continue
                for addr in list(m.to) + list(m.cc):
                    p = by_email.get(_lower(addr))
                    if p is None:
                        continue
                    text = (m.subject or "") + "\n\n" + (m.body_plain or "")
                    out, meta = self._answer(p, text)
                    if out is None:
                        continue
                    subj = m.subject or ""
                    reply = Mail(thread_id=m.thread_id, from_=p["email"], from_name=p["name"], to=[m.from_],
                                 subject=subj if subj.lower().startswith("re:") else "Re: " + subj,
                                 body_plain=out, in_reply_to=m.id, label_ids=["INBOX", "UNREAD"])
                    g.messages.append(reply)
                    self.own_ids.add(reply.id)
                    self.seen_mail.add(reply.id)
                    self.ledger.append(dict({"persona": p["id"], "ok": True, "channel": "gmail", "after_call": seq,
                                             "in_reply_to": m.id, "text": out}, **meta))
        s = getattr(world, "slack", None)
        if s is not None:
            from automationbench.schema.slack.message import Message as Post

            channels = {c.id: c for c in s.channels}
            for m in list(s.messages):
                key = (m.channel_id, m.ts)
                if key in self.seen_slack:
                    continue
                self.seen_slack.add(key)
                if m.user_id in by_slack:
                    continue
                ch = channels.get(m.channel_id)
                members = set(ch.member_ids) if ch else set()
                is_dm = ch is not None and ch.channel_type in ("dm", "mpim")
                for uid, p in by_slack.items():
                    named = f"<@{uid}>" in (m.text or "") or (uid in members and _lower(p["name"]) in _lower(m.text))
                    if not ((is_dm and uid in members) or named):
                        continue
                    out, meta = self._answer(p, m.text or "")
                    if out is None:
                        continue
                    post = Post(channel_id=m.channel_id, user_id=uid, text=out, thread_ts=m.thread_ts or m.ts)
                    s.messages.append(post)
                    self.seen_slack.add((post.channel_id, post.ts))
                    self.ledger.append(dict({"persona": p["id"], "ok": True, "channel": "slack", "after_call": seq,
                                             "slack_channel": m.channel_id, "text": out}, **meta))

    def report(self):
        return {"mode": self.doc["mode"], "persona_messages": self.ledger, "persona_errors": self.errors}
