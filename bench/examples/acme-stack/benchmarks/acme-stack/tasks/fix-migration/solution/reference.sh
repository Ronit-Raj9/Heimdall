#!/bin/sh
# Reference: backfill before the column becomes required, then apply the
# pending migrations to the stack's database, as the instruction asks.
set -eu
cd /app
cat > migrations/002_add_sku.sql <<'HEIMDALL_EOF'
-- 002: every product gets a stock keeping unit (SKU), required from now on.
-- Existing rows are backfilled as SKU-<id> before the column becomes required.
ALTER TABLE products ADD COLUMN sku TEXT;
UPDATE products SET sku = 'SKU-' || id;
ALTER TABLE products ALTER COLUMN sku SET NOT NULL;
CREATE UNIQUE INDEX products_sku ON products (sku);
HEIMDALL_EOF
python -m acme.migrate
