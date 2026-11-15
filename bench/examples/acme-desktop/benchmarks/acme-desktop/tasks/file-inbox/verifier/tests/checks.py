# Checks for file-inbox, read by verify.py beside it. STATE is the desktop's
# state dump; "paths" lists every file under Documents, "fs" is the tree
# with each file's sha256, so a moved file is recognised by its content.
STATE = "desktop/state.json"

CHECKS = [
    {"name": "invoice_filed", "path": "fs/Invoices/2026/invoice-0042.pdf/sha256", "equals": "268619de8f06bacfcece8dbfa7bb50deeb90718da74faf6930ec19fe7c129f5d"},
    {"name": "photo_filed", "path": "fs/Photos/office-party-2026.jpg/sha256", "equals": "f66d732453b817acec6c7b0ddb2314a3dc5455d2d9a83fe31eed6a5b3e8255a1"},
    {"name": "scan_left_inbox", "path": "paths", "not_contains": "Inbox/scan_0042.pdf"},
    {"name": "photo_left_inbox", "path": "paths", "not_contains": "Inbox/IMG_2031.jpg"},
    {"name": "notes_stay", "path": "fs/Inbox/notes.txt/sha256", "equals": "9ffa7150c34088361c3b50e490f11b0ea7d31edec51eec885c34a28dedfaa8ef"},
    {"name": "old_invoice_kept", "path": "fs/Invoices/2026/invoice-0041.pdf/sha256", "equals": "3c62cb1c6052814785b94976a8256b5f67a0222ffc10d308d78d026e15b36719"},
    {"name": "team_photo_kept", "path": "fs/Photos/team-2025.jpg/sha256", "equals": "678910c5c2b7950bcf0ae36912b6a76da4338340f157d282f525b6edd04cf24b"},
    {"name": "exactly_five_files", "path": "paths", "length": 5},
]
