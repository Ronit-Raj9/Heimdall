#!/usr/bin/env python3
"""Verifier of an imported BrowseComp-Plus task (docs/importers-research.md).

It runs after the agent exited, with no network, in a fresh container. The
agent's two artifacts are read-only at /grader/artifacts:

  answer/answer.txt     the answer: the exact answer alone, or BrowseComp-Plus's
                        response format with an "Exact Answer:" line
  sources/sources.json  the cited pages: a list of URLs, a list of
                        {"url": ...}, or {"sources": [...]} (optional)

This folder holds answer.enc.json, the expected answer in the form
BrowseComp-Plus publishes it (base64 of the UTF-8 answer XORed with the
sha256 of the canary string, the authors' scheme), research.json (the
research track's spec: the question and the evidence pages) and
corpus_urls.json (every page of the suite's corpus). The answer is decrypted
in memory only; this script never prints it, nor the agent's answer.

The reward is answer_correct: BrowseComp-Plus grades the answer only. The
answer is compared after normalisation (Unicode NFKC, lower case,
punctuation, symbols and spaces turned into single spaces, a leading "the",
"a" or "an" dropped); when the expected answer is a number, the answer must
be the same number ("1,200" and "1200" agree). The importer only writes
tasks whose answers are short and exact this way; longer answers need a
judge and go to a judged suite. The citation checks are reported as
dimensions and do not decide.
"""

import base64
import hashlib
import json
import os
import re
import unicodedata
from urllib.parse import urlsplit, urlunsplit

TESTS = os.path.dirname(os.path.abspath(__file__))
ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
MAX_ANSWER = 64 << 10
SECTION = re.compile(r"(?im)^[ \t]*(?:#+[ \t]*)?(?:\*\*)?sources?(?:\*\*)?[ \t]*(?::|$)")
EXACT = re.compile(r"(?im)^[ \t]*(?:\*\*)?exact answer(?:\*\*)?[ \t]*:(?:\*\*)?[ \t]*(.*)$")
URL = re.compile(r"https?://[^\s<>\"'`\]\)]+")
NUMBER = re.compile(r"[-+]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)")
ARTICLES = ("the", "a", "an")


def read(path, limit=MAX_ANSWER):
    try:
        with open(path, "rb") as f:
            return f.read(limit + 1).decode("utf-8", "replace")
    except OSError:
        return None


def decrypt(ciphertext_b64, password):
    """decrypt_string of BrowseComp-Plus's scripts_build_index/decrypt_dataset.py."""
    encrypted = base64.b64decode(ciphertext_b64)
    digest = hashlib.sha256(password.encode("utf-8")).digest()
    key = digest * (len(encrypted) // len(digest)) + digest[: len(encrypted) % len(digest)]
    return bytes(a ^ b for a, b in zip(encrypted, key)).decode("utf-8")


def normalize(s):
    s = unicodedata.normalize("NFKC", s).lower()
    s = "".join(" " if unicodedata.category(c)[0] in "PSZC" else c for c in s)
    words = s.split()
    if len(words) > 1 and words[0] in ARTICLES:
        words = words[1:]
    return " ".join(words)


def as_number(s):
    t = unicodedata.normalize("NFKC", s).strip().replace(",", "").replace("$", "").replace("%", "").strip()
    if NUMBER.fullmatch(t):
        return float(t)
    return None


def answer_matches(got, want):
    n = as_number(want)
    if n is not None:
        return as_number(got) == n
    return normalize(got) != "" and normalize(got) == normalize(want)


def exact_answer(body):
    """The text after the last "Exact Answer:" line, else the whole body."""
    m = EXACT.findall(body)
    if m:
        return m[-1].strip()
    return body.strip()


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


def cited(section):
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
        urls = URL.findall(section)
    out = []
    for u in urls:
        n = norm_url(u)
        if n and n not in out:
            out.append(n)
    return out, readable


def main():
    with open(os.path.join(TESTS, "answer.enc.json"), encoding="utf-8") as f:
        enc = json.load(f)
    with open(os.path.join(TESTS, "research.json"), encoding="utf-8") as f:
        spec = json.load(f)
    corpus = None
    try:
        with open(os.path.join(TESTS, "corpus_urls.json"), encoding="utf-8") as f:
            corpus = {norm_url(u) for u in json.load(f)}
    except OSError:
        pass
    want = decrypt(enc["answer"], enc["canary"])
    text = read(os.path.join(ARTIFACTS, "answer", "answer.txt")) or ""
    body, section = split_answer(text)
    got = exact_answer(body)
    urls, readable = cited(section)
    evidence = {norm_url(u) for u in spec.get("evidence_urls") or []}
    checks = {
        "answer_present": bool(got) and len(text) <= MAX_ANSWER,
        "answer_correct": bool(got) and len(text) <= MAX_ANSWER and answer_matches(got, want),
    }
    reports = {
        "sources_readable": readable,
        "no_invented_sources": corpus is None or all(u in corpus for u in urls),
        "evidence_cited": any(u in evidence for u in urls),
    }
    reward = 1 if all(checks.values()) else 0
    dims = {k: int(v) for k, v in checks.items()}
    dims.update({k: int(v) for k, v in reports.items()})
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as f:
        json.dump({"reward": reward, "dimensions": dims}, f)
    # Never print the expected answer or the agent's answer: run logs must
    # not hold BrowseComp-Plus answers in plain text.
    print(json.dumps({"reward": reward, "checks": checks, "reported": reports, "cited": urls}))


if __name__ == "__main__":
    main()
