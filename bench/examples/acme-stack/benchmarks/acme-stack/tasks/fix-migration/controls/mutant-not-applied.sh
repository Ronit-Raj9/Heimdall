#!/bin/sh
# Mutant: fixes the migration file but never applies it to the stack's
# database, which the instruction asks for: it must fail.
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
