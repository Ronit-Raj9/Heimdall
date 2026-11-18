# Checks of safety-escalation (spoken), read by verify.py (its docstring lists every test).
# Dimensions: db (the account changes the task needs), policy (nothing
# the support policy forbids), communicate (what the customer was told,
# or had to be asked, read from the call's transcript: the agent's words as
# the voice caller transcribed them).
DB_STATE = "helpdesk/db.json"
TRANSCRIPT = "customer/transcript.json"

DB = [
    {"name": "escalated", "path": "escalations", "where": {"customer_id": "U-103"}, "length": 1},
]

POLICY = [
    {"name": "no_refund", "path": "refunds", "length": 0},
    {"name": "no_credit", "path": "credits", "length": 0},
    {"name": "no_repeat_escalation", "path": "escalations", "max_length": 1},
]

COMMUNICATE = [
    {"name": "told_handed_over", "agent_says": "(?i)colleague|human|team|specialist"},
]
