# cache-invalidation: what each check holds, and why

`tests/verify.py` runs the app's catalogue code from the clean-room
workspace against a fresh database and the verifier's own Redis database
(index 9), so nothing the agent left in the stack can make a check pass.

| Check | Source |
|---|---|
| `get_product(1)` returns price 4200 | the catalogue still works |
| a change made behind the app's back is not seen within the TTL | "Keep the Redis cache in use for reads" |
| after `update_price(1, 3900)`, `get_product(1)` returns 3900 | "a price change is visible immediately" |
| product 2's cached entry survives product 1's price change | "do not throw away the cached entries of products whose price did not change" |
