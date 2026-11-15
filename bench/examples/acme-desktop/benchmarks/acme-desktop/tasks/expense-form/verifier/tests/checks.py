# Checks for expense-form, read by verify.py beside it. STATE is the desktop's
# state dump; the form app appends each submission to submitted.json, which
# the dump carries under json/claims (DUMP_JSON in task.yaml).
STATE = "desktop/state.json"

CHECKS = [
    {"name": "submitted_once", "path": "json/claims/submissions", "length": 1},
    {"name": "employee", "path": "json/claims/submissions/0/values/employee", "matches": "^(?i:dana whitfield)$"},
    {"name": "date", "path": "json/claims/submissions/0/values/date", "equals": "2026-03-12"},
    {"name": "amount", "path": "json/claims/submissions/0/values/amount", "matches": "^(EUR ?|€ ?)?86[.,]40( ?EUR| ?€)?$"},
    {"name": "category", "path": "json/claims/submissions/0/values/category", "equals": "Travel"},
    {"name": "description_names_taxi", "path": "json/claims/submissions/0/values/description", "matches": "(?i)taxi"},
    {"name": "receipt_ticked", "path": "json/claims/submissions/0/values/receipt", "equals": True},
]
