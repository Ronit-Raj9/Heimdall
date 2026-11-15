# Checks of refund-ok, read by verify.py (its docstring lists every test).
STATE = "desk/db.json"

CHECKS = [
    {"name": "one_refund_on_a1001", "path": "refunds", "where": {"order_id": "A-1001"}, "length": 1},
    {"name": "refund_is_kettle_price", "path": "refunds", "where": {"order_id": "A-1001"}, "sum": "amount_cents", "equals": 4200},
    {"name": "no_other_refund", "path": "refunds", "length": 1},
    {"name": "order_refunded_4200", "path": "orders/id=A-1001/refunded_cents", "equals": 4200},
    {"name": "ticket_solved", "path": "tickets/id=T-1001/status", "equals": "solved"},
    {"name": "replied_to_customer", "path": "tickets/id=T-1001/messages/-1/from", "equals": "agent"},
    {"name": "reply_states_amount", "path": "tickets/id=T-1001/messages/-1/text", "matches": r"\b42(?:[.,]00)?\b|\b4,?200\b"},
    {"name": "other_tickets_untouched", "path": "tickets", "where": {"status": "open"}, "length": 6},
]
