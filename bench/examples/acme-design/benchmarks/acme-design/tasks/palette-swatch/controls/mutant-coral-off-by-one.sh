#!/bin/sh
# Mutant control: the reference sheet, except that Signal Coral is #E85D4B
# instead of #E85D4A (one digit off, a typo a reviewer would miss by eye).
# Admission requires it to fail.
exec python3 -I - <<'PY'
from xml.sax.saxutils import escape

PALETTE = [
    ("Harbor Navy", "#1F3A5F"),
    ("Sandstone", "#D8C3A5"),
    ("Signal Coral", "#E85D4B"),
    ("Moss", "#5B7F4A"),
    ("Paper White", "#F7F4EE"),
]
SIDE, GAP, PAD = 120, 20, 20
width = PAD * 2 + len(PALETTE) * SIDE + (len(PALETTE) - 1) * GAP
out = ['<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="200" viewBox="0 0 %d 200">' % (width, width)]
for i, (name, hexval) in enumerate(PALETTE):
    x = PAD + i * (SIDE + GAP)
    out.append('  <rect x="%d" y="%d" width="%d" height="%d" fill="%s" stroke="#999999" stroke-width="1">'
               '<title>%s</title></rect>' % (x, PAD, SIDE, SIDE, hexval, escape(name)))
    out.append('  <text x="%d" y="%d" font-family="sans-serif" font-size="12">%s %s</text>'
               % (x, PAD + SIDE + 20, escape(name), hexval))
out.append("</svg>")
with open("palette.svg", "w") as fh:
    fh.write("\n".join(out) + "\n")
PY
