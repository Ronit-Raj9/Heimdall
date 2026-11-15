# Checks for edit-config, read by verify.py beside it. STATE is the desktop's
# state dump ("<service id>/<export id>" from task.yaml); desktop.py dump
# parses .conf files into "ini" (section, key, value as text).
STATE = "desktop/state.json"

CHECKS = [
    {"name": "port_is_8443", "path": "fs/settings.conf/ini/server/port", "equals": "8443"},
    {"name": "upload_limit_is_50", "path": "fs/settings.conf/ini/server/max_upload_mb", "equals": "50"},
    {"name": "host_unchanged", "path": "fs/settings.conf/ini/server/host", "equals": "0.0.0.0"},
    {"name": "workers_unchanged", "path": "fs/settings.conf/ini/server/workers", "equals": "4"},
    {"name": "top_keys_unchanged", "path": "fs/settings.conf/ini/top", "equals": {"name": "notes-sync", "environment": "staging"}},
    {"name": "storage_unchanged", "path": "fs/settings.conf/ini/storage", "equals": {"path": "/srv/notes", "retention_days": "30"}},
    {"name": "no_other_sections", "path": "fs/settings.conf/ini", "length": 3},
    {"name": "comments_kept", "path": "fs/settings.conf/text", "contains": "# The service reads this file when it starts."},
    {"name": "no_other_files", "path": "paths", "equals": ["settings.conf"]},
]
