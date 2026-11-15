#!/bin/sh
# Mutant control: a clean, closed block, but 12 mm thick instead of 10 mm
# (a misread drawing). Admission requires it to fail on geometry.
exec python3 -I - <<'PY'
X, Y, Z = 40, 25, 12
verts = [(0, 0, 0), (X, 0, 0), (X, Y, 0), (0, Y, 0),
         (0, 0, Z), (X, 0, Z), (X, Y, Z), (0, Y, Z)]
quads = [(1, 4, 3, 2), (5, 6, 7, 8), (1, 2, 6, 5), (3, 4, 8, 7), (1, 5, 8, 4), (2, 3, 7, 6)]
with open("bracket.obj", "w") as fh:
    for v in verts:
        fh.write("v %g %g %g\n" % v)
    for q in quads:
        fh.write("f %d %d %d %d\n" % q)
PY
