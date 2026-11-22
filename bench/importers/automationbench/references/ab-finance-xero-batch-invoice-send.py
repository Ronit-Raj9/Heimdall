inv = get("https://api.xero.com/api.xro/2.0/Invoices")
# INV-6001: client asked to hold; 6003 already sent; 6004 HOLD ref; 6005 DRAFT.
post("https://api.xero.com/api.xro/2.0/Invoices/xi_102/Email", {})
post("https://slack.com/api/chat.postMessage", {"channel": "C_BILLING", "text": "Invoices sent: INV-6002 to Baxter Industries (ap@baxter.example.com), total 32000 (CFO cc'd, over $25,000)."})
send_email("ap@baxter.example.com", "Invoice INV-6002 from Company", "Hi Baxter Industries,\n\nPlease find invoice INV-6002, amount due 32000.\n\nThanks", cc="cfo@company.example.com")
