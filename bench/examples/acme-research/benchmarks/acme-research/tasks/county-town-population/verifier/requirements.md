# county-town-population: what each check holds, and why

Question: What was the population, at the most recent census, of the county town of the county in which Greyholt Observatory stands?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | numeric match of the answer (expected 48310) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | at least one cited URL is in evidence_urls |

Evidence pages (citation_precision counts citations among them):

- https://greyholt-observatory.example/about
- https://caldercoast-council.example/counties/westmere
- https://calder-stats.example/census/2024/ashcombe
- https://caldercoast-council.example/counties
- https://coastwiki.example/wiki/Tor_Hill
