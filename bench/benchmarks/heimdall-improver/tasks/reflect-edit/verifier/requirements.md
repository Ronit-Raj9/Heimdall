# reflect-edit: what each check holds, and why

The instruction asks for `/workspace/edits.json`, an edit set within
`/heimdall/improve/limits.json`. `tests/verify.py` checks its form only:

| Check | Source |
|---|---|
| `form` | "Write /workspace/edits.json" and "Stay within /heimdall/improve/limits.json": valid JSON of the edits schema, at most 8 edits, known operations, absolute file and skill paths (or `@instructions`) outside system folders, rule text at most 120 words and skill text at most 600 words |

Whether every cited run exists, and the other rules of an edit set, are
checked when Heimdall applies the edits, because the verifier never sees
the evidence.
