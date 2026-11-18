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

import redis  # noqa: E402

CACHE_DB = 9  # the verifier's own Redis database: the agent's keys live in 0


def run():
    with FreshDatabase() as db:
        os.environ["DATABASE_URL"] = db.url
        os.environ["CACHE_URL"] = f"redis://cache:6379/{CACHE_DB}"
        r = subprocess.run([sys.executable, "-m", "acme.migrate"], cwd="/app", env=dict(os.environ), capture_output=True, text=True, timeout=60)
        if not check(r.returncode == 0, f"python -m acme.migrate exited {r.returncode}: {r.stderr.strip()[-300:]}"):
            return
        cache = redis.Redis.from_url(os.environ["CACHE_URL"], socket_timeout=3)
        cache.flushdb()
        from acme import catalog

        def db_price(pid, cents):
            with psycopg.connect(db.url, autocommit=True) as c:
                c.execute("UPDATE products SET price_cents = %s WHERE id = %s", (cents, pid))

        first = catalog.get_product(1)
        check(first and first["price_cents"] == 4200, f"get_product(1) = {first}, want price 4200")
        catalog.get_product(2)
        # The cache must still be used: a change made behind the app's back
        # is not seen while the entry lives.
        db_price(1, 1)
        db_price(2, 2)
        cached = catalog.get_product(1)
        check(cached and cached["price_cents"] == 4200, "the cache is no longer used: get_product(1) read the database again within the TTL")
        db_price(1, 4200)
        # A price change through the app is visible at once.
        check(catalog.update_price(1, 3900), "update_price(1, 3900) reported no such product")
        after = catalog.get_product(1)
        check(after and after["price_cents"] == 3900, f"after update_price(1, 3900), get_product(1) = {after}: the stale cached price is still served")
        # Only that product's entry goes: the rest of the cache stays warm.
        other = catalog.get_product(2)
        check(other and other["price_cents"] == 2900, "update_price(1, ...) dropped product 2's cache entry too; invalidate only the changed product")
        cache.flushdb()


try:
    run()
except Exception as e:
    check(False, f"{type(e).__name__}: {e}")
finish()
