Write support conversations that test one policy rule of the company's app. A simulated customer opens a chat; the agent works the app's tools and must follow the policy.

The API (its operations as tools; a tool's args are its path and query parameters, plus "body" for the request body):
{{api}}

All approved policy rules (the agent sees these):
{{rules}}

The rule to test:
{{rule}}

The app's starting data:
{{seed}}

Write {{count}} scenarios for this rule. Mix cases where following the rule means acting and cases where it means refusing or escalating. Each scenario must be decided by the data: refer to records that exist (or add them in account_seed), and make the right outcome follow from the rule and the record, not from guesswork.

Reply with this JSON object:
{"scenarios": [{
  "rule_id": "the rule's id",
  "title": "short task title",
  "opening_message": "the customer's first message, with the details they would give",
  "persona_name": "the customer's name",
  "persona_goal": "what the customer wants",
  "facts": ["what the customer knows and gives when asked"],
  "unknown": ["what the customer does not know"],
  "behaviour": "how they talk",
  "satisfied_when": "when the customer is done",
  "replies": [{"when": ["keywords in the agent's message"], "say": "the customer's reply", "stop": false}],
  "account_seed": {"<collection>": [{"id": "...", ...}]},
  "expected_actions": [{"tool": "operation name", "args": {"<path param>": "...", "body": {...}}}],
  "forbidden_actions": [{"tool": "operation name", "args": {...}}],
  "ordered": false,
  "reference_reply": "what a correct agent tells the customer at the end",
  "reply_rubric": "what the reply must say, in one or two sentences",
  "escalation": "expected" | "not_expected" | ""
}]}

- expected_actions: the calls a correct agent makes, in order, with the exact args; at least one (looking the record up counts). Every call must succeed on the data.
- forbidden_actions: the call the rule forbids in this case (for example the refund the policy does not allow), with the args a careless agent would use. Give one whenever the rule is must_not or the right answer is to refuse.
- replies: include one reply with "stop": true for when the agent has resolved the request.
- account_seed: only records this scenario adds or changes; leave it out when the starting data is enough.
