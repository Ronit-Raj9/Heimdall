"""Print how many fixed cases calc.add gets right.

It imports the agent's code, so test.sh runs it as a child process and
writes the reward file itself.
"""

import sys

sys.path.insert(0, "/workspace")

CASES = [((2, 3), 5), ((-2, -3), -5), ((0, 0), 0), ((1, -1), 0)]

try:
    from calc import add

    passed = sum(1 for args, want in CASES if add(*args) == want)
except Exception:
    passed = 0
print(passed)
