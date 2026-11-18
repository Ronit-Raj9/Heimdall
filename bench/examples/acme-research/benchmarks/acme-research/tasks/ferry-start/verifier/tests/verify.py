#!/usr/bin/env python3
"""Verifier of a Heimdall research task (docs/research-tasks.md).

It runs after the agent exited, with no network, in a fresh container. The
agent's two artifacts are read-only at /grader/artifacts:

  answer/answer.txt    the answer (only the answer; a "Sources:" section at
                       its end is read as the source list when there is no
                       sources.json)
  sources/sources.json the cited sources: a list of URLs, a list of
                       {"url": ...}, or {"sources": [...]}

This folder (/grader/tests) holds research.json, the task's hidden spec, and
optionally corpus_urls.json, every page of a fixed corpus. The reward goes to
/grader/out/reward.json as {"reward": 0 or 1, "dimensions": {...}}.

Checks, every one needed for a pass:
  answer_present       the answer is not empty
  answer_correct       the answer matches the spec as the answer_match grader
                       does (exact, contains_all, regex, numeric), or for an
                       abstention task says the answer is not in the sources
  sources_readable     sources.json, when present, parses
  no_invented_sources  every cited URL is a page of the corpus (only when
                       corpus_urls.json exists)
  evidence_cited       at least one cited URL is in the spec's evidence_urls
                       (only when require_grounding is true)

Grounding against what this run's searches actually returned is a metric
(citations_grounded), read from the tool proxy's ledger, which a verifier
never sees.
"""

import json
import os
import re
import unicodedata
from urllib.parse import urlsplit, urlunsplit

TESTS = os.path.dirname(os.path.abspath(__file__))
ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
MAX_ANSWER = 64 << 10

# The answer_match grader's abstention phrases, plus the ones a research
# answer uses.
ABSTAIN = [
    "i don't know", "i do not know", "i dont know", "i don't remember", "i do not remember", "i don't recall",
    "i do not recall", "i'm not sure", "i am not sure", "not mentioned", "never mentioned", "wasn't mentioned",
    "was not mentioned", "didn't mention", "did not mention", "never told", "didn't tell", "did not tell",
    "no information", "not enough information", "insufficient information", "information is not available",
    "no record of", "cannot be determined", "can't be determined", "cannot determine", "can't determine",
    "cannot answer", "can't answer", "unable to answer", "unanswerable", "not answerable", "not known", "unknown",
    "not in the sources", "not in sources", "not found in the sources", "the sources do not", "sources don't",
]
WORDS = {w: i for i, w in enumerate("zero one two three four five six seven eight nine ten eleven twelve thirteen "
                                    "fourteen fifteen sixteen seventeen eighteen nineteen twenty".split())}
NUMBER = re.compile(r"\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?")
SECTION = re.compile(r"(?im)^[ \t]*(?:#+[ \t]*)?(?:\*\*)?sources?(?:\*\*)?[ \t]*(?::|$)")
URL = re.compile(r"https?://[^\s<>\"'`\]\)]+")


def read(path, limit=MAX_ANSWER):
    try:
        with open(path, "rb") as f:
            return f.read(limit + 1).decode("utf-8", "replace")
    except OSError:
        return None


def normalize(s, steps):
    if not steps:
        return s
    s = s.strip()
    if "lower" in steps:
        s = s.lower()
    if "strip_punct" in steps:
        s = "".join(c for c in s if not unicodedata.category(c).startswith("P"))
    if "collapse_space" in steps:
        s = " ".join(s.split())
    return s.strip()


def numbers(text):
    found = set()
    for m in NUMBER.findall(text):
        found.add(float(m.replace(",", "")))
    for w in re.findall(r"[a-z]+", text.lower()):
        if w in WORDS:
            found.add(float(WORDS[w]))
    return found


def abstained(text):
    t = " ".join(text.lower().replace("’", "'").replace("‘", "'").split())
    return any(p in t for p in ABSTAIN)


def matches(spec, answer):
    if spec.get("abstain"):
        return abstained(answer)
    mode, expected, steps = spec.get("match"), spec.get("expected") or [], spec.get("normalize") or []
    a = normalize(answer, steps)
    if mode == "exact":
        return any(a == normalize(e, steps) for e in expected)
    if mode == "contains_all":
        return bool(expected) and all(normalize(e, steps) in a for e in expected)
    if mode == "regex":
        return any(re.search(e, a) for e in expected)
    if mode == "numeric":
        got = numbers(answer)
        tol = float(spec.get("tolerance") or 0)
        return len(got) == 1 and any(abs(next(iter(got)) - float(e.replace(",", ""))) <= tol for e in expected)
    raise ValueError(f"unknown match mode {mode!r}")


def norm_url(raw):
    s = raw.strip().strip("<>\"'`").rstrip(".,;:)]}>\"'")
    try:
        p = urlsplit(s)
    except ValueError:
        return s
    if p.scheme.lower() not in ("http", "https") or not p.netloc:
        return s
    path = p.path.rstrip("/") if len(p.path) > 1 else p.path
    return urlunsplit((p.scheme.lower(), p.netloc.lower(), path, p.query, ""))


def split_answer(text):
    m = SECTION.search(text)
    if not m:
        return text.strip(), ""
    return text[:m.start()].strip(), text[m.start():]


def cited(answer_section):
    """The cited URLs (normalized, distinct) and whether sources.json parsed."""
    raw = read(os.path.join(ARTIFACTS, "sources", "sources.json"), 1 << 20)
    urls, readable = [], True
    if raw is not None:
        try:
            v = json.loads(raw)
            if isinstance(v, dict):
                v = v.get("sources")
            if not isinstance(v, list):
                raise ValueError("not a list")
            for item in v:
                if isinstance(item, str):
                    urls.append(item)
                elif isinstance(item, dict) and isinstance(item.get("url"), str):
                    urls.append(item["url"])
        except ValueError:
            readable = False
    else:
        urls = URL.findall(answer_section)
    out = []
    for u in urls:
        n = norm_url(u)
        if n and n not in out:
            out.append(n)
    return out, readable


def main():
    with open(os.path.join(TESTS, "research.json"), encoding="utf-8") as f:
        spec = json.load(f)
    corpus = None
    try:
        with open(os.path.join(TESTS, "corpus_urls.json"), encoding="utf-8") as f:
            corpus = {norm_url(u) for u in json.load(f)}
    except OSError:
        pass
    text = read(os.path.join(ARTIFACTS, "answer", "answer.txt")) or ""
    body, section = split_answer(text)
    urls, readable = cited(section)
    evidence = {norm_url(u) for u in spec.get("evidence_urls") or []}
    checks = {
        "answer_present": bool(body) and len(text) <= MAX_ANSWER,
        "answer_correct": bool(body) and matches(spec, body),
        "sources_readable": readable,
        "no_invented_sources": corpus is None or all(u in corpus for u in urls),
        "evidence_cited": not spec.get("require_grounding") or any(u in evidence for u in urls),
    }
    reward = 1 if all(checks.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as f:
        json.dump({"reward": reward, "dimensions": {k: int(v) for k, v in checks.items()}}, f)
    print(json.dumps({"reward": reward, "checks": checks, "cited": urls}))


if __name__ == "__main__":
    main()
