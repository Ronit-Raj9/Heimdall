# aquarium-opening-day: what each check holds, and why

Question: How many visitors attended the opening day of Brightwater Aquarium?

| Check | Holds when |
|---|---|
| `answer_present` | answer.txt is not empty |
| `answer_correct` | exact match of the answer (abstention: says the answer is not in the sources) |
| `sources_readable` | sources.json, when written, is a list of URLs |
| `no_invented_sources` | every cited URL is a page of the corpus (corpus_urls.json) |
| `evidence_cited` | not required: an abstention cites what it checked |

Evidence pages (citation_precision counts citations among them):

- none: the corpus does not hold the answer
