# GAIA fixture (invented)

A hand-made stand-in for a download of GAIA's 2023 validation split, in the
documented shape (metadata.jsonl rows with task_id, Question, Level, "Final
answer", file_name, file_path and "Annotator Metadata"; the attachment next
to metadata.jsonl). Every question, answer and file here is invented; none
is a GAIA question. GAIA itself is gated and is not in this repository.

Question 5 is about the fictional Calder Coast of bench/research/standin. Its
cassette (cassettes/<task_id>/tavily.cassette.jsonl) was recorded from the
Tavily stand-in through the real tool proxy code in record mode, from
queries.json beside it, with:

    go run ./research/cmd/tavily-record -suite importers/gaia/testdata/fixture \
      -queries importers/gaia/testdata/fixture/cassettes/00000000-0000-4000-8000-000000000005/queries.json \
      -out importers/gaia/testdata/fixture/cassettes/00000000-0000-4000-8000-000000000005/tavily.cassette.jsonl

A test re-records it and fails when it is stale.
