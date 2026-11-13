import json
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def run(*args, cwd=None):
    env = {k: v for k, v in os.environ.items() if not k.startswith("GATEKEEPER_")}
    env["PYTHONPATH"] = str(ROOT)
    return subprocess.run(
        [sys.executable, "-m", "gatekeeper", *args],
        cwd=cwd or ROOT, env=env, capture_output=True, text=True, timeout=60,
    )


def test_config_prints_defaults():
    result = run("config")
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout)["limiter"]["limit"] == 60


def test_config_reads_file(tmp_path):
    path = tmp_path / "g.toml"
    path.write_text('name = "edge"\n', encoding="utf-8")
    result = run("config", "--file", str(path))
    assert json.loads(result.stdout)["name"] == "edge"


def test_simulate_prints_one_line_per_event():
    result = run("simulate", "--limit", "2", "--window", "60", "a@0", "a@1", "a@2", "b@3")
    assert result.returncode == 0, result.stderr
    assert result.stdout.splitlines() == [
        "a@0 allow remaining=1",
        "a@1 allow remaining=0",
        "a@2 deny remaining=0",
        "b@3 allow remaining=1",
    ]


def test_simulate_rejects_bad_event():
    result = run("simulate", "a@soon")
    assert result.returncode == 1
    assert "gatekeeper: error:" in result.stderr


def test_dedupe_prints_groups(tmp_path):
    path = tmp_path / "users.jsonl"
    rows = [{"email": "a@x.org"}, {"email": "b@x.org"}, {"email": " A@x.org"}]
    path.write_text("\n".join(json.dumps(r) for r in rows) + "\n", encoding="utf-8")
    result = run("dedupe", str(path), "--key", "email")
    assert result.returncode == 0, result.stderr
    assert json.loads(result.stdout) == [[0, 2]]
