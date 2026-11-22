body = """Dear Bright Ideas Studio,

Statement of account as of 2026-02-01.

Unpaid invoices:
- WI-3001, dated 2026-01-05, amount due $3040 (current, 0-30 days)
- WI-3002, dated 2025-12-10, amount due $1500 (31-60 days, OVERDUE)

Total balance: $4,540

Aging breakdown:
- Current (0-30 days): $3,040
- 31-60 days: $1,500
- 61-90 days: $0
- 90+ days: $0

Thank you.
"""
send_email("pay@brightideas.example.com", "Monthly Account Statement - Bright Ideas Studio", body)
