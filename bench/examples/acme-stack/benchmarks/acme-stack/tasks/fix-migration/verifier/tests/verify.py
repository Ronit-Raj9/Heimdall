"""Hidden verifier: runs after the agent exited, in the app image, on the
clean-room workspace (/app: pristine image files plus the agent's diff), on
the run's internal network so it reaches db and cache by name. Writes
{"reward": 1} or {"reward": 0} to /grader/out/reward.json and prints why."""
import json
import os
import sys
import uuid

sys.path.insert(0, "/app")
import psycopg  # noqa: E402

URL = os.environ["DATABASE_URL"]
FAILS = []


def check(ok, msg):
    if not ok:
        FAILS.append(msg)
    return ok


def finish():
    with open("/grader/out/reward.json", "w") as f:
        json.dump({"reward": 0 if FAILS else 1}, f)
    for msg in FAILS:
        print("FAIL:", msg)
    print("PASS" if not FAILS else f"{len(FAILS)} check(s) failed")


class FreshDatabase:
    """A database of its own on the stack's server, created from template0
    and dropped afterwards, so nothing the agent left in the live database
    can make a check pass."""

    def __enter__(self):
        self.name = "verify_" + uuid.uuid4().hex[:12]
        with psycopg.connect(URL, autocommit=True, connect_timeout=5) as c:
            # From template0, which no connection can change: an agent that
            # altered template1 cannot plant objects in the verifier's database.
            c.execute(f'CREATE DATABASE "{self.name}" TEMPLATE template0')
        self.url = URL.rsplit("/", 1)[0] + "/" + self.name
        return self

    def __exit__(self, *exc):
        with psycopg.connect(URL, autocommit=True, connect_timeout=5) as c:
            c.execute(f'DROP DATABASE IF EXISTS "{self.name}" WITH (FORCE)')
        return False


import subprocess  # noqa: E402

M001 = open(os.path.join(os.path.dirname(__file__), "001_products.sql")).read()


def live_database():
    """What the agent left in the stack's own database: 002 applied, the
    three products kept, each backfilled as SKU-<id>, the column required."""
    with psycopg.connect(URL, autocommit=True, connect_timeout=5) as c:
        versions = sorted(r[0] for r in c.execute("SELECT version FROM schema_migrations"))
        check({"001", "002"} <= set(versions), f"live database: applied migrations {versions}, want 001 and 002")
        cols = dict(c.execute(
            "SELECT column_name, is_nullable FROM information_schema.columns WHERE table_name = 'products'").fetchall())
        if not check(cols.get("sku") == "NO", f"live database: products.sku is {cols.get('sku', 'missing')!r} nullable, want a NOT NULL column"):
            return
        rows = c.execute("SELECT id, sku FROM products ORDER BY id").fetchall()
        check([r[0] for r in rows] == [1, 2, 3], f"live database: product ids {[r[0] for r in rows]}, want [1, 2, 3]: no row may be lost")
        check(all(r[1] == f"SKU-{r[0]}" for r in rows), f"live database: skus {rows}, want SKU-<id> for every existing product")


def fresh_database():
    """The migrations as committed, on a database at 001 that holds data
    the live one does not (a fourth product), run by the app's own runner."""
    with FreshDatabase() as db:
        with psycopg.connect(db.url, autocommit=True) as c:
            c.execute("CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())")
            c.execute(M001)
            c.execute("INSERT INTO schema_migrations (version) VALUES ('001')")
            c.execute("INSERT INTO products (id, name, price_cents) VALUES (4, 'Tray', 1500)")
        env = dict(os.environ, DATABASE_URL=db.url, PYTHONDONTWRITEBYTECODE="1")
        r = subprocess.run([sys.executable, "-m", "acme.migrate"], cwd="/app", env=env, capture_output=True, text=True, timeout=60)
        if not check(r.returncode == 0, f"fresh database: python -m acme.migrate exited {r.returncode}: {r.stderr.strip()[-400:]}"):
            return
        with psycopg.connect(db.url, autocommit=True) as c:
            rows = c.execute("SELECT id, sku FROM products ORDER BY id").fetchall()
            check(rows == [(i, f"SKU-{i}") for i in (1, 2, 3, 4)], f"fresh database: products {rows}, want ids 1-4 each with SKU-<id>")
            nullable = c.execute("SELECT is_nullable FROM information_schema.columns WHERE table_name = 'products' AND column_name = 'sku'").fetchone()
            check(nullable == ("NO",), "fresh database: products.sku must be NOT NULL")
            try:
                c.execute("INSERT INTO products (id, name, price_cents, sku) VALUES (5, 'Dup', 1, 'SKU-1')")
                check(False, "fresh database: a duplicate sku was accepted; skus must stay unique")
            except psycopg.errors.UniqueViolation:
                pass


for step in (live_database, fresh_database):
    try:
        step()
    except Exception as e:  # a broken stack or a broken patch is a fail with the reason
        check(False, f"{step.__name__}: {type(e).__name__}: {e}")
finish()
