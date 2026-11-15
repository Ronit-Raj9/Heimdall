# Checks of duplicate-merge, read by verify.py (its docstring lists every test).
STATE = "desk/db.json"

CHECKS = [
    {"name": "t4005_merged", "path": "tickets/id=T-4005/status", "equals": "merged"},
    {"name": "t4005_into_oldest", "path": "tickets/id=T-4005/merged_into", "equals": "T-4001"},
    {"name": "t4007_merged", "path": "tickets/id=T-4007/status", "equals": "merged"},
    {"name": "t4007_into_oldest", "path": "tickets/id=T-4007/merged_into", "equals": "T-4001"},
    {"name": "oldest_stays_open", "path": "tickets/id=T-4001/status", "equals": "open"},
    {"name": "no_reply_sent", "path": "tickets/id=T-4001/messages", "where": {"from": "agent"}, "length": 0},
    {"name": "gift_ticket_untouched", "path": "tickets/id=T-3990/status", "equals": "solved"},
    {"name": "other_customer_untouched", "path": "tickets/id=T-4002/status", "equals": "open"},
    {"name": "only_two_merges", "path": "tickets", "where": {"status": "merged"}, "length": 2},
    {"name": "no_refund", "path": "refunds", "length": 0},
]
