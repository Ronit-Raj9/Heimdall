#!/usr/bin/env python3
"""Turn the AgentVista parquet shards into what `bifrost-bench import agentvista` reads.

Usage: python3 -I extract.py <snapshot dir with data/*.parquet> <out dir>

Writes <out>/tasks.jsonl (one row per question, in shard and row order) and
<out>/images/<id>-<n>.<ext>. The id is av-NNN, the 0-based position of the
row across the sorted shards, so it is stable for one dataset revision. Needs
pyarrow. Image bytes are written unchanged; the extension comes from the
stored path when it has one, else from the file's magic bytes.
"""
import glob
import json
import os
import sys

import pyarrow.parquet as pq

MAGIC = [(b"\x89PNG", ".png"), (b"\xff\xd8", ".jpg"), (b"GIF8", ".gif"), (b"RIFF", ".webp"), (b"BM", ".bmp")]


def ext_of(path, data):
    e = os.path.splitext(path or "")[1].lower()
    if e in (".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"):
        return ".jpg" if e == ".jpeg" else e
    for m, x in MAGIC:
        if data.startswith(m):
            return x
    return ".bin"


def main():
    src, out = sys.argv[1], sys.argv[2]
    shards = sorted(glob.glob(os.path.join(src, "data", "*.parquet")))
    if not shards:
        sys.exit("no data/*.parquet under " + src)
    os.makedirs(os.path.join(out, "images"), exist_ok=True)
    n = 0
    with open(os.path.join(out, "tasks.jsonl"), "w", encoding="utf-8") as fh:
        for shard in shards:
            for row in pq.read_table(shard).to_pylist():
                qid = "av-%03d" % n
                names = []
                for i, img in enumerate(row.get("images") or []):
                    data = img.get("bytes") or b""
                    if not data:
                        sys.exit("%s: image %d has no bytes" % (qid, i))
                    name = "%s-%d%s" % (qid, i + 1, ext_of(img.get("path"), data))
                    with open(os.path.join(out, "images", name), "wb") as f:
                        f.write(data)
                    names.append(name)
                rec = {"id": qid, "problem": row.get("problem") or "", "answer": row.get("answer") or "",
                       "domain": row.get("domain") or "", "subdomain": row.get("subdomain") or "", "images": names,
                       "shard": os.path.basename(shard)}
                fh.write(json.dumps(rec, ensure_ascii=False) + "\n")
                n += 1
    print("wrote %d questions to %s" % (n, out))


if __name__ == "__main__":
    main()
