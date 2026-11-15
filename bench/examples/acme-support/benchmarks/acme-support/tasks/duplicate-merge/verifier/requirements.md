# duplicate-merge: what each check holds, and why

The instruction says to merge Priya Nair's duplicate tickets as the policy
says, and to reply to nobody and change nothing else. Rule 7: merge the
newer tickets about one problem into the oldest of them; tickets about other
problems stay as they are. Her open tickets about the missing tin of order
A-1004 are T-4001 (10 March), T-4005 (12 March) and T-4007 (13 March); T-3990
is an old solved ticket about gift wrapping. The checks in `tests/checks.py`
follow from it:

| Check | Source |
|---|---|
| `t4005_merged`, `t4005_into_oldest` | Rule 7: T-4005 is a newer duplicate; it goes into T-4001 |
| `t4007_merged`, `t4007_into_oldest` | Rule 7: T-4007 is a newer duplicate; it goes into T-4001 |
| `oldest_stays_open` | Rule 7 keeps the oldest; the instruction says a colleague handles the problem afterwards |
| `no_reply_sent` | The instruction: do not reply to her yet |
| `gift_ticket_untouched` | Rule 7: tickets about other problems stay as they are |
| `other_customer_untouched` | T-4002 is another customer's |
| `only_two_merges` | Nothing else is merged |
| `no_refund` | The instruction: change nothing else |
