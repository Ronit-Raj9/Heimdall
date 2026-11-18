`GET /healthz` always says `{"status": "ok"}`, even when the app
cannot reach its database, so the load balancer keeps sending
traffic to broken instances. Make it report the database:
`200 {"status": "ok", "database": "up"}` when a query works, and
`503 {"status": "degraded", "database": "down"}` when it does not,
answering within a few seconds either way.
