row = {"Name": "Carla Mendes", "Company": "Horizon Biotech", "Email": "carla.mendes@horizonbiotech.example.com",
       "Phone": "555-0142", "Source": "LinkedIn", "Status": "New"}
cur = get("https://sheets.googleapis.com/v4/spreadsheets/ss_leads/values/Leads")
headers = cur["values"][0]
post("https://sheets.googleapis.com/v4/spreadsheets/ss_leads/values/Leads:append", {"range": "Leads", "values": [[row.get(h, "") for h in headers]]}, valueInputOption="RAW")
