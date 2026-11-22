get("https://gmail.googleapis.com/gmail/v1/users/me/messages", q="login")
post("https://{your-domain}.atlassian.net/rest/api/3/issue", {"fields": {"project": {"key": "PROD"}, "issuetype": {"name": "Bug"}, "summary": "Login fails with special characters in password"}})
