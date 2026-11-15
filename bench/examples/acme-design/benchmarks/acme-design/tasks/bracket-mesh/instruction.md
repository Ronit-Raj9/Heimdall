Our engineer left a drawing note for a mounting bracket in `drawing.md`.
Before the holes and fillets are added we need the bracket's blank as a
mesh.

Write the blank as an ASCII Wavefront OBJ file named `bracket.obj` in the
workspace root:

- A solid rectangular block 40 mm along X, 25 mm along Y and 10 mm along Z,
  with one corner at the origin, so it fills x from 0 to 40, y from 0 to 25
  and z from 0 to 10. One OBJ unit is one millimetre.
- A closed (watertight) surface made of `v` and `f` lines (triangles or
  quads): every edge is shared by exactly two faces.
- Every face wound counter-clockwise when seen from outside the block, so
  the face normals point outwards.
- Nothing else in the file: no other objects or loose geometry.

Only `bracket.obj` is read; do not change `drawing.md`.
