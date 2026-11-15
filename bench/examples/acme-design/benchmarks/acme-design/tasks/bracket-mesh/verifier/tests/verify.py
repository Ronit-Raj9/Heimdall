#!/usr/bin/env python3
"""Verifier of the bracket-mesh task (Acme Design example, fictional).

The bench copies the agent's bracket.obj out of the workspace after the
agent exits and mounts it read-only at /grader/artifacts/mesh/bracket.obj.
This script never reads the workspace. It writes /grader/out/reward.json:

  {"reward": 1, "dimensions": {"geometry": 1, "watertight": 1}}

geometry    the bounding box is [0,40] x [0,25] x [0,10] within 0.01 mm and
            the enclosed volume is 10000 mm^3 within 0.5 percent (signed
            volume by the divergence theorem over the triangulated faces)
watertight  every edge is shared by exactly two faces, every directed edge
            is used once (consistent winding), and the signed volume is
            positive (faces wound counter-clockwise seen from outside)

The reward is 1 only when both dimensions are 1. A file that is missing or
does not parse as OBJ scores 0 on both.

Run it by hand:
  GRADER_ARTIFACTS=<folder holding mesh/bracket.obj> GRADER_OUT=<folder> python3 verify.py
"""

import json
import os
import sys

ARTIFACTS = os.environ.get("GRADER_ARTIFACTS", "/grader/artifacts")
OUT_DIR = os.environ.get("GRADER_OUT", "/grader/out")
MESH = os.path.join(ARTIFACTS, "mesh", "bracket.obj")

EXPECTED_MIN = (0.0, 0.0, 0.0)
EXPECTED_MAX = (40.0, 25.0, 10.0)
EXPECTED_VOLUME = 40.0 * 25.0 * 10.0
BOX_TOLERANCE = 0.01
VOLUME_TOLERANCE = 0.005
MAX_FACES = 200000


class ObjError(Exception):
    pass


def parse_obj(text):
    """Returns the vertices and the faces (lists of 0-based vertex indexes)."""
    vertices, faces = [], []
    for lineno, raw in enumerate(text.splitlines(), 1):
        line = raw.split("#", 1)[0].strip()
        if not line:
            continue
        parts = line.split()
        if parts[0] == "v":
            if len(parts) < 4:
                raise ObjError(f"line {lineno}: a vertex needs x, y and z")
            try:
                vertices.append(tuple(float(p) for p in parts[1:4]))
            except ValueError:
                raise ObjError(f"line {lineno}: vertex coordinates are not numbers")
        elif parts[0] == "f":
            if len(parts) < 4:
                raise ObjError(f"line {lineno}: a face needs at least three vertices")
            face = []
            for tok in parts[1:]:
                try:
                    idx = int(tok.split("/", 1)[0])
                except ValueError:
                    raise ObjError(f"line {lineno}: face index {tok!r} is not a number")
                if idx < 0:
                    idx = len(vertices) + idx
                else:
                    idx -= 1
                if idx < 0 or idx >= len(vertices):
                    raise ObjError(f"line {lineno}: face index {tok!r} names no vertex")
                face.append(idx)
            faces.append(face)
            if len(faces) > MAX_FACES:
                raise ObjError(f"more than {MAX_FACES} faces")
        elif parts[0] in ("vn", "vt", "o", "g", "s", "usemtl", "mtllib"):
            continue
        else:
            raise ObjError(f"line {lineno}: unsupported statement {parts[0]!r}")
    if not vertices or not faces:
        raise ObjError("no vertices or no faces")
    return vertices, faces


def triangles(faces):
    for face in faces:
        for i in range(1, len(face) - 1):
            yield face[0], face[i], face[i + 1]


def signed_volume(vertices, tris):
    total = 0.0
    for a, b, c in tris:
        (ax, ay, az), (bx, by, bz), (cx, cy, cz) = vertices[a], vertices[b], vertices[c]
        total += ax * (by * cz - bz * cy) - ay * (bx * cz - bz * cx) + az * (bx * cy - by * cx)
    return total / 6.0


def check_geometry(vertices, faces, volume):
    used = sorted({i for f in faces for i in f})
    pts = [vertices[i] for i in used]
    lo = tuple(min(p[k] for p in pts) for k in range(3))
    hi = tuple(max(p[k] for p in pts) for k in range(3))
    notes = []
    for k, axis in enumerate("xyz"):
        if abs(lo[k] - EXPECTED_MIN[k]) > BOX_TOLERANCE or abs(hi[k] - EXPECTED_MAX[k]) > BOX_TOLERANCE:
            notes.append(f"{axis} spans {lo[k]:g}..{hi[k]:g}, want {EXPECTED_MIN[k]:g}..{EXPECTED_MAX[k]:g}")
    if abs(abs(volume) - EXPECTED_VOLUME) > VOLUME_TOLERANCE * EXPECTED_VOLUME:
        notes.append(f"volume {abs(volume):.3f}, want {EXPECTED_VOLUME:g}")
    return not notes, notes


def check_watertight(faces, volume):
    directed = {}
    for face in faces:
        if len(set(face)) != len(face):
            return False, ["a face repeats a vertex"]
        for i in range(len(face)):
            edge = (face[i], face[(i + 1) % len(face)])
            directed[edge] = directed.get(edge, 0) + 1
    notes = []
    undirected = {}
    for (a, b), n in directed.items():
        key = (min(a, b), max(a, b))
        undirected[key] = undirected.get(key, 0) + n
    open_edges = sum(1 for n in undirected.values() if n != 2)
    if open_edges:
        notes.append(f"{open_edges} edges are not shared by exactly two faces")
    flipped = sum(1 for n in directed.values() if n != 1)
    if flipped:
        notes.append(f"{flipped} edges are wound inconsistently")
    if volume <= 0:
        notes.append(f"signed volume {volume:.3f} is not positive; faces point inwards")
    return not notes, notes


def grade():
    dims = {"geometry": 0, "watertight": 0}
    try:
        with open(MESH, encoding="utf-8") as fh:
            text = fh.read()
    except (OSError, UnicodeDecodeError) as exc:
        print(f"bracket.obj: cannot read: {exc}")
        return dims
    try:
        vertices, faces = parse_obj(text)
    except ObjError as exc:
        print(f"bracket.obj: not a usable OBJ mesh: {exc}")
        return dims
    volume = signed_volume(vertices, list(triangles(faces)))
    ok, notes = check_geometry(vertices, faces, volume)
    dims["geometry"] = int(ok)
    print("geometry:", "ok" if ok else "; ".join(notes))
    ok, notes = check_watertight(faces, volume)
    dims["watertight"] = int(ok)
    print("watertight:", "ok" if ok else "; ".join(notes))
    return dims


def main():
    dims = grade()
    reward = 1 if all(dims.values()) else 0
    os.makedirs(OUT_DIR, exist_ok=True)
    with open(os.path.join(OUT_DIR, "reward.json"), "w", encoding="utf-8") as fh:
        json.dump({"reward": reward, "dimensions": dims}, fh)
    print(json.dumps({"reward": reward, "dimensions": dims}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
