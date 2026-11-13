import pytest

from gatekeeper import DEFAULTS, ConfigError, default_config, load_config


def write(tmp_path, text):
    path = tmp_path / "gatekeeper.toml"
    path.write_text(text, encoding="utf-8")
    return path


def test_defaults_without_file():
    assert load_config() == DEFAULTS


def test_default_config_is_a_copy():
    cfg = default_config()
    cfg["limiter"]["exempt_keys"].append("x")
    assert DEFAULTS["limiter"]["exempt_keys"] == []


def test_file_values_merge_over_defaults(tmp_path):
    path = write(tmp_path, '[limiter]\nlimit = 5\n\n[log]\nlevel = "DEBUG"\n')
    cfg = load_config(path)
    assert cfg["limiter"]["limit"] == 5
    assert cfg["limiter"]["window"] == 60.0
    assert cfg["log"] == {"level": "DEBUG", "timestamps": True}
    assert cfg["name"] == "gatekeeper"


def test_integer_accepted_for_float_setting(tmp_path):
    cfg = load_config(write(tmp_path, "[limiter]\nwindow = 30\n"))
    assert cfg["limiter"]["window"] == 30.0
    assert isinstance(cfg["limiter"]["window"], float)


@pytest.mark.parametrize("text", [
    "[limiter]\nlimt = 5\n",
    "colour = 'blue'\n",
    "[limiter]\nlimit = '5'\n",
    "[limiter]\nenabled = 1\n",
    "[dedupe]\nkeys = 'email'\n",
    "limiter = 3\n",
    "[limiter\n",
])
def test_bad_files_raise_config_error(tmp_path, text):
    with pytest.raises(ConfigError):
        load_config(write(tmp_path, text))


def test_missing_file(tmp_path):
    with pytest.raises(ConfigError, match="not found"):
        load_config(tmp_path / "nope.toml")
