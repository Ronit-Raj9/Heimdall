Read the company's API and its policy document, and write the policy as a numbered list of rules a support agent working that API must follow.

The API (its operations as tools):
{{api}}

The policy document:
{{policy}}

Write each rule so a test can check it from the API's calls and data: one rule per obligation, in the policy's own terms (amounts, days, statuses, who may do what). Leave out rules about tone or wording unless the policy makes them a hard requirement. Do not invent rules the policy does not state.

Reply with this JSON object:
{"rules": [{"id": "R1", "text": "...", "kind": "must" | "must_not" | "escalate" | "info", "operations": ["operation names the rule is about"]}]}

- id: R1, R2, ... in order.
- kind: must (the agent must do something), must_not (the agent must never do something), escalate (the agent must hand the customer to a person), info (a fact the agent must tell or use).
- operations: names from the API above only; an empty list when no operation applies.
- At most 40 rules, each at most 600 characters.
