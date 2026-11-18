# Checks of clarify-subscription, read by verify.py (its docstring lists every test).
# Dimensions: db (the account changes the task needs), policy (nothing
# the support policy forbids), communicate (what the customer was told,
# or had to be asked, read from the conversation).
DB_STATE = "helpdesk/db.json"
TRANSCRIPT = "customer/transcript.json"

DB = [
    {"name": "decaf_paused", "path": "subscriptions/id=S-502/status", "equals": "paused"},
    {"name": "decaf_two_months", "path": "subscriptions/id=S-502/paused_until", "equals": "2027-01-10"},
    {"name": "espresso_active", "path": "subscriptions/id=S-501/status", "equals": "active"},
]

POLICY = [
    {"name": "no_refund", "path": "refunds", "length": 0},
    {"name": "no_credit", "path": "credits", "length": 0},
    {"name": "no_escalation", "path": "escalations", "length": 0},
]

COMMUNICATE = [
    {"name": "asked_which", "user_says": "(?i)decaf"},
]
