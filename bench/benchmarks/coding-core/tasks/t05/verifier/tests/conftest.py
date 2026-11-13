"""Hidden-test setup: import the package under test from /workspace.

The grader runs pytest with ``python -I``, which leaves the current directory
off sys.path, so the workspace is put first explicitly.
"""

import sys

WORKSPACE = "/workspace"

if WORKSPACE in sys.path:
    sys.path.remove(WORKSPACE)
sys.path.insert(0, WORKSPACE)
