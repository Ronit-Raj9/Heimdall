# bracket-mesh: what each check holds, and why

The instruction asks for the bracket blank as `bracket.obj`: a solid block
40 x 25 x 10 mm with one corner at the origin, closed, faces pointing
outwards. `tests/verify.py` reads the exported file at
`/grader/artifacts/mesh/bracket.obj` and reports two dimensions:

| Check | Source |
|---|---|
| `geometry` | "40 mm along X, 25 mm along Y and 10 mm along Z, with one corner at the origin": bounding box [0,40] x [0,25] x [0,10] within 0.01 mm, enclosed volume 10000 mm^3 within 0.5 percent |
| `watertight` | "every edge is shared by exactly two faces" and "wound counter-clockwise when seen from outside": each edge used by two faces, each directed edge once, signed volume positive |

The reward is 1 only when both hold. The mutants each break one: a block
12 mm thick (`geometry`), and a block with no top face (`watertight`, and
its volume is wrong too).
