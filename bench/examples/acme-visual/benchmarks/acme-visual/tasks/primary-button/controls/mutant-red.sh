#!/bin/sh
# Mutant: centres the button but makes it red (#dc2626) instead of blue.
# The structure check passes; the colour and region checks must fail it.
exec python3 -I - <<'PY'
p = "site/style.css"
css = open(p).read()
css = css.replace("justify-content: flex-start;", "justify-content: center;")
css = css.replace("background: #e5e7eb;\n  color: #111827;\n  border: 0;", "background: #dc2626;\n  color: #ffffff;\n  border: 0;")
open(p, "w").write(css)
PY
