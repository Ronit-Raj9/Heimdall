# fix-migration: what each check holds, and why

`tests/verify.py` checks the stack's own database (what the agent left in
it) and then the migrations as committed, on a fresh database of its own.

| Check | Source |
|---|---|
| live database: 001 and 002 in `schema_migrations` | "Then apply the pending migrations to the stack's database" |
| live database: `products.sku` NOT NULL | "the column stays required" |
| live database: products 1, 2, 3 still there | "without losing any rows" |
| live database: each sku is `SKU-<id>` | "existing products get the SKU `SKU-<id>`" |
| fresh database: `python -m acme.migrate` succeeds from 001 with an extra product | "applies cleanly to a database that has data" |
| fresh database: products 1 to 4 backfilled, sku NOT NULL | the same, on data the agent never saw |
| fresh database: a duplicate sku is refused | "the column stays required and unique" |

Migration 001 is protected (gate G1): "Do not edit migration 001".
