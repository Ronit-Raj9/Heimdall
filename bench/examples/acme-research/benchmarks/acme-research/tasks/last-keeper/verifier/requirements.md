# last-keeper: what each check holds, and why

Question: Who was the last resident keeper of Gull Point Lighthouse?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | contains_all match of the answer (expected tomas venn) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | at least one cited URL is in evidence_urls |

Evidence pages (citation_precision counts citations among them):

- https://gullpoint-heritage.example/history
- https://calder-gazette.example/1991/10/gull-point-goes-automatic
- https://gullpoint-heritage.example/keepers
