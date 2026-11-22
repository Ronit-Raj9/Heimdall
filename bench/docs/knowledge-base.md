# Knowledge-base (RAG) benchmarks

A help-center bot answers from your documents. The knowledge-base template
tests that: the documents are not in the agent's workspace but behind a
search service, and the answer is matched against a hidden expected answer
with no model.

```console
$ bifrost-bench suite init -id help-center -template knowledge-base
```

or Benchmarks > New, template "knowledge-base".

## The search service

`services/kb/` is an MCP server (Python standard library only) with two
tools:

- `search_docs {query, k?}`: BM25 over the documents' passages; returns the
  best documents (at most 20) with their best passage;
- `read_doc {doc_id}`: one document in full.

It serves every Markdown, text and HTML file under `services/kb/docs/` (HTML
is read as text: scripts and styles dropped). Convert PDFs to text first, for
example with `pdftotext`. The documents are baked into the service image, and
the folder is part of the benchmark's lock, so changing a document changes the
benchmark version and runs on different documents are never compared as the
same. The corpus sha256 is on the service's `/healthz` and in its MCP server
info; `python3 services/kb/server.py index` with `DOCS_DIR` set lists the
documents.

Add documents by copying them into `services/kb/docs/` or uploading them on
the benchmark's Edit tab, then run the readiness checks so the image is
rebuilt.

## Tasks

The sample task `refund-window` asks one question; its
`verifier/answer.yaml` holds the expected answer and plausible wrong ones
(answer_match: exact, contains_all, regex, numeric, choice). Every search and
read the agent makes is exported as `kb/reads.json` (`searches` with their
result ids, `reads`, `corpus_sha256`), so a reviewer sees which documents an
answer came from. Add tasks with `bifrost-bench task init -suite help-center
-id <task>`, or from a spreadsheet on the Build tab.
