-- 002: every product gets a stock keeping unit (SKU), required from now on.
ALTER TABLE products ADD COLUMN sku TEXT NOT NULL;
CREATE UNIQUE INDEX products_sku ON products (sku);
