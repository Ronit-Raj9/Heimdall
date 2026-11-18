# Database tool service of an imported MultiAgentBench database task
# (docs/importers-marble.md). Standard library only; runs with the helper
# image's python3 beside a stock PostgreSQL container (service "db").
#
# At start it waits for PostgreSQL, runs the task's init SQL (the upstream
# init_sql, statement by statement, as marble/environments/db_env.py does),
# creates pg_stat_statements and replays the task's anomalies with a scaled,
# sequential replica of the upstream anomaly trigger
# (marble/environments/db_env_docker/anomaly_trigger/anomaly.py). Only then
# does /healthz answer 200, so the agents start on a database that already
# carries the anomaly's traces. Agents reach it only through the run's tool
# proxy: POST /query {"sql": "..."} runs the SQL like upstream's query_db
# action and answers with the rows of the last statement.
#
# PostgreSQL is reached over its simple query protocol (v3) with trust
# authentication on the run's internal network: the helper image has no
# PostgreSQL driver, and the network has no route out.
import argparse, json, socket, struct, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DB_HOST, DB_PORT, DB_USER, DB_NAME = "db", 5432, "test", "sysbench"
MAX_SQL = 64 * 1024
MAX_ROWS = 500
MAX_CELL = 2000

state = {"ready": False, "error": "", "log": []}


class PGError(Exception):
    pass


class Conn:
    """One PostgreSQL connection speaking the simple query protocol."""

    def __init__(self, timeout=30.0, app="marble-dbtool"):
        self.s = socket.create_connection((DB_HOST, DB_PORT), timeout=timeout)
        params = {"user": DB_USER, "database": DB_NAME, "application_name": app,
                  "options": "-c statement_timeout=%d" % int(timeout * 1000)}
        body = struct.pack("!i", 196608)
        for k, v in params.items():
            body += k.encode() + b"\0" + v.encode() + b"\0"
        body += b"\0"
        self.s.sendall(struct.pack("!i", len(body) + 4) + body)
        while True:
            t, payload = self._read()
            if t == b"R":
                (code,) = struct.unpack("!i", payload[:4])
                if code != 0:
                    raise PGError("server asked for authentication method %d; the bench runs PostgreSQL with trust" % code)
            elif t == b"E":
                raise PGError(_error(payload))
            elif t == b"Z":
                return

    def _recv(self, n):
        out = b""
        while len(out) < n:
            chunk = self.s.recv(n - len(out))
            if not chunk:
                raise PGError("connection closed by the server")
            out += chunk
        return out

    def _read(self):
        t = self._recv(1)
        (n,) = struct.unpack("!i", self._recv(4))
        return t, self._recv(n - 4)

    def query(self, sql):
        """Runs sql (one or more statements). Returns (columns, rows, tag) of
        the last statement that returned or changed rows; raises PGError with
        the server's message on the first failing statement."""
        q = sql.encode() + b"\0"
        self.s.sendall(b"Q" + struct.pack("!i", len(q) + 4) + q)
        cols, rows, tag, err = [], [], "", None
        cur_cols, cur_rows = None, []
        while True:
            t, p = self._read()
            if t == b"T":
                (n,) = struct.unpack("!h", p[:2])
                i, cur_cols, cur_rows = 2, [], []
                for _ in range(n):
                    j = p.index(b"\0", i)
                    cur_cols.append(p[i:j].decode("utf-8", "replace"))
                    i = j + 1 + 18
            elif t == b"D":
                (n,) = struct.unpack("!h", p[:2])
                i, row = 2, []
                for _ in range(n):
                    (ln,) = struct.unpack("!i", p[i:i + 4])
                    i += 4
                    if ln < 0:
                        row.append(None)
                    else:
                        row.append(p[i:i + ln].decode("utf-8", "replace"))
                        i += ln
                if len(cur_rows) < MAX_ROWS:
                    cur_rows.append(row)
            elif t == b"C":
                tag = p.rstrip(b"\0").decode()
                if cur_cols is not None:
                    cols, rows = cur_cols, cur_rows
                cur_cols, cur_rows = None, []
            elif t == b"E":
                err = _error(p)
            elif t == b"Z":
                if err:
                    raise PGError(err)
                return cols, rows, tag

    def close(self):
        try:
            self.s.sendall(b"X" + struct.pack("!i", 4))
            self.s.close()
        except OSError:
            pass


def _error(p):
    fields = {}
    for part in p.split(b"\0"):
        if part:
            fields[chr(part[0])] = part[1:].decode("utf-8", "replace")
    return fields.get("M", "error") + (" (" + fields["C"] + ")" if "C" in fields else "")


def run(sql, timeout=60.0):
    c = Conn(timeout=timeout, app="anomaly")
    try:
        return c.query(sql)
    finally:
        c.close()


def split_sql(sql):
    """Splits SQL on semicolons outside quotes, dollar quotes and comments."""
    out, cur, i, n = [], [], 0, len(sql)
    quote = None
    while i < n:
        ch = sql[i]
        if quote:
            if sql.startswith(quote, i):
                cur.append(quote)
                i += len(quote)
                quote = None
                continue
            cur.append(ch)
            i += 1
            continue
        if sql.startswith("--", i):
            j = sql.find("\n", i)
            i = n if j < 0 else j
            continue
        if sql.startswith("/*", i):
            j = sql.find("*/", i + 2)
            i = n if j < 0 else j + 2
            continue
        if ch in "'\"":
            quote = ch
        elif ch == "$":
            j = sql.find("$", i + 1)
            tag = sql[i:j + 1] if j > i else ""
            if tag and all(c.isalnum() or c == "_" for c in tag[1:-1]):
                quote = tag
                cur.append(tag)
                i = j + 1
                continue
        if ch == ";":
            s = "".join(cur).strip()
            if s:
                out.append(s)
            cur = []
        else:
            cur.append(ch)
        i += 1
    s = "".join(cur).strip()
    if s:
        out.append(s)
    return out


def wait_db(deadline_s):
    end = time.time() + deadline_s
    last = ""
    while time.time() < end:
        try:
            c = Conn(timeout=5)
            c.close()
            return
        except (OSError, PGError) as e:
            last = str(e)
            time.sleep(1)
    raise RuntimeError("PostgreSQL did not accept connections within %ds: %s" % (deadline_s, last))


def cols(n, size):
    return ", ".join("name%d varchar(%d)" % (i, size) for i in range(n))


def rand_cols(n, size):
    return ", ".join("(SELECT substr(md5(random()::text), 1, %d))" % size for _ in range(n))


def create_table(table, ncol, colsize):
    run("DROP TABLE IF EXISTS %s" % table)
    run("CREATE TABLE %s (id int, %s, time timestamp)" % (table, cols(ncol, colsize)))


def fill(table, nrow, ncol, colsize):
    run("INSERT INTO %s SELECT generate_series(1,%d), %s, now()" % (table, nrow, rand_cols(ncol, colsize)))


# Each replica runs the upstream anomaly's statements with the task's column
# count and size, sequentially, `repeats` times instead of `threads` threads
# for 60 seconds (setup.json says how far it was scaled). Unlike upstream it
# leaves its tables and indexes in place, so the evidence the agents' profiles
# point to (pg_stat_user_tables, pg_stat_user_indexes, pg_indexes) is there
# besides pg_stat_statements.

def insert_large_data(a, sc):
    t = sc["table"]
    create_table(t, a["ncolumn"], a["colsize"])
    sql = "INSERT INTO %s SELECT generate_series(1,%d),%s, NOW()" % (t, sc["nrow"], rand_cols(a["ncolumn"], a["colsize"]))
    for _ in range(sc["repeats"]):
        run(sql)


def lock_contention(a, sc):
    t = sc["table"]
    create_table(t, a["ncolumn"], a["colsize"])
    fill(t, sc["nrow"], a["ncolumn"], a["colsize"])
    # One session holds a row lock while the others update the same row, so
    # the updates wait on it (pg_stat_statements mean time, pg_locks while it
    # runs); upstream gets the waits from 100 concurrent updaters.
    holder = Conn(timeout=60, app="anomaly")
    holder.query("BEGIN; UPDATE %s SET name0 = 'held' WHERE id = 1" % t)
    errs = []

    def waiter(i):
        try:
            run("update %s set name%d=(SELECT substr(md5(random()::text), 1, %d)) where id =1" % (t, i % a["ncolumn"], a["colsize"]))
        except Exception as e:  # recorded, not fatal: the wait is the anomaly
            errs.append(str(e))

    ws = [threading.Thread(target=waiter, args=(i,)) for i in range(sc["waiters"])]
    for w in ws:
        w.start()
    time.sleep(sc["hold_s"])
    holder.query("COMMIT")
    holder.close()
    for w in ws:
        w.join(30)
    for i in range(sc["repeats"]):
        run("update %s set name%d=(SELECT substr(md5(random()::text), 1, %d)) where id =%d" % (t, i % a["ncolumn"], a["colsize"], 1 + i % max(1, sc["nrow"] - 1)))
    if errs:
        state["log"].append("lock waiters: %d errors, first: %s" % (len(errs), errs[0]))


def vacuum(a, sc):
    t = sc["table"]
    create_table(t, a["ncolumn"], a["colsize"])
    run("ALTER TABLE %s SET (autovacuum_enabled = false)" % t)
    fill(t, sc["nrow"], a["ncolumn"], a["colsize"])
    run("delete from %s where id < %d" % (t, int(sc["nrow"] * 0.9)))
    for _ in range(sc["vacuum_repeats"]):
        run("VACUUM FULL")


def redundant_index(a, sc):
    t = sc["table"]
    create_table(t, a["ncolumn"], a["colsize"])
    fill(t, sc["nrow"], a["ncolumn"], a["colsize"])
    nindex = int((5 * a["ncolumn"]) / 10)  # upstream: nindex (default 5) * ncolumns / 10
    for i in range(nindex):
        run("CREATE INDEX index_%s_%d ON %s(name%d)" % (t, i, t, i))
    run("CREATE INDEX index_%s_id ON %s(id)" % (t, t))
    for i in range(sc["repeats"]):
        run("update %s set name%d=(SELECT substr(md5(random()::text), 1, %d)) where id =%d" % (t, i % a["ncolumn"], a["colsize"], 1 + i % max(1, sc["nrow"] - 1)))


def fetch_large_data(a, sc):
    run("CREATE TABLE IF NOT EXISTS orders (o_orderkey int, o_orderpriority varchar(15), o_orderdate date)")
    run("CREATE TABLE IF NOT EXISTS lineitem (l_orderkey int, l_commitdate date, l_receiptdate date)")
    run("""INSERT INTO orders SELECT generate_series(1, %d),
        CASE WHEN random() > 0.5 THEN '1-URGENT' ELSE '5-LOW' END::varchar,
        (date '1996-03-01' + (random() * (date '1998-09-01' - date '1996-03-01'))::int)
        ON CONFLICT DO NOTHING""" % sc["orders"])
    for _ in range(sc["repeats"]):
        run("SELECT * FROM orders LIMIT 100")


REPLICAS = {
    "INSERT_LARGE_DATA": insert_large_data,
    "LOCK_CONTENTION": lock_contention,
    "VACUUM": vacuum,
    "REDUNDANT_INDEX": redundant_index,
    "FETCH_LARGE_DATA": fetch_large_data,
}


def setup(args):
    try:
        wait_db(args.wait)
        c = Conn(timeout=120, app="init")
        try:
            c.query("SET client_min_messages TO WARNING")
            with open(args.init_sql, encoding="utf-8") as f:
                for stmt in split_sql(f.read()):
                    c.query(stmt)
            c.query("RESET client_min_messages")
            c.query("CREATE EXTENSION IF NOT EXISTS pg_stat_statements")
        finally:
            c.close()
        with open(args.setup, encoding="utf-8") as f:
            spec = json.load(f)
        for a in spec.get("anomalies", []):
            fn = REPLICAS.get(a["anomaly"])
            if fn is None:
                raise RuntimeError("no replica for anomaly %s" % a["anomaly"])
            fn(a, spec["scale"])
            state["log"].append("replayed %s" % a["anomaly"])
        state["ready"] = True
    except Exception as e:
        state["error"] = "%s: %s" % (type(e).__name__, e)
        print("setup failed: " + state["error"], file=sys.stderr, flush=True)


def explain(sql, cols_, rows, tag):
    if rows:
        shown = [tuple(r) for r in rows]
        return "Your query on the database was successful. \nYour query is: %s \nResult: %s" % (sql, shown)
    return "Your query on the database was successful but no data was returned. \nYour query is: %s \nResult: [] (%s)" % (sql, tag)


class Handler(BaseHTTPRequestHandler):
    def answer(self, code, obj):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        if self.path == "/healthz":
            if state["ready"]:
                return self.answer(200, {"ok": True, "log": state["log"]})
            return self.answer(503 if not state["error"] else 500, {"ok": False, "error": state["error"]})
        if self.path in ("/", ""):
            return self.answer(200, {"tools": {"POST /query": {"sql": "SQL text, one or more statements; the rows of the last one are returned"}}})
        self.answer(404, {"error": "not found"})

    def do_POST(self):
        if self.path.rstrip("/") != "/query":
            return self.answer(404, {"error": "not found: POST /query"})
        n = int(self.headers.get("Content-Length") or 0)
        if n > MAX_SQL + 1024:
            return self.answer(400, {"status": "error", "explanation": "request over 64 KiB"})
        try:
            req = json.loads(self.rfile.read(n) or b"{}")
            sql = req["sql"]
            if not isinstance(sql, str) or not sql.strip():
                raise ValueError
        except (ValueError, KeyError, TypeError):
            return self.answer(400, {"status": "error", "explanation": 'body must be {"sql": "<SQL text>"}'})
        if not state["ready"]:
            return self.answer(503, {"status": "error", "explanation": "the database is not ready: " + (state["error"] or "still starting")})
        try:
            c = Conn(timeout=30)
            try:
                cols_, rows, tag = c.query(sql)
            finally:
                c.close()
        except (OSError, PGError) as e:
            return self.answer(200, {"status": "error", "function_name": "query_db",
                                     "explanation": "An error occurred while you tried to query the database: %s" % e})
        rows = [[(v[:MAX_CELL] if isinstance(v, str) else v) for v in r] for r in rows]
        self.answer(200, {"status": "success", "function_name": "query_db", "columns": cols_, "rows": rows,
                          "explanation": explain(sql, cols_, rows, tag)})

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--setup", required=True)
    ap.add_argument("--init-sql", required=True)
    ap.add_argument("--port", type=int, default=8080)
    ap.add_argument("--wait", type=int, default=150)
    args = ap.parse_args()
    threading.Thread(target=setup, args=(args,), daemon=True).start()
    ThreadingHTTPServer(("0.0.0.0", args.port), Handler).serve_forever()
