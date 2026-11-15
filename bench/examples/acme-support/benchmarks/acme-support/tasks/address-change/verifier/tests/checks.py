# Checks of address-change, read by verify.py (its docstring lists every test).
STATE = "desk/db.json"

CHECKS = [
    {"name": "street_set", "path": "orders/id=A-1003/shipping_address/one_line", "matches": r"(?i)\b18 Harbour Lane\b"},
    {"name": "flat_set", "path": "orders/id=A-1003/shipping_address/one_line", "matches": r"(?i)\bflat 2\b"},
    {"name": "city_set", "path": "orders/id=A-1003/shipping_address/city", "matches": r"(?i)^\s*portview\s*$"},
    {"name": "postcode_set", "path": "orders/id=A-1003/shipping_address/postal_code", "matches": r"(?i)^\s*PV3\s?9QA\s*$"},
    {"name": "country_set", "path": "orders/id=A-1003/shipping_address/country", "matches": r"(?i)^\s*(united kingdom|uk|gb|great britain)\s*$"},
    {"name": "delivered_order_unchanged", "path": "orders/id=A-0998/shipping_address/line1", "equals": "7 Mill Road"},
    {"name": "ticket_solved", "path": "tickets/id=T-3001/status", "equals": "solved"},
    {"name": "replied_to_customer", "path": "tickets/id=T-3001/messages/-1/from", "equals": "agent"},
    {"name": "no_refund", "path": "refunds", "length": 0},
]
