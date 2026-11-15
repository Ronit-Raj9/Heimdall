#!/bin/sh
# Mutant control: the right size, but the top face is missing, so the mesh
# is open (and its signed volume is no longer the block's). Admission
# requires it to fail.
exec python3 -I - <<'PY'
X, Y, Z = 40, 25, 10
verts = [(0, 0, 0), (X, 0, 0), (X, Y, 0), (0, Y, 0),
         (0, 0, Z), (X, 0, Z), (X, Y, Z), (0, Y, Z)]
quads = [(1, 4, 3, 2), (1, 2, 6, 5), (3, 4, 8, 7), (1, 5, 8, 4), (2, 3, 7, 6)]
with open("bracket.obj", "w") as fh:
    for v in verts:
        fh.write("v %g %g %g\n" % v)
    for a, b, c, d in quads:
        fh.write("f %d %d %d\nf %d %d %d\n" % (a, b, c, a, c, d))
PY
