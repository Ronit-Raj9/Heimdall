# Import an agent by its platform id

If your agent already lives on an agent platform, you do not have to describe its endpoint by hand. Give Heimdall the platform, the agent's id there and the org secrets that unlock the platform's API. Heimdall reads the agent's definition through that API and writes `agents/<name>.yaml` with the right endpoint preset, plus `agents/imported/<name>.json` with what it read.

Web: **Agents > New agent > Import from a platform**. API: `GET /api/v1/agents/platforms` lists the platforms with the inputs and secrets each needs; `POST /api/v1/agents/import-platform` imports (`dry_run: true` reads the platform and returns the config without saving it).

```json
{"id": "support-line", "platform": "vapi", "agent_id": "5f1c0a9e-2b7d-4c1e-9a43-0d6c7e2f9b11",
 "secrets": {"api_key": "VAPI_API_KEY"}, "dry_run": true}
```

`secrets` maps each secret role of the platform to the name of an org secret (Settings > Secrets). Secret values are read on the server, used for the platform calls and never written to the config, the response or an error message.

| Platform | Id | Secrets | Reads | Agent config |
|---|---|---|---|---|
| Vapi (`vapi`) | assistant id | private key | name, system prompt, first message, tools (inline and saved), model, voice, transcriber language | `voice_platform` |
| Retell (`retell`) | chat agent id (a voice agent id works, with a warning) | API key | name, Retell LLM prompt, begin message, tools, model, voice, language | `voice_platform` |
| ElevenLabs Agents (`elevenlabs`) | agent id | API key | name, prompt, first message, tools (inline, built in, saved), LLM, voice and its settings, language | `voice_platform` |
| AWS Bedrock AgentCore (`agentcore`) | runtime ARN or id (+ region) | access key id, secret key, optional session token | name, description, protocol, status, version (GetAgentRuntime, SigV4) | `remote_http`, preset `agentcore`: InvokeAgentRuntime signed with SigV4, session header |
| Salesforce Agentforce (`agentforce`) | BotDefinition id (`0Xx...`) + My Domain host | connected app client id and secret | name, developer name, description | `remote_http`, preset `agentforce`: OAuth client credentials, session, messages, end |
| Microsoft Copilot Studio (`copilot_studio`) | bot id or schema name | Direct Line secret; optional Entra app with the Dataverse environment and tenant | Direct Line secret check; with Dataverse: name, schema name, language | `remote_http`, preset `copilot_studio`: Direct Line conversation and polling |
| Google Vertex AI Agent Engine (`vertex`) | reasoning engine id or resource name | service account JSON key | display name, description, framework, methods | `remote_http`, preset `vertex`: `:query`, or `:streamQuery` for ADK agents |
| LangGraph Platform (`langgraph`) | assistant id or graph id + deployment URL | optional LangSmith key | name, graph id, description, input fields | `remote_http`, preset `langgraph`: `/runs/wait` |

What is read becomes the agent's capability hints (voice, tools, sessions) and, for voice agents, the prompt, first message and voice the conversation driver can use. Import again to refresh it.

Notes:

- An AgentCore runtime that serves MCP is refused (it is a tool server, not an agent). One with a custom JWT authorizer gets a warning: it wants a bearer token, not SigV4.
- AgentCore passes your entrypoint's own JSON through: Heimdall sends `{"prompt": ...}`. Check the request and answer path with **Test my endpoint**.
- Vertex AI needs an OAuth access token, so the config uses auth type `gcp_access_token`; the server's remote auth must support that type before the agent can be saved.
- On a hosted server every address you type (a LangGraph deployment, a Dataverse environment) must be public, and platform calls never follow redirects.
