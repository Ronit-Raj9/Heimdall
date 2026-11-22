post("https://www.googleapis.com/calendar/v3/calendars/cal_primary/events", {
    "summary": "Weekly Team Standup",
    "start": {"dateTime": "2026-08-10T14:00:00Z", "timeZone": "America/New_York"},
    "end": {"dateTime": "2026-08-10T14:30:00Z", "timeZone": "America/New_York"},
    "recurrence": ["RRULE:FREQ=WEEKLY;BYDAY=MO"],
    "attendees": [{"email": "alice@company.example.com"}, {"email": "bob@company.example.com"}, {"email": "carol@company.example.com"}]})
