Write the starting data of a copy of the company's app, so support tasks can be tested against it.

The API (its operations as tools; the mock keeps one collection per resource):
{{api}}

The approved policy rules:
{{rules}}

Collections to fill: {{collections}}

Write realistic records for each collection: enough variety that every rule can be tested both ways (cases the policy allows and cases it forbids: amounts over and under limits, dates inside and outside windows, statuses that allow and block an action). Every record needs a unique "id" (a short string such as "ORD-1001"), every field the create operation's body schema requires, and values of the schema's types and enums. Records that refer to each other must use ids that exist. Dates are ISO 8601; assume today is 2026-11-20.

Reply with this JSON object:
{"collections": {"<collection>": [{"id": "...", ...}]}}

At most 25 records per collection and 200 in all.
