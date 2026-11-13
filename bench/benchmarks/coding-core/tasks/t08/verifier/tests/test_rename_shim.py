import json
import os
import subprocess
import sys
import warnings

import pytest

import gatekeeper
import gatekeeper.dedupe

MESSAGE = "find_duplicates() is deprecated; use duplicate_groups() instead"

RECORDS = [
    {"email": "a@x.org", "phone": "1"},
    {"email": "b@x.org", "phone": "2"},
    {"email": " A@X.ORG", "phone": "3"},
    {"email": "c@x.org", "phone": "2"},
    {"email": "d@x.org", "phone": "4"},
    {"email": "C@x.org", "phone": "5"},
]
EXPECTED = [[0, 2], [1, 3, 5]]


def deprecations(caught):
    return [w for w in caught if issubclass(w.category, DeprecationWarning)]


def run_python(*args, tmp_path):
    root = os.path.dirname(os.path.dirname(os.path.abspath(gatekeeper.__file__)))
    env = {k: v for k, v in os.environ.items() if not k.startswith(("GATEKEEPER_", "PYTHONWARNINGS"))}
    env["PYTHONPATH"] = root
    return subprocess.run(
        [sys.executable, "-W", "error::DeprecationWarning", *args],
        cwd=tmp_path, env=env, capture_output=True, text=True, timeout=60,
    )


def test_new_name_is_public():
    from gatekeeper import duplicate_groups

    assert "duplicate_groups" in gatekeeper.__all__
    assert gatekeeper.dedupe.duplicate_groups is duplicate_groups


def test_new_name_returns_tuples_and_takes_keys_by_keyword_only():
    with warnings.catch_warnings():
        warnings.simplefilter("error", DeprecationWarning)
        result = gatekeeper.duplicate_groups(RECORDS, keys=["email", "phone"])
        assert result == [tuple(g) for g in EXPECTED]
        assert type(result) is list
        assert all(type(group) is tuple for group in result)
        assert gatekeeper.duplicate_groups(RECORDS, keys=["email"]) == [(0, 2), (3, 5)]
        assert gatekeeper.duplicate_groups([], keys=["email"]) == []
    with pytest.raises(TypeError):
        gatekeeper.duplicate_groups(RECORDS, ["email"])


def test_old_name_still_public_and_unchanged():
    assert "find_duplicates" in gatekeeper.__all__
    assert gatekeeper.dedupe.find_duplicates is gatekeeper.find_duplicates
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", DeprecationWarning)
        positional = gatekeeper.find_duplicates(RECORDS, ["email", "phone"])
        keyword = gatekeeper.find_duplicates(RECORDS, keys=["email", "phone"])
        both_keyword = gatekeeper.find_duplicates(records=RECORDS, keys=("email",))
        with pytest.raises(ValueError):
            gatekeeper.find_duplicates(RECORDS, [])
    assert positional == keyword == EXPECTED
    assert type(positional) is list
    assert all(type(group) is list for group in positional)
    assert both_keyword == [[0, 2], [3, 5]]


def test_every_call_to_old_name_warns_once_with_exact_message():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        gatekeeper.find_duplicates(RECORDS, ["email"])
        gatekeeper.find_duplicates(RECORDS, keys=["phone"])
    found = deprecations(caught)
    assert len(found) == 2
    for w in found:
        assert w.category is DeprecationWarning
        assert str(w.message) == MESSAGE


def test_warning_points_at_the_caller():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        line = sys._getframe().f_lineno + 1
        gatekeeper.find_duplicates(RECORDS, ["email"])
    (w,) = deprecations(caught)
    assert os.path.basename(w.filename) == os.path.basename(__file__)
    assert w.lineno == line


def test_rest_of_the_api_does_not_warn():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        kept = gatekeeper.drop_duplicates(RECORDS, ["email", "phone"])
        gatekeeper.duplicate_groups(RECORDS, keys=["email"])
    assert deprecations(caught) == []
    assert kept == [RECORDS[0], RECORDS[1], RECORDS[4]]


def test_import_and_cli_do_not_warn_and_cli_output_is_unchanged(tmp_path):
    imported = run_python(
        "-c", "import gatekeeper; from gatekeeper import find_duplicates, duplicate_groups",
        tmp_path=tmp_path,
    )
    assert imported.returncode == 0, imported.stderr
    path = tmp_path / "users.jsonl"
    path.write_text("\n".join(json.dumps(r) for r in RECORDS) + "\n", encoding="utf-8")
    result = run_python("-m", "gatekeeper", "dedupe", str(path), "--key", "email", "--key", "phone",
                        tmp_path=tmp_path)
    assert result.returncode == 0, result.stderr
    assert result.stdout == json.dumps(EXPECTED) + "\n"
