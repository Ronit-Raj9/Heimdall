Plan tomorrow's (2026-11-16) morning drone deliveries for our three depots:
Riverside, Hilltop and Quayside.

Use the weather API: its URL is under the key `weather` in the JSON in the
environment variable `BENCH_EXTERNAL_APIS`, and `API.md` in the workspace
describes it. Apply the flight rule in `DISPATCH.md` to the 09:00 forecast of
each depot. Do not guess the weather.

Write `plan.json` in the workspace:

```json
{"date": "2026-11-16", "fly": ["<site id>", ...], "hold": ["<site id>", ...]}
```

Use the site ids the API uses.
