"""The ``heimdall`` command of the Python package: ``heimdall serve``.

    heimdall serve app/agent.py:graph --agent support-bot
    uvx --from https://<host>/downloads/<wheel> heimdall serve app.agent:crew

Imports the object, wraps it by type (heimdall.wrap) and serves it in pull
mode (heimdall.serve): this process connects out to Heimdall and answers the
evaluation's turns; nothing connects in. The server and token are --url and
HEIMDALL_URL / HEIMDALL_TOKEN, else what ``heimdall login`` (the Heimdall
binary) stored in ~/.config/heimdall/credentials.json.

Relaying an HTTP service (``heimdall serve http://... --map auto``), login and
connect are in the Heimdall binary.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from typing import List, Optional, Tuple

from .wrap import load_object, wrap


def credentials_path() -> str:
    d = os.environ.get("HEIMDALL_CONFIG_DIR")
    if not d:
        xdg = os.environ.get("XDG_CONFIG_HOME")
        d = os.path.join(xdg, "heimdall") if xdg else os.path.join(os.path.expanduser("~"), ".config", "heimdall")
    return os.path.join(d, "credentials.json")


def resolve(url: Optional[str], token_env: str = "HEIMDALL_TOKEN") -> Tuple[str, str]:
    """The server and token: explicit, then the environment, then the
    stored login (same rules as the binary)."""
    server = (url or os.environ.get("HEIMDALL_URL") or "").strip().rstrip("/")
    token = (os.environ.get(token_env) or "").strip()
    if token and server:
        return server, token
    try:
        with open(credentials_path(), "r", encoding="utf-8") as f:
            creds = json.load(f)
    except (OSError, ValueError):
        creds = {}
    if not server:
        server = (creds.get("default") or "").rstrip("/")
    if not token:
        token = ((creds.get("servers") or {}).get(server) or {}).get("token", "")
    return server, token


def agent_name_from(ref: str) -> str:
    mod, _, obj = ref.rpartition(":")
    base = os.path.splitext(os.path.basename(mod.replace(".", "/") if not mod.endswith(".py") else mod))[0]
    if base in ("agent", "main", "app", "__init__", ""):
        base = os.path.basename(os.getcwd()) or obj
    name = re.sub(r"[^a-z0-9._-]+", "-", base.lower()).strip("-._")[:63]
    return name or "my-agent"


def cmd_serve(args: argparse.Namespace) -> int:
    if args.target.startswith(("http://", "https://")):
        sys.stderr.write("relaying an HTTP service is in the Heimdall binary: heimdall serve %s --map auto\n" % args.target)
        return 2
    server, token = resolve(args.url, args.token_env)
    if not server or not token:
        sys.stderr.write("no Heimdall login: run `heimdall login -url https://your-heimdall` with the Heimdall binary, "
                         "or set HEIMDALL_URL and HEIMDALL_TOKEN\n")
        return 2
    try:
        obj = load_object(args.target, args.dir)
        fn, kind = wrap(obj)
    except Exception as e:  # noqa: BLE001 - report the import or type problem plainly
        sys.stderr.write("can not serve %s: %s: %s\n" % (args.target, type(e).__name__, e))
        return 2
    from .serve import serve  # Y11 pull mode

    agent = args.agent or agent_name_from(args.target)
    concurrency = args.concurrency
    if kind in ("crewai", "smolagents") and concurrency > 1:
        sys.stderr.write("note: a %s object answers one turn at a time; other workers wait\n" % kind)
    sys.stderr.write("serving %s (%s) as agent %s on %s with %d worker(s); Ctrl-C stops\n" % (args.target, kind, agent, server, concurrency))

    def log(msg: str) -> None:
        sys.stderr.write(msg + "\n")

    def on_error(turn, err) -> None:  # noqa: ANN001
        sys.stderr.write("turn %s failed: %s: %s\n" % (getattr(turn, "id", "?"), type(err).__name__, err))

    n = serve(agent, fn, url=server, token=token, concurrency=concurrency,
              max_turns=args.max_turns or None, on_error=on_error, log=log)
    sys.stderr.write("served %d turns\n" % n)
    return 0


def main(argv: Optional[List[str]] = None) -> int:
    p = argparse.ArgumentParser(prog="heimdall", description="Serve your agent to Heimdall (pull mode).")
    sub = p.add_subparsers(dest="cmd")
    s = sub.add_parser("serve", help="serve an agent object: heimdall serve file.py:object")
    s.add_argument("target", help="file.py:object or module:object")
    s.add_argument("--agent", default="", help="Heimdall agent name (default from the file or folder)")
    s.add_argument("--concurrency", type=int, default=1)
    s.add_argument("--max-turns", type=int, default=0, help="stop after this many turns (0: until Ctrl-C)")
    s.add_argument("--url", default="", help="Heimdall server (HEIMDALL_URL, else your login)")
    s.add_argument("--token-env", default="HEIMDALL_TOKEN", help="environment variable with the org API token")
    s.add_argument("--dir", default="", help="folder the target is relative to (default the current folder)")
    args = p.parse_args(argv)
    if args.cmd == "serve":
        return cmd_serve(args)
    p.print_help(sys.stderr)
    sys.stderr.write("\nlogin, connect and first-look are in the Heimdall binary (heimdall login, heimdall connect).\n")
    return 2


if __name__ == "__main__":
    sys.exit(main())
