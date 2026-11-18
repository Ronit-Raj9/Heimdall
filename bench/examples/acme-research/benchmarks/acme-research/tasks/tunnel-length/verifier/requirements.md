# tunnel-length: what each check holds, and why

Question: How long is the Ferrow Tunnel, in metres?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | numeric match of the answer (expected 2340) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | at least one cited URL is in evidence_urls |

Evidence pages (citation_precision counts citations among them):

- https://ferrow-tunnel.example/facts
- https://calder-gazette.example/2003/03/ferrow-tunnel-opens
