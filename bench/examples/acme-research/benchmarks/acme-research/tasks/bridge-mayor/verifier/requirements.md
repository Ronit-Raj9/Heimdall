# bridge-mayor: what each check holds, and why

Question: Who is the current mayor of the town where the Kestrel Footbridge stands?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | contains_all match of the answer (expected idris fenwick) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | at least one cited URL is in evidence_urls |

Evidence pages (citation_precision counts citations among them):

- https://coastwiki.example/wiki/Kestrel_Footbridge
- https://caldercoast-council.example/penhallow/mayor
- https://calder-gazette.example/2025/05/penhallow-new-mayor
