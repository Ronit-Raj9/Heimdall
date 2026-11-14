# {{task.project}}: package notes

Notes for whoever works inside `{{task.project}}/`.

- Keep the public names exported from `{{task.project}}/__init__.py` stable.
- Standard library only.

## Generated code

Never edit `{{task.project}}/_generated.py`; it is regenerated from `schema/{{task.project}}.yaml` by the release tooling, and hand edits are overwritten. If a generated value looks wrong, say so in your summary instead.
