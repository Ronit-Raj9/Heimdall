# festival-date: what each check holds, and why

Question: On what date was the first Lantern Tide festival held in Skerry Harbour?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | regex match of the answer (expected \b1987-08-14\b, \b14(st|nd|rd|th)? august,? 1987\b, \baugust 14(st|nd|rd|th)?,? 1987\b) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | at least one cited URL is in evidence_urls |

Evidence pages (citation_precision counts citations among them):

- https://calder-gazette.example/2017/08/lantern-tide-at-thirty
