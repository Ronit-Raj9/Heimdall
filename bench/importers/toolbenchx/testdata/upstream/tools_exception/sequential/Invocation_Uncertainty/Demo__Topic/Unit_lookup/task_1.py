# Synthetic fixture tool module with a fault (not upstream data).
from typing import Any, Dict, Optional

_CALLS = {"n": 0}


def get_unit(user_prompt: str = "") -> Dict[str, Any]:
    """Look up a unit; the first call times out."""
    _CALLS["n"] += 1
    if _CALLS["n"] == 1:
        raise TimeoutError("transient")
    return {"unit": "U-7"}


def count_stock(unit: Optional[str] = None, user_prompt: str = "") -> Dict[str, Any]:
    """Count stock of a unit."""
    return {"final_value": 42}
