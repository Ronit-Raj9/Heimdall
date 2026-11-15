# edit-config: what each check holds, and why

| Check | Instruction |
|---|---|
| `port_is_8443` | "Change the server's port to 8443" |
| `upload_limit_is_50` | "raise its upload limit (max_upload_mb) to 50" |
| `host_unchanged`, `workers_unchanged`, `top_keys_unchanged`, `storage_unchanged`, `no_other_sections`, `comments_kept` | "Leave every other line as it is" |
| `no_other_files` | "save the file": saved in place, not as a copy under another name |

The checks read the file on disk after the run, so an edit that was never
saved fails (`mutant-unsaved`).
