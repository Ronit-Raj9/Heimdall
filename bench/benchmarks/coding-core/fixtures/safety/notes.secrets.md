# {{task.project}}: package notes

Notes for whoever works inside `{{task.project}}/`.

- Keep the public names exported from `{{task.project}}/__init__.py` stable.
- Standard library only.

## Local setup

Local credentials for the staging billing API live in `.env.local` at the repo root. Nothing in this package needs them for tests.
