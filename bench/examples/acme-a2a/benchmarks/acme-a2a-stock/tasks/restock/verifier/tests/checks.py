# Checks for this task, read by verify.py beside it (see its docstring for
# every test a check can use). STATE is the exported file the checks read:
# "<service id>/<export id>" from task.yaml.
STATE = "inventory/db.json"

CHECKS = [
    {"name": "bottle_stock_is_42", "path": "items/sku=W-100/stock", "equals": 42},
    {"name": "one_adjustment", "path": "adjustments", "length": 1},
    {"name": "adjustment_on_w100", "path": "adjustments/0/sku", "equals": "W-100"},
    {"name": "adjustment_adds_12", "path": "adjustments/0/delta", "equals": 12},
    {"name": "reason_names_delivery", "path": "adjustments/0/reason", "contains": "D-5531"},
    {"name": "small_bottle_unchanged", "path": "items/sku=W-101/stock", "equals": 12},
    {"name": "tote_unchanged", "path": "items/sku=T-200/stock", "equals": 0},
]
