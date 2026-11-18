"""Structure check of the primary-button task (hidden).

Reads the exported page at /grader/artifacts/site/ and writes
/grader/out/reward.json with one dimension per check: the page still holds
its heading, both inputs and the Save changes button with class primary.
What the page looks like is checked by the visual grader, not here. To run
it by hand:

  GRADER_ARTIFACTS=<folder holding site/index.html> GRADER_OUT=<folder> python3 verify.py
"""
import json
import os
import re

SITE = os.path.join(os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts"), "site")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")


def read(name):
    try:
        with open(os.path.join(SITE, name), encoding="utf-8") as fh:
            return fh.read()
    except OSError:
        return ""


html = read("index.html")
dims = {
    "heading": 1.0 if "<h1>Account settings</h1>" in html else 0.0,
    "inputs": 1.0 if len(re.findall(r"<input ", html)) == 2 else 0.0,
    "button": 1.0 if re.search(r'<button class="primary"[^>]*>Save changes</button>', html) else 0.0,
    "stylesheet": 1.0 if read("style.css").strip() else 0.0,
}
os.makedirs(OUT_DIR, exist_ok=True)
with open(os.path.join(OUT_DIR, "reward.json"), "w") as fh:
    json.dump({"reward": min(dims.values()), "dimensions": dims}, fh)
