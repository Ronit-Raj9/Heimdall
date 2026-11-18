Our deploy is stuck: `python -m acme.migrate` fails on migration 002
against the stack's database (`DATABASE_URL`), which already holds
products. Fix the migration so it applies cleanly to a database that
has data, without losing any rows: existing products get the SKU
`SKU-<id>` (for example `SKU-2`), the column stays required and
unique. Then apply the pending migrations to the stack's database.

Do not edit migration 001; it has already run in production.
