#!/bin/sh
# Reference control: makes the change a correct agent would, in style.css
# only: the button blue with white text, its row centred. Admission runs it
# in the agent's place and requires the verifier and every visual check to
# pass what the bench renders, three times in a row.
exec python3 -I - <<'PY'
p = "site/style.css"
css = open(p).read()
css = css.replace("justify-content: flex-start;", "justify-content: center;")
css = css.replace("background: #e5e7eb;\n  color: #111827;\n  border: 0;", "background: #1d4ed8;\n  color: #ffffff;\n  border: 0;")
open(p, "w").write(css)
PY
