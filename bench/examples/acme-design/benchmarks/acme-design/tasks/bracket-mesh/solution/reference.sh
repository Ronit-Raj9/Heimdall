#!/bin/sh
# Reference control: writes the bracket blank the way a correct agent would,
# 8 vertices and 12 triangles wound counter-clockwise seen from outside.
# Admission runs it in the agent's place and requires the verifier to pass
# the bracket.obj it leaves, three times in a row.
exec python3 -I - <<'PY'
X, Y, Z = 40, 25, 10
verts = [(0, 0, 0), (X, 0, 0), (X, Y, 0), (0, Y, 0),
         (0, 0, Z), (X, 0, Z), (X, Y, Z), (0, Y, Z)]
# Each quad is counter-clockwise seen from outside (outward normal).
quads = [(1, 4, 3, 2),  # bottom, -z
         (5, 6, 7, 8),  # top, +z
         (1, 2, 6, 5),  # front, -y
         (3, 4, 8, 7),  # back, +y
         (1, 5, 8, 4),  # left, -x
         (2, 3, 7, 6)]  # right, +x
with open("bracket.obj", "w") as fh:
    fh.write("# Wall bracket WB-7 blank, 40 x 25 x 10 mm\no blank\n")
    for v in verts:
        fh.write("v %g %g %g\n" % v)
    for a, b, c, d in quads:
        fh.write("f %d %d %d\nf %d %d %d\n" % (a, b, c, a, c, d))
PY
