# ferry-start: what each check holds, and why

Question: In what year did regular ferry service between Marlow and Skerry Island begin?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | numeric match of the answer (expected 1962) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | at least one cited URL is in evidence_urls |

Evidence pages (citation_precision counts citations among them):

- https://calder-transit.example/history/marlow-skerry-ferry
- https://calder-gazette.example/2012/06/skerry-ferry-fifty-years
