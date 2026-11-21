"""Heimdall Modal helper (bench/sandbox/modal.go).

Modal's sandbox API is gRPC, reached only through its SDKs, so the Go driver
runs this script with the official modal SDK for each call. One JSON request
on stdin, one JSON answer on stdout:

    {"op": "check"}                                              -> {"ok": true}
    {"op": "create", "name", "image", "env", "tags", "cpu",
     "memory_mb", "timeout_s", "block_network", "allow_hosts",
     "allow_cidrs"}                                              -> {"sandbox_id"}
    {"op": "exec", "sandbox_id", "argv", "env", "cwd",
     "timeout_s"}                                                -> {"exit_code", "output" (base64)}
    {"op": "upload", "sandbox_id", "path", "data" (base64)}      -> {}
    {"op": "download", "sandbox_id", "path", "max_bytes"}        -> {"data" (base64)}
    {"op": "terminate", "sandbox_id"}                            -> {}

Errors: {"error": "<kind>", "message": "..."} with kind unauthorized,
not_found, too_large or failed, and exit code 1. The organization's token
comes in MODAL_TOKEN_ID and MODAL_TOKEN_SECRET; it is never printed.
"""

import base64
import json
import sys

APP_NAME = "heimdall-sandboxes"
OUTPUT_CAP = 1 << 20


def answer(obj, code=0):
    sys.stdout.write(json.dumps(obj))
    sys.stdout.flush()
    sys.exit(code)


def main():
    req = json.load(sys.stdin)
    try:
        import modal
        import modal.exception as mexc
    except ImportError:
        answer({"error": "failed", "message": "the modal Python package is not installed (pip install modal)"}, 1)

    def sandbox(sid):
        return modal.Sandbox.from_id(sid)

    op = req.get("op")
    try:
        if op == "check":
            modal.App.lookup(APP_NAME, create_if_missing=True)
            answer({"ok": True})
        if op == "create":
            kw = {
                "app": modal.App.lookup(APP_NAME, create_if_missing=True),
                "image": modal.Image.from_registry(req["image"]),
                "timeout": int(req.get("timeout_s") or 3600),
            }
            if req.get("name"):
                kw["name"] = req["name"]
            if req.get("env"):
                kw["env"] = req["env"]
            if req.get("cpu"):
                kw["cpu"] = float(req["cpu"])
            if req.get("memory_mb"):
                kw["memory"] = int(req["memory_mb"])
            hosts, cidrs = req.get("allow_hosts") or [], req.get("allow_cidrs") or []
            if req.get("block_network") or (not hosts and not cidrs):
                kw["block_network"] = True
            else:
                if hosts:
                    kw["outbound_domain_allowlist"] = hosts
                kw["outbound_cidr_allowlist"] = cidrs or []
            sb = modal.Sandbox.create("sleep", "infinity", **kw)
            if req.get("tags"):
                sb.set_tags({str(k): str(v)[:50] for k, v in req["tags"].items()})
            answer({"sandbox_id": sb.object_id})
        if op == "exec":
            sb = sandbox(req["sandbox_id"])
            kw = {"timeout": int(req.get("timeout_s") or 60), "text": False}
            if req.get("cwd"):
                kw["workdir"] = req["cwd"]
            if req.get("env"):
                kw["env"] = req["env"]
            p = sb.exec(*req["argv"], **kw)
            out = p.stdout.read() + p.stderr.read()
            code = p.wait()
            if isinstance(out, str):
                out = out.encode("utf-8", "replace")
            answer({"exit_code": code, "output": base64.b64encode(out[:OUTPUT_CAP]).decode()})
        if op == "upload":
            sb = sandbox(req["sandbox_id"])
            parent = req["path"].rsplit("/", 1)[0] or "/"
            sb.filesystem.make_directory(parent, create_parents=True)
            sb.filesystem.write_bytes(base64.b64decode(req["data"]), req["path"])
            answer({})
        if op == "download":
            sb = sandbox(req["sandbox_id"])
            mx = int(req["max_bytes"])
            if sb.filesystem.stat(req["path"]).size > mx:
                answer({"error": "too_large", "message": "file is larger than max_bytes"}, 1)
            data = sb.filesystem.read_bytes(req["path"])
            if len(data) > mx:
                answer({"error": "too_large", "message": "file is larger than max_bytes"}, 1)
            answer({"data": base64.b64encode(data).decode()})
        if op == "terminate":
            sandbox(req["sandbox_id"]).terminate()
            answer({})
        answer({"error": "failed", "message": "unknown op %r" % op}, 1)
    except mexc.AuthError as e:
        answer({"error": "unauthorized", "message": str(e)}, 1)
    except mexc.NotFoundError as e:
        answer({"error": "not_found", "message": str(e)}, 1)
    except Exception as e:  # noqa: BLE001 - every failure goes back to the driver as text
        answer({"error": "failed", "message": "%s: %s" % (type(e).__name__, e)}, 1)


if __name__ == "__main__":
    main()
