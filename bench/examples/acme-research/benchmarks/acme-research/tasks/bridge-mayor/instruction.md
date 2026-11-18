Who is the current mayor of the town where the Kestrel Footbridge stands?

Research it on the web and follow RESEARCH.md in the workspace. Search with
the `web_search` and `fetch_page` tools of the `websearch` MCP server, or
call the Tavily API yourself at the URL under the key `tavily` in the JSON
in the environment variable `BENCH_EXTERNAL_APIS` (no key needed).

Write two files in the workspace:

- `answer.txt`: the person's full name, on one line.
- `sources.json`: a JSON list of the URLs of the pages that support your
  answer, for example `["https://..."]`.
