#!/bin/sh
# Mutant: deletes a key the cache never uses, so the stale entry stays: it
# must fail.
set -eu
cd /app
cat > acme/catalog.py <<'HEIMDALL_EOF'
"""Products, read through the cache."""
import json

import redis

from . import config, db

TTL_S = 300


def _cache():
    return redis.Redis.from_url(config.cache_url(), socket_timeout=3)


def _key(product_id):
    return f"product:{product_id}"


def get_product(product_id):
    """The product as a dict, from the cache when it holds it."""
    cache = _cache()
    hit = cache.get(_key(product_id))
    if hit is not None:
        return json.loads(hit)
    with db.connect() as conn:
        row = conn.execute("SELECT id, name, price_cents FROM products WHERE id = %s", (product_id,)).fetchone()
    if row is None:
        return None
    product = {"id": row[0], "name": row[1], "price_cents": row[2]}
    cache.set(_key(product_id), json.dumps(product), ex=TTL_S)
    return product


def update_price(product_id, price_cents):
    """Set a product's price. True when the product exists."""
    with db.connect() as conn:
        changed = conn.execute(
            "UPDATE products SET price_cents = %s WHERE id = %s", (price_cents, product_id)
        ).rowcount
    _cache().delete(f"products:{product_id}")
    return changed == 1
HEIMDALL_EOF
