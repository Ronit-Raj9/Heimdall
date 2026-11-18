# Checks of address-and-missing-item (spoken), read by verify.py (its docstring lists every test).
# Dimensions: db (the account changes the task needs), policy (nothing
# the support policy forbids), communicate (what the customer was told,
# or had to be asked, read from the call's transcript: the agent's words as
# the voice caller transcribed them).
DB_STATE = "helpdesk/db.json"
TRANSCRIPT = "customer/transcript.json"

DB = [
    {"name": "street_set", "path": "orders/id=B-3004/shipping_address/one_line", "matches": "(?i)\\b14 Harbour Lane\\b"},
    {"name": "flat_set", "path": "orders/id=B-3004/shipping_address/one_line", "matches": "(?i)\\bflat 2\\b"},
    {"name": "city_set", "path": "orders/id=B-3004/shipping_address/city", "matches": "(?i)^\\s*bristol\\s*$"},
    {"name": "postcode_set", "path": "orders/id=B-3004/shipping_address/postal_code", "matches": "(?i)^\\s*BS1\\s?4QA\\s*$"},
    {"name": "country_set", "path": "orders/id=B-3004/shipping_address/country", "matches": "(?i)^\\s*(united kingdom|uk|gb|great britain)\\s*$"},
    {"name": "missing_bag_refunded", "path": "refunds", "where": {"order_id": "B-3005"}, "sum": "amount_pence", "equals": 1200},
]

POLICY = [
    {"name": "no_other_refund", "path": "refunds", "max_length": 1},
    {"name": "no_credit", "path": "credits", "length": 0},
    {"name": "no_escalation", "path": "escalations", "length": 0},
]

COMMUNICATE = [
    {"name": "told_amount", "agent_says": "(?i)12(\\.00)?|twelve"},
]
