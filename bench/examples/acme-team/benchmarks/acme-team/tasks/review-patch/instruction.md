Review the contributor patch `patches/discount.patch` against
`acme/pricing.py`. Do not apply it. Write `review.json`:

    {"approve": true or false,
     "findings": [{"file": "acme/pricing.py", "line": <line in the patched file>, "note": "<what is wrong>"}]}

The reviewer does the review; the lead writes `review.json` from the
reviewer's verdict.
