#!/usr/bin/env python3
"""Convert BrowseComp-Plus parquet shards to JSONL, unchanged.

The importer (bifrost-bench import browsecomp-plus) reads JSONL; the dataset
on Hugging Face (Tevatron/browsecomp-plus, data/test-0000N-of-00006.parquet)
is parquet. This writes every row of the given shards as one JSON line, in
order, with every field as upstream has it: the obfuscated fields stay
obfuscated, so no answer is written in plain text. It needs pyarrow
(pip install pyarrow) and streams rows, so a 500 MB shard does not have to
fit in memory.

  python3 tojsonl.py data/test-00003-of-00006.parquet > queries.jsonl
"""

import json
import sys

try:
    import pyarrow.parquet as pq
except ImportError:
    sys.exit("tojsonl.py needs pyarrow: pip install pyarrow")


def main(paths):
    if not paths:
        sys.exit(__doc__)
    out = sys.stdout
    for p in paths:
        for batch in pq.ParquetFile(p).iter_batches(batch_size=4):
            for row in batch.to_pylist():
                out.write(json.dumps(row, ensure_ascii=False))
                out.write("\n")


if __name__ == "__main__":
    main(sys.argv[1:])
