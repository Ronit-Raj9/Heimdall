get("https://gmail.googleapis.com/gmail/v1/users/me/messages", q="from:compliance@company.example.com subject:Follow-up")
# Latest: msg_comp_002 (2026-01-28): "Task: Verify extinguisher inspection reports. Due 2026-02-14."
get("https://api.trello.com/1/boards/brd_ops/lists")
put("https://api.trello.com/1/cards/card_812", {"idBoard": "brd_ops", "idList": "lst_in_review", "due": "2026-02-14"})
post("https://api.trello.com/1/cards/card_812/idLabels", {"value": "lbl_compliance", "board": "brd_ops"})
post("https://3.basecampapi.com/acct_ops/buckets/proj_facilities/todosets/set_compliance/todolists/list_fire/todos",
     {"content": "Verify extinguisher inspection reports", "due_on": "2026-02-14"})
post("https://slack.com/api/chat.postMessage", {"channel": "COPSUPD", "text":
     "Compliance follow-up update: 'Verify extinguisher inspection reports' due 2026-02-14. Trello card card_812 moved to In Review with label lbl_compliance and due date set; Basecamp todo created in list_fire."})
