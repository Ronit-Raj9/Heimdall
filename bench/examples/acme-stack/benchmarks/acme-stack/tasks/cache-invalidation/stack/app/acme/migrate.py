"""Apply migrations/NNN_name.sql in order, each once, in its own transaction.

python -m acme.migrate applies what the database (DATABASE_URL) is missing and
records each version in schema_migrations.
"""
import pathlib

from . import db

MIGRATIONS = pathlib.Path(__file__).resolve().parent.parent / "migrations"


def migrate(url=None):
    applied = []
    with db.connect(url) as conn:
        conn.execute(
            "CREATE TABLE IF NOT EXISTS schema_migrations ("
            "version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"
        )
        done = {row[0] for row in conn.execute("SELECT version FROM schema_migrations")}
        for path in sorted(MIGRATIONS.glob("*.sql")):
            version = path.name.split("_", 1)[0]
            if version in done:
                continue
            with conn.transaction():
                conn.execute(path.read_text())
                conn.execute("INSERT INTO schema_migrations (version) VALUES (%s)", (version,))
            applied.append(path.name)
    return applied


if __name__ == "__main__":
    names = migrate()
    for name in names:
        print("applied", name)
    if not names:
        print("nothing to apply")
