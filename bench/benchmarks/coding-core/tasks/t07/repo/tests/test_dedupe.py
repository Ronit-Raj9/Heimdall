import pytest

from gatekeeper import drop_duplicates, find_duplicates, normalise_key


def test_normalise_key():
    assert normalise_key("  Ada   LOVELACE ") == "ada lovelace"
    assert normalise_key(None) == ""
    assert normalise_key(7) == "7"


def test_single_key_groups():
    records = [
        {"email": "a@x.org"},
        {"email": "b@x.org"},
        {"email": "A@X.org "},
        {"email": "c@x.org"},
        {"email": "b@x.org"},
    ]
    assert find_duplicates(records, ["email"]) == [[0, 2], [1, 4]]


def test_any_key_field_can_match():
    records = [
        {"email": "a@x.org", "phone": "111"},
        {"email": "b@x.org", "phone": "222"},
        {"email": "c@x.org", "phone": "111"},
    ]
    assert find_duplicates(records, ["email", "phone"]) == [[0, 2]]


def test_no_duplicates():
    records = [{"email": f"user{i}@x.org"} for i in range(50)]
    assert find_duplicates(records, ["email"]) == []
    assert find_duplicates([], ["email"]) == []


def test_drop_duplicates_keeps_first():
    records = [
        {"id": 1, "email": "a@x.org"},
        {"id": 2, "email": "b@x.org"},
        {"id": 3, "email": "a@x.org"},
    ]
    assert [r["id"] for r in drop_duplicates(records, ["email"])] == [1, 2]


def test_keys_required():
    with pytest.raises(ValueError):
        find_duplicates([{"email": "a"}], [])
