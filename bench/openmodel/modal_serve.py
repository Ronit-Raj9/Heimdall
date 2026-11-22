"""Heimdall open model helper (bench/openmodel/modal.go).

Deploys, lists and stops the Modal apps that serve an open model for an
organization, with the official modal package. One JSON request on stdin,
one JSON answer on stdout:

    {"op": "deploy", "app_name", "engine", "model", "served_name", "gpu",
     "args", "vllm_version", "sglang_version", "scaledown_s",
     "startup_timeout_s", "concurrency", "hf_token": bool}  -> {"url"}
    {"op": "stop", "app_name"}                               -> {"stopped": bool}
    {"op": "list"}                                           -> {"apps": [{"name", "app_id", "state", "tasks"}]}

The organization's Modal token comes in MODAL_TOKEN_ID and
MODAL_TOKEN_SECRET; the deployment's API key in HEIMDALL_SERVE_KEY and an
optional Hugging Face token in HF_TOKEN. None of them is ever printed: the
modal CLI's own output is captured, and only its last lines come back on a
failure, with the secrets removed.

Errors: {"error": "<kind>", "message": "..."} with kind unauthorized or
failed, and exit code 1.
"""

import json
import os
import subprocess
import sys
import tempfile

PREFIX = "heimdall-om-"

APP_SOURCE = r'''
import json
import os
import subprocess
import threading

import modal

CFG = json.loads(%(cfg)r)

# The engines JIT-compile kernels at start (vLLM's FlashInfer sampler, SGLang's
# FlashInfer attention), so the image is CUDA devel with nvcc, not a slim
# Python image: on debian_slim vLLM 0.33 fails with "Could not find nvcc".
_base = modal.Image.from_registry("nvidia/cuda:12.8.1-devel-ubuntu22.04", add_python="3.12").entrypoint([])
if CFG["engine"] == "sglang":
    image = (
        _base.apt_install("libnuma1")
        .uv_pip_install("sglang[all]==" + CFG["sglang_version"], "huggingface_hub[hf_transfer]")
        .env({"HF_HUB_ENABLE_HF_TRANSFER": "1"})
    )
else:
    image = (
        _base.uv_pip_install("vllm==" + CFG["vllm_version"], "huggingface_hub[hf_transfer]")
        .env({"HF_HUB_ENABLE_HF_TRANSFER": "1"})
    )

app = modal.App(CFG["app_name"])

_secret = {"HEIMDALL_SERVE_KEY": os.environ.get("HEIMDALL_SERVE_KEY", "")}
if CFG.get("hf_token"):
    _secret["HF_TOKEN"] = os.environ.get("HF_TOKEN", "")


@app.function(
    image=image,
    gpu=CFG["gpu"],
    min_containers=0,
    max_containers=1,
    scaledown_window=CFG["scaledown_s"],
    timeout=24 * 3600,
    secrets=[modal.Secret.from_dict(_secret)],
)
@modal.concurrent(max_inputs=CFG["concurrency"])
@modal.web_server(port=8000, startup_timeout=CFG["startup_timeout_s"])
def serve():
    key = os.environ["HEIMDALL_SERVE_KEY"]
    if CFG["engine"] == "sglang":
        cmd = ["python", "-m", "sglang.launch_server", "--model-path", CFG["model"],
               "--host", "0.0.0.0", "--port", "8000", "--served-model-name", CFG["served_name"],
               "--api-key", key]
    else:
        cmd = ["vllm", "serve", CFG["model"], "--host", "0.0.0.0", "--port", "8000",
               "--served-model-name", CFG["served_name"], "--api-key", key]
    proc = subprocess.Popen(cmd + CFG["args"])

    # A server that dies while loading never opens its port, and Modal would
    # hold the GPU container until the startup timeout: end the container
    # instead, so the failure shows at once and stops billing.
    def _watch():
        code = proc.wait()
        print("heimdall: the model server exited with code %%d" %% code, flush=True)
        os._exit(1)

    threading.Thread(target=_watch, daemon=True).start()
'''


def answer(obj, code=0):
    sys.stdout.write(json.dumps(obj))
    sys.stdout.flush()
    sys.exit(code)


def scrub(text):
    for k in ("MODAL_TOKEN_ID", "MODAL_TOKEN_SECRET", "HEIMDALL_SERVE_KEY", "HF_TOKEN"):
        v = os.environ.get(k)
        if v:
            text = text.replace(v, "[redacted]")
    return text


def modal_cli(*args, timeout=900):
    p = subprocess.run([sys.executable, "-m", "modal", *args], stdin=subprocess.DEVNULL,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    return p.returncode, p.stdout.decode("utf-8", "replace")


def fail_from(out):
    low = out.lower()
    kind = "unauthorized" if ("token" in low and ("invalid" in low or "unauthorized" in low)) else "failed"
    tail = "\n".join(scrub(out).strip().splitlines()[-12:])
    answer({"error": kind, "message": tail[-1500:]}, 1)


def main():
    req = json.load(sys.stdin)
    try:
        import modal  # noqa: F401
    except ImportError:
        answer({"error": "failed", "message": "the modal Python package is not installed (pip install modal)"}, 1)
    op = req.get("op")
    if op == "deploy":
        name = req["app_name"]
        if not name.startswith(PREFIX):
            answer({"error": "failed", "message": "app name must start with " + PREFIX}, 1)
        cfg = {k: req[k] for k in ("app_name", "engine", "model", "served_name", "gpu", "args", "vllm_version",
                                   "sglang_version", "scaledown_s", "startup_timeout_s", "concurrency")}
        cfg["hf_token"] = bool(req.get("hf_token"))
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "heimdall_open_model.py")
            with open(path, "w") as f:
                f.write(APP_SOURCE % {"cfg": json.dumps(cfg)})
            code, out = modal_cli("deploy", path, timeout=int(req.get("deploy_timeout_s") or 1500))
        if code != 0:
            fail_from(out)
        try:
            fn = modal.Function.from_name(name, "serve")
            url = fn.get_web_url()
        except Exception as e:  # noqa: BLE001
            answer({"error": "failed", "message": scrub("deployed, but the endpoint URL could not be read: %s" % e)}, 1)
        answer({"url": url})
    if op == "stop":
        name = req["app_name"]
        if not name.startswith(PREFIX):
            answer({"error": "failed", "message": "app name must start with " + PREFIX}, 1)
        code, out = modal_cli("app", "stop", name, "--yes", timeout=180)
        if code != 0:
            low = out.lower()
            if "not found" in low or "no app" in low or "could not find" in low:
                answer({"stopped": False})
            fail_from(out)
        answer({"stopped": True})
    if op == "list":
        code, out = modal_cli("app", "list", "--json", timeout=120)
        if code != 0:
            fail_from(out)
        try:
            start = out.index("[")
            rows = json.loads(out[start:])
        except ValueError:
            answer({"error": "failed", "message": "modal app list did not answer JSON"}, 1)
        apps = []
        for r in rows:
            n = r.get("description") or r.get("name") or ""
            if n.startswith(PREFIX):
                apps.append({"name": n, "app_id": r.get("app_id", ""), "state": r.get("state", ""),
                             "tasks": str(r.get("tasks", ""))})
        answer({"apps": apps})
    answer({"error": "failed", "message": "unknown op %r" % op}, 1)


if __name__ == "__main__":
    main()
