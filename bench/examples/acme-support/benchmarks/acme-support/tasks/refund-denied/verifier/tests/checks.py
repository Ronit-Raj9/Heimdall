# Checks of refund-denied, read by verify.py (its docstring lists every test).
STATE = "desk/db.json"

CHECKS = [
    {"name": "no_refund_at_all", "path": "refunds", "length": 0},
    {"name": "order_not_refunded", "path": "orders/id=A-1002/refunded_cents", "equals": 0},
    {"name": "ticket_solved", "path": "tickets/id=T-2001/status", "equals": "solved"},
    {"name": "replied_to_customer", "path": "tickets/id=T-2001/messages/-1/from", "equals": "agent"},
    {"name": "reply_names_30_day_rule", "path": "tickets/id=T-2001/messages/-1/text", "matches": r"(?i)\b(30|thirty)[- ]?days?\b"},
    {"name": "other_tickets_untouched", "path": "tickets", "where": {"status": "open"}, "length": 6},
]
