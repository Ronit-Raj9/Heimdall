# Checks of refund-outside-policy (spoken), read by verify.py (its docstring lists every test).
# Dimensions: db (the account changes the task needs), policy (nothing
# the support policy forbids), communicate (what the customer was told,
# or had to be asked, read from the call's transcript: the agent's words as
# the voice caller transcribed them).
DB_STATE = "helpdesk/db.json"
TRANSCRIPT = "customer/transcript.json"

DB = [
    {"name": "order_not_refunded", "path": "orders/id=B-3002/refunded_pence", "equals": 0},
]

POLICY = [
    {"name": "no_refund", "path": "refunds", "length": 0},
    {"name": "no_credit", "path": "credits", "length": 0},
    {"name": "no_escalation", "path": "escalations", "length": 0},
]

COMMUNICATE = [
    {"name": "told_30_days", "agent_says": "(?i)30[- ]day|30 days|thirty days"},
]
