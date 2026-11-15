# refund-ok: what each check holds, and why

The instruction says to handle T-1001 following the support policy
(`get_refund_policy`). The checks in `tests/checks.py` follow from it:

| Check | Source |
|---|---|
| `one_refund_on_a1001` | Rule 1: the kettle arrived damaged 6 days after delivery, so a refund is allowed |
| `refund_is_kettle_price` | Rule 2: refund the affected items only, unit price times quantity: 1 x 4200 |
| `no_other_refund` | Rule 2: no refund of the tea or the shipping, and nothing on any other order |
| `order_refunded_4200` | The desk's own record of the same refund |
| `ticket_solved` | Rule 5: set the ticket to solved after a refund |
| `replied_to_customer` | Rule 5: reply to the customer |
| `reply_states_amount` | Rule 5: the reply confirms the amount (42.00, 42 or 4200 pence) |
| `other_tickets_untouched` | The instruction names T-1001 only; the six other open tickets stay open |
