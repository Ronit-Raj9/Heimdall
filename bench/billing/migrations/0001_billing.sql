-- Plans, metering and billing (lane Y16, audit G4-10). Portable SQL: runs on
-- SQLite and Postgres. Times are unix milliseconds UTC.

-- One org's plan and its Stripe link. An org with no row is on the Free
-- plan. plan is free | team | enterprise; status is Stripe's subscription
-- status ('' when the org never subscribed). event_at is the created time
-- of the newest Stripe event applied, so an older event that arrives late
-- never overwrites a newer state.
CREATE TABLE billing_accounts (
    org_id          TEXT PRIMARY KEY,
    plan            TEXT NOT NULL DEFAULT 'free',
    status          TEXT NOT NULL DEFAULT '',
    customer_id     TEXT NOT NULL DEFAULT '',
    subscription_id TEXT NOT NULL DEFAULT '',
    seats           INTEGER NOT NULL DEFAULT 0,
    period_end      BIGINT NOT NULL DEFAULT 0,
    event_at        BIGINT NOT NULL DEFAULT 0,
    updated_by      TEXT NOT NULL DEFAULT '',
    updated_at      BIGINT NOT NULL
);
CREATE INDEX billing_accounts_customer ON billing_accounts (customer_id);

-- Metered usage per org, UTC day (YYYY-MM-DD) and meter. Counters
-- (shared_worker_minutes) add up; gauges (storage_bytes) keep the newest
-- reading of the day.
CREATE TABLE billing_meters (
    org_id     TEXT NOT NULL,
    day        TEXT NOT NULL,
    meter      TEXT NOT NULL,
    quantity   DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL,
    PRIMARY KEY (org_id, day, meter)
);

-- Stripe webhook events already applied (Stripe retries and may deliver an
-- event twice).
CREATE TABLE billing_events (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL,
    received_at BIGINT NOT NULL
);
