# health-db: what each check holds, and why

`tests/verify.py` starts the app's server from the clean-room workspace
twice: once against the stack's database, once against a port on the
database host that refuses connections.

| Check | Source |
|---|---|
| database up: `200` with `"database": "up"` | "`200 {"status": "ok", "database": "up"}` when a query works" |
| database down: `503` with `"database": "down"` | "`503 {"status": "degraded", "database": "down"}` when it does not" |
| each answer within 7 seconds | "answering within a few seconds either way" |
