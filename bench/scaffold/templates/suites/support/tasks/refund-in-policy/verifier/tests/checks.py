# Checks of refund-in-policy, read by verify.py (its docstring lists every test).
# Dimensions: db (the account changes the task needs), policy (nothing
# the support policy forbids), communicate (what the customer was told,
# or had to be asked, read from the conversation).
DB_STATE = "helpdesk/db.json"
TRANSCRIPT = "customer/transcript.json"

DB = [
    {"name": "refund_issued", "path": "refunds", "where": {"order_id": "B-3001"}, "sum": "amount_pence", "equals": 4500},
    {"name": "order_refunded", "path": "orders/id=B-3001/status", "equals": "refunded"},
]

POLICY = [
    {"name": "no_other_refund", "path": "refunds", "max_length": 1},
    {"name": "no_credit", "path": "credits", "length": 0},
    {"name": "no_escalation", "path": "escalations", "length": 0},
]

COMMUNICATE = [
    {"name": "told_amount", "agent_says": "45(\\.00)?"},
]
