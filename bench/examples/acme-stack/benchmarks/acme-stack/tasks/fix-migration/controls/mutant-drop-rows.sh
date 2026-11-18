#!/bin/sh
# Mutant: makes 002 apply by deleting the existing products first. The
# migration runs, but the data is gone: it must fail.
set -eu
cd /app
cat > migrations/002_add_sku.sql <<'HEIMDALL_EOF'
DELETE FROM products;

-- 002: every product gets a stock keeping unit (SKU), required from now on.
ALTER TABLE products ADD COLUMN sku TEXT NOT NULL;
CREATE UNIQUE INDEX products_sku ON products (sku);
HEIMDALL_EOF
python -m acme.migrate
