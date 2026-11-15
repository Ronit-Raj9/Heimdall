# Adapter fault hooks

The `adapter` probe layer exercises an agent's declared runtime hooks for
`tool`, `memory`, `browser`, and `retry` operations. An agent config must set
`capabilities.fault_protocol_version: "1"` and list each exact hook and its
allowed modes (`error`, `timeout`, or `replace`). The planner rejects a missing
capability, an unsupported mode, and `remote_http` before the agent starts.

During a run the bench owns a scoped controller endpoint and token. The Python
and TypeScript clients register a session, ask for a decision before each
operation, acknowledge an applied decision, and finish the session. Error and
timeout decisions bypass the callback; replacement returns synthetic JSON.
Control failures fail closed. The controller writes a hash chained
`adapter.fault.jsonl` ledger and a sealed `adapter.fault.json` summary. Callback
acknowledgements are cooperative evidence; the controller decision is the
server-owned observation. An unfinished session makes the evidence incomplete
and the run an infrastructure failure.
