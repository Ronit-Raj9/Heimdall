# Synthetic fixture tool module (not upstream data).
from typing import Any, Dict, Optional


def get_unit(user_prompt: str = "") -> Dict[str, Any]:
    """Look up a unit."""
    return {"unit": "U-7"}


def count_stock(unit: Optional[str] = None, user_prompt: str = "") -> Dict[str, Any]:
    """Count stock of a unit."""
    return {"final_value": 42}
