# gatekeeper

Small, dependency-free building blocks for request admission.

- `SlidingWindowLimiter`: at most `limit` requests per key in any `window` seconds, with an injectable clock.
- `Gate`: applies the `limiter` section of a configuration (on/off switch, exempt keys).
- `load_config`: defaults, an optional TOML file and environment variables.
- `find_duplicates` / `drop_duplicates`: duplicate records by normalised key fields.

```
python -m gatekeeper config --file gatekeeper.toml
python -m gatekeeper simulate --limit 2 --window 10 alice@0 alice@1 alice@2
python -m gatekeeper dedupe users.jsonl --key email --key phone
```

Requires Python 3.12. Tests: `python -m pytest -q`.
