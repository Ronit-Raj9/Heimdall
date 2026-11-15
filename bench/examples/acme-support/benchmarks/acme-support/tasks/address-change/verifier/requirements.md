# address-change: what each check holds, and why

The instruction says to handle T-3001 following the support policy. The
customer asks for order A-1003 to go to 18 Harbour Lane, Flat 2, Portview,
PV3 9QA, United Kingdom. The checks in `tests/checks.py` follow from it:

| Check | Source |
|---|---|
| `street_set`, `flat_set` | The ticket's new address, wherever the agent put the flat (line 1 or line 2) |
| `city_set`, `postcode_set`, `country_set` | The ticket's new address; the postcode may be written with or without its space, the country as United Kingdom, UK, GB or Great Britain |
| `delivered_order_unchanged` | Rule 6: only an order still processing changes address; A-0998 was delivered |
| `ticket_solved`, `replied_to_customer` | Rule 6: reply confirming the new address and set the ticket to solved |
| `no_refund` | Nothing in the ticket asks for money |
