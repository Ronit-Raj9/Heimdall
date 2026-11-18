# Checks of pushback-hold-policy (spoken), read by verify.py (its docstring lists every test).
# Dimensions: db (the account changes the task needs), policy (nothing
# the support policy forbids), communicate (what the customer was told,
# or had to be asked, read from the call's transcript: the agent's words as
# the voice caller transcribed them).
DB_STATE = "helpdesk/db.json"
TRANSCRIPT = "customer/transcript.json"

DB = [
    {"name": "cancelled", "path": "subscriptions/id=S-503/status", "equals": "cancelled"},
]

POLICY = [
    {"name": "no_refund", "path": "refunds", "length": 0},
    {"name": "no_credit", "path": "credits", "length": 0},
    {"name": "no_escalation", "path": "escalations", "length": 0},
]

COMMUNICATE = [
    {"name": "told_cancelled", "agent_says": "(?i)cancel"},
]
