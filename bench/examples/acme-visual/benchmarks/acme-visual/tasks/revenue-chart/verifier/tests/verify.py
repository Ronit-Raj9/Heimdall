"""Structure check of the revenue-chart task (hidden).

Parses the exported /grader/artifacts/chart/revenue.svg and writes
/grader/out/reward.json with one dimension per check: well-formed XML with
an svg root, the 480 x 320 canvas, and four bars filled #2563eb. Whether the
chart looks right (heights, labels, positions) is checked by the visual
grader, not here. To run it by hand:

  GRADER_ARTIFACTS=<folder holding chart/revenue.svg> GRADER_OUT=<folder> python3 verify.py
"""
import json
import os
import xml.etree.ElementTree as ET

NS = "{http://www.w3.org/2000/svg}"
ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
dims = {"well_formed": 0.0, "canvas": 0.0, "bars": 0.0}
try:
    root = ET.parse(os.path.join(ARTIFACTS, "chart", "revenue.svg")).getroot()
    dims["well_formed"] = 1.0 if root.tag == NS + "svg" else 0.0
    dims["canvas"] = 1.0 if (root.get("width"), root.get("height")) == ("480", "320") else 0.0
    bars = [r for r in root.iter(NS + "rect") if (r.get("fill") or "").lower() == "#2563eb"]
    dims["bars"] = 1.0 if len(bars) == 4 else 0.0
except (OSError, ET.ParseError):
    pass
os.makedirs(OUT_DIR, exist_ok=True)
with open(os.path.join(OUT_DIR, "reward.json"), "w") as fh:
    json.dump({"reward": min(dims.values()), "dimensions": dims}, fh)
