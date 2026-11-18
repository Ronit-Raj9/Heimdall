#!/bin/sh
# Mutant: makes the button blue with white text but leaves it at the left.
# The colour check passes; the region check (button not where the reference
# has it) and the page SSIM must fail it.
exec python3 -I - <<'PY'
p = "site/style.css"
css = open(p).read()
css = css.replace("background: #e5e7eb;\n  color: #111827;\n  border: 0;", "background: #1d4ed8;\n  color: #ffffff;\n  border: 0;")
open(p, "w").write(css)
PY
