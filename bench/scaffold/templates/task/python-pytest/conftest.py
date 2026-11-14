"""Hidden-test bootstrap written by `bifrost-bench task init`.

pytest runs with rootdir /grader and ``python -I``, so neither the workspace
nor PYTHONPATH is on sys.path. Put the workspace first, then a src/ layout
and the pythonpath entries of the workspace's pytest settings (pyproject.toml
is reset to the task's own copy before grading). When the repository has a
root conftest.py, its fixtures and hooks are loaded from repo_conftest.py.
"""

import os
import sys

WORKSPACE = "/workspace"


def _workspace_paths():
    paths = [WORKSPACE]
    src = os.path.join(WORKSPACE, "src")
    if os.path.isdir(src):
        paths.append(src)
    try:
        import tomllib

        with open(os.path.join(WORKSPACE, "pyproject.toml"), "rb") as f:
            settings = tomllib.load(f)
        ini = settings.get("tool", {}).get("pytest", {}).get("ini_options", {})
        for entry in ini.get("pythonpath", []) or []:
            paths.append(os.path.normpath(os.path.join(WORKSPACE, entry)))
    except (OSError, ValueError, ImportError):
        pass
    return paths


for _path in reversed(_workspace_paths()):
    if _path not in sys.path:
        sys.path.insert(0, _path)

if os.path.exists(os.path.join(os.path.dirname(__file__), "repo_conftest.py")):
    sys.path.insert(0, os.path.dirname(__file__))
    from repo_conftest import *  # noqa: E402,F401,F403
