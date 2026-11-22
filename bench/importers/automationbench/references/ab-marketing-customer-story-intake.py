# Submissions: Acme $85k, Nova $140k, Pine $60k ARR. Best = Nova (highest ARR).
post("https://gmail.googleapis.com/gmail/v1/users/me/messages/cs_002/modify", {"removeLabelIds": ["UNREAD"]})
post("https://api.hubapi.com/crm/v3/objects/tickets", {"properties": {"subject": "Case Study Review - Nova", "content": "Case study submission from Nova (ARR $140,000). Contact: zoe@nova.example.com", "hs_pipeline": "0", "hs_pipeline_stage": "1"}})
send_email("zoe@nova.example.com", "Your case study submission - Nova",
    "Hi Zoe,\n\nThank you for submitting Nova for a customer case study. We've selected your story and opened a review to get it started. We'll be in touch shortly to schedule an interview.\n\nBest regards,\nMarketing Team")
