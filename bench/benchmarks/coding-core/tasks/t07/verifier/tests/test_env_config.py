import json
import os
import subprocess
import sys

import pytest

import gatekeeper
from gatekeeper import DEFAULTS, ConfigError, load_config

FILE_TEXT = """\
name = "from-file"

[limiter]
limit = 10
window = 5.0

[log]
level = "WARNING"
"""


@pytest.fixture
def config_file(tmp_path):
    path = tmp_path / "gatekeeper.toml"
    path.write_text(FILE_TEXT, encoding="utf-8")
    return path


def test_environment_beats_file_beats_defaults(config_file):
    cfg = load_config(config_file, env={
        "GATEKEEPER_LIMITER__LIMIT": "20",
        "GATEKEEPER_LOG__TIMESTAMPS": "false",
    })
    assert cfg["limiter"]["limit"] == 20
    assert cfg["limiter"]["window"] == 5.0
    assert cfg["limiter"]["enabled"] is True
    assert cfg["log"] == {"level": "WARNING", "timestamps": False}
    assert cfg["name"] == "from-file"
    assert cfg["dedupe"] == {"keys": ["email"]}
    # Without a file the environment applies over the defaults.
    cfg = load_config(env={"GATEKEEPER_LIMITER__WINDOW": "2.5"})
    assert cfg["limiter"]["window"] == 2.5
    assert cfg["limiter"]["limit"] == DEFAULTS["limiter"]["limit"]


def test_variable_names_for_nested_and_top_level_settings():
    cfg = load_config(env={
        "GATEKEEPER_LIMITER__EXEMPT_KEYS": " health, metrics ,,ops ",
        "GATEKEEPER_DEDUPE__KEYS": "",
        "GATEKEEPER_NAME": "edge-1",
        "GATEKEEPER_LOG__LEVEL": " debug ",
    })
    assert cfg["limiter"]["exempt_keys"] == ["health", "metrics", "ops"]
    assert cfg["dedupe"]["keys"] == []
    assert cfg["name"] == "edge-1"
    assert cfg["log"]["level"] == " debug "


BOOLEAN_WORDS = [
    ("1", True), ("true", True), ("TRUE", True), ("Yes", True), (" on ", True),
    ("0", False), ("false", False), ("No", False), ("OFF", False), (" 0\t", False),
]


def test_boolean_values():
    for raw, expected in BOOLEAN_WORDS:
        cfg = load_config(env={"GATEKEEPER_LIMITER__ENABLED": raw, "GATEKEEPER_LOG__TIMESTAMPS": raw})
        assert cfg["limiter"]["enabled"] is expected, raw
        assert cfg["log"]["timestamps"] is expected, raw


def test_numeric_values():
    cfg = load_config(env={"GATEKEEPER_LIMITER__LIMIT": " 120 ", "GATEKEEPER_LIMITER__WINDOW": "30"})
    assert cfg["limiter"]["limit"] == 120
    assert type(cfg["limiter"]["limit"]) is int
    assert cfg["limiter"]["window"] == 30.0
    assert type(cfg["limiter"]["window"]) is float


BAD_VALUES = [
    ("GATEKEEPER_LIMITER__LIMIT", "ten"),
    ("GATEKEEPER_LIMITER__LIMIT", "2.5"),
    ("GATEKEEPER_LIMITER__WINDOW", "soon"),
    ("GATEKEEPER_LIMITER__ENABLED", "maybe"),
    ("GATEKEEPER_LOG__TIMESTAMPS", ""),
]


def test_unconvertible_value_raises_config_error_naming_the_variable():
    for name, raw in BAD_VALUES:
        with pytest.raises(ConfigError) as info:
            load_config(env={name: raw})
        assert name in str(info.value), (name, raw)


def test_variables_that_name_no_setting_are_ignored(config_file):
    cfg = load_config(config_file, env={
        "gatekeeper_limiter__limit": "1",
        "Gatekeeper_Name": "lower",
        "GATEKEEPER_LIMITER_LIMIT": "2",
        "GATEKEEPER_LIMITER__BURST": "3",
        "GATEKEEPER_COLOUR": "blue",
        "LIMITER__LIMIT": "4",
    })
    assert cfg == load_config(config_file, env={})


def test_env_none_reads_os_environ_and_a_mapping_replaces_it(monkeypatch):
    for name in list(os.environ):
        if name.startswith("GATEKEEPER_"):
            monkeypatch.delenv(name)
    monkeypatch.setenv("GATEKEEPER_LIMITER__LIMIT", "7")
    assert load_config()["limiter"]["limit"] == 7
    assert load_config(env=None)["limiter"]["limit"] == 7
    assert load_config(env={})["limiter"]["limit"] == DEFAULTS["limiter"]["limit"]
    assert load_config(env={"GATEKEEPER_LIMITER__LIMIT": "9"})["limiter"]["limit"] == 9


def test_cli_config_reflects_the_process_environment(config_file, tmp_path):
    root = os.path.dirname(os.path.dirname(os.path.abspath(gatekeeper.__file__)))
    env = {k: v for k, v in os.environ.items() if not k.startswith("GATEKEEPER_")}
    env["PYTHONPATH"] = root
    env["GATEKEEPER_LIMITER__WINDOW"] = "15"
    env["GATEKEEPER_LIMITER__EXEMPT_KEYS"] = "a,b"
    result = subprocess.run(
        [sys.executable, "-m", "gatekeeper", "config", "--file", str(config_file)],
        cwd=tmp_path, env=env, capture_output=True, text=True, timeout=60,
    )
    assert result.returncode == 0, result.stderr
    shown = json.loads(result.stdout)
    assert shown["limiter"]["window"] == 15.0
    assert shown["limiter"]["limit"] == 10
    assert shown["limiter"]["exempt_keys"] == ["a", "b"]
    assert shown["name"] == "from-file"
