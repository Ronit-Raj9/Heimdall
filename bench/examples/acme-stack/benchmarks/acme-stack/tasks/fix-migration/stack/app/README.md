# Acme Shop

A small catalogue API: products in Postgres (`DATABASE_URL`), read through a
Redis cache (`CACHE_URL`). The stack runs from `compose.yaml`:

    python -m acme.migrate     # apply the migrations the database is missing
    python -m acme.server      # serve the API on :8080
