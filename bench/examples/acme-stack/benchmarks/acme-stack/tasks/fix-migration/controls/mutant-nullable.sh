#!/bin/sh
# Mutant: adds the column without making it required. It applies, but
# products.sku stays nullable: it must fail.
set -eu
cd /app
cat > migrations/002_add_sku.sql <<'HEIMDALL_EOF'
ALTER TABLE products ADD COLUMN sku TEXT;
UPDATE products SET sku = 'SKU-' || id;
CREATE UNIQUE INDEX products_sku ON products (sku);
HEIMDALL_EOF
python -m acme.migrate
