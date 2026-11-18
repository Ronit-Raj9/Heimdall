-- The stack's database as production has it: migration 001 applied.
CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
-- 001: the product catalogue.
CREATE TABLE products (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    price_cents INTEGER NOT NULL CHECK (price_cents >= 0)
);
INSERT INTO products (id, name, price_cents) VALUES (1, 'Kettle', 4200), (2, 'Teapot', 2900), (3, 'Mug', 900);
INSERT INTO schema_migrations (version) VALUES ('001');
