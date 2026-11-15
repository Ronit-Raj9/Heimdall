#!/usr/bin/env python3
"""Verifier of the palette-swatch task (Acme Design example, fictional).

The bench copies the agent's palette.svg out of the workspace after the
agent exits and mounts it read-only at /grader/artifacts/swatch/palette.svg.
The expected palette is written below, copied from the task's brief; this
script never reads the workspace. It writes /grader/out/reward.json:

  {"reward": 1, "dimensions": {"svg": 1, "colors": 1, "layout": 1}}

svg     the file is well-formed XML whose root is svg in the SVG namespace
colors  exactly five rect elements, in document order, each with the
        brief's hex as its fill (case does not matter) and a title child
        holding the colour's name
layout  every rect has numeric x, y, width and height, is at least 40 by
        40, no two rects overlap (touching edges are fine), and no element
        has a transform

The reward is 1 only when all three dimensions are 1. A file that is
missing or not well formed scores 0 on all three.

Run it by hand:
  GRADER_ARTIFACTS=<folder holding swatch/palette.svg> GRADER_OUT=<folder> python3 verify.py
"""

import json
import os
import re
import sys
import xml.etree.ElementTree as ET

ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
SVG_PATH = os.path.join(ARTIFACTS, "swatch", "palette.svg")
SVG_NS = "http://www.w3.org/2000/svg"

PALETTE = [
    ("Harbor Navy", "#1f3a5f"),
    ("Sandstone", "#d8c3a5"),
    ("Signal Coral", "#e85d4a"),
    ("Moss", "#5b7f4a"),
    ("Paper White", "#f7f4ee"),
]
MIN_SIDE = 40.0
NUMBER_RE = re.compile(r"^\s*-?[0-9]+(\.[0-9]+)?\s*$")


def local(tag):
    return tag.rsplit("}", 1)[-1] if isinstance(tag, str) else ""


def rects_of(root):
    return [el for el in root.iter() if local(el.tag) == "rect"]


def fill_of(rect):
    fill = rect.get("fill")
    style = rect.get("style") or ""
    m = re.search(r"(?:^|;)\s*fill\s*:\s*([^;]+)", style)
    if m:
        fill = m.group(1)
    return (fill or "").strip().lower()


def title_of(rect):
    for child in rect:
        if local(child.tag) == "title":
            return " ".join("".join(child.itertext()).split())
    return None


def check_colors(rects):
    notes = []
    if len(rects) != len(PALETTE):
        notes.append(f"{len(rects)} rect elements, want {len(PALETTE)}")
    for i, (rect, (name, hexval)) in enumerate(zip(rects, PALETTE), 1):
        fill, title = fill_of(rect), title_of(rect)
        if fill != hexval:
            notes.append(f"rect {i}: fill {fill!r}, want {hexval.upper()} ({name})")
        if title is None or title.lower() != name.lower():
            notes.append(f"rect {i}: title {title!r}, want {name!r}")
    return not notes, notes


def check_layout(root, rects):
    notes = []
    if any(el.get("transform") is not None for el in root.iter()):
        notes.append("an element has a transform")
    boxes = []
    for i, rect in enumerate(rects, 1):
        vals = {}
        for attr in ("x", "y", "width", "height"):
            raw = rect.get(attr, "0" if attr in ("x", "y") else None)
            if raw is None or not NUMBER_RE.match(raw):
                notes.append(f"rect {i}: {attr} {raw!r} is not a plain number")
                break
            vals[attr] = float(raw)
        else:
            if vals["width"] < MIN_SIDE or vals["height"] < MIN_SIDE:
                notes.append(f"rect {i}: {vals['width']:g} x {vals['height']:g}, want at least {MIN_SIDE:g} x {MIN_SIDE:g}")
            boxes.append((i, vals["x"], vals["y"], vals["x"] + vals["width"], vals["y"] + vals["height"]))
    for a in range(len(boxes)):
        for b in range(a + 1, len(boxes)):
            i, ax0, ay0, ax1, ay1 = boxes[a]
            j, bx0, by0, bx1, by1 = boxes[b]
            if min(ax1, bx1) > max(ax0, bx0) and min(ay1, by1) > max(ay0, by0):
                notes.append(f"rects {i} and {j} overlap")
    if not rects:
        notes.append("no rect elements")
    return not notes, notes


def main():
    dims = {"svg": 0, "colors": 0, "layout": 0}
    try:
        root = ET.parse(SVG_PATH).getroot()
    except (OSError, ET.ParseError) as exc:
        print(f"palette.svg: cannot parse: {exc}")
        root = None
    if root is not None:
        if root.tag != "{%s}svg" % SVG_NS:
            print(f"svg: root element is {root.tag!r}, want svg in the SVG namespace")
        else:
            dims["svg"] = 1
            print("svg: ok")
            rects = rects_of(root)
            for name, check in (("colors", lambda: check_colors(rects)), ("layout", lambda: check_layout(root, rects))):
                ok, notes = check()
                dims[name] = int(ok)
                print(f"{name}:", "ok" if ok else "; ".join(notes))
    reward = 1 if all(dims.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as fh:
        json.dump({"reward": reward, "dimensions": dims}, fh)
    print(json.dumps({"reward": reward, "dimensions": dims}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
