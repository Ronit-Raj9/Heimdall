# What the hidden checks hold (review-patch)

| Check | Instruction sentence |
|---|---|
| review_json | "Write `review.json`" in the given shape |
| rejects_patch | "approve": the patch drops the first basket item, so it must not be approved |
| finds_skipped_first_item | "findings ... line in the patched file": acme/pricing.py line 14 (one line either way accepted) |
