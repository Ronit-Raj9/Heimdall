rows = get("https://sheets.googleapis.com/v4/spreadsheets/ss_serp/values/Data")
# Opportunities: snippet exists, owned by someone else, we rank top 10 (realistic to win).
# Excluded: how to choose crm (we own it), sales automation (pos 15), crm benefits (page sunset),
# crm implementation guide (unverified volume), what is a crm system (duplicate of 'what is crm').
# Added per instruction: 'crm pricing' (high-intent commercial target). Ordered by volume.
queue = [["what is crm", "4", "22000", "Featured snippet owned by competitor.com - high"],
         ["crm pricing", "6", "12000", "High-intent commercial keyword - target snippet - high"],
         ["crm vs erp", "8", "4500", "Featured snippet owned by other.com - medium"]]
post("https://sheets.googleapis.com/v4/spreadsheets/ss_optimize/values/Queue!A:D:append", {"values": queue}, valueInputOption="RAW")
