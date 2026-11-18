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
import time  # noqa: E402
import urllib.error  # noqa: E402
import urllib.request  # noqa: E402


def probe(port, deadline_s=8):
    """GET /healthz on a server started by serve(); (status, body) or None."""
    end = time.time() + deadline_s
    while time.time() < end:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=6) as r:
                return r.status, json.loads(r.read() or b"{}")
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read() or b"{}")
        except (urllib.error.URLError, ConnectionError, OSError):
            time.sleep(0.2)
    return None


def serve(port, database_url):
    env = dict(os.environ, DATABASE_URL=database_url, PYTHONDONTWRITEBYTECODE="1")
    return subprocess.Popen([sys.executable, "-m", "acme.server", "--port", str(port)], cwd="/app", env=env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)


def case(name, port, database_url, want_status, want_db):
    proc = serve(port, database_url)
    try:
        started = time.time()
        got = probe(port)
        took = time.time() - started
        if not check(got is not None, f"{name}: the server did not answer GET /healthz"):
            return
        status, body = got
        check(status == want_status, f"{name}: GET /healthz answered {status}, want {want_status}")
        check(isinstance(body, dict) and body.get("database") == want_db, f"{name}: body {body}, want database {want_db!r}")
        check(took < 7, f"{name}: GET /healthz took {took:.1f} s; it must answer within a few seconds")
    finally:
        proc.kill()
        proc.wait()


case("database up", 18181, URL, 200, "up")
# Port 1 on the database host refuses connections: the database is down.
case("database down", 18182, URL.replace("@db:5432/", "@db:1/"), 503, "down")
finish()
