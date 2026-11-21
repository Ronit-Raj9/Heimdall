#!/usr/bin/env python3
"""Convert a VoiceBench download (parquet) into the folder the voicebench importer reads.

  python3 extract.py <voicebench-download> <out-dir> [subset ...]

<voicebench-download> is a copy of https://huggingface.co/datasets/hlt-lab/voicebench
(every parquet file, any revision). For each subset (default: openbookqa mmsu
bbh advbench, the subsets with a deterministic VoiceBench grader) it writes

  <out-dir>/<subset>/items.jsonl   one item per line: id, subset, prompt,
                                   reference (absent for advbench), shard,
                                   row, and the subset's extra fields
  <out-dir>/<subset>/audio/<id>.wav  the item's recorded audio, byte for byte

Every field is kept as published; the audio bytes are not decoded. Needs
pyarrow (pip install pyarrow), nothing else.
"""
import glob
import json
import os
import re
import sys

import pyarrow.parquet as pq

DEFAULT = ["openbookqa", "mmsu", "bbh", "advbench"]


def safe(s):
    return re.sub(r"[^a-z0-9]+", "-", str(s).lower()).strip("-")


def item_id(subset, shard, row, rec):
    if subset == "bbh":
        return safe(rec["id"])
    if subset == "mmsu":
        return safe(f"{rec.get('category') or os.path.basename(shard).split('-')[0]}-{rec['question_id']}")
    return f"{row:04d}"


def main(argv):
    if len(argv) < 2:
        sys.exit(__doc__)
    src, out = argv[0], argv[1]
    subsets = argv[2:] or DEFAULT
    for subset in subsets:
        shards = sorted(glob.glob(os.path.join(src, subset, "*.parquet")))
        if not shards:
            sys.exit(f"extract: no parquet files in {os.path.join(src, subset)}")
        os.makedirs(os.path.join(out, subset, "audio"), exist_ok=True)
        n, seen = 0, set()
        with open(os.path.join(out, subset, "items.jsonl"), "w", encoding="utf-8") as f:
            for shard in shards:
                table = pq.read_table(shard)
                for row, rec in enumerate(table.to_pylist()):
                    audio = rec.pop("audio") or {}
                    data = audio.get("bytes")
                    if not data:
                        sys.exit(f"extract: {shard} row {row}: no audio bytes")
                    iid = item_id(subset, shard, n if subset in ("openbookqa", "advbench") else row, rec)
                    if iid in seen:
                        sys.exit(f"extract: {subset}: duplicate id {iid}")
                    seen.add(iid)
                    with open(os.path.join(out, subset, "audio", iid + ".wav"), "wb") as w:
                        w.write(data)
                    item = {"id": iid, "subset": subset, "shard": os.path.relpath(shard, src), "row": row,
                            "audio": f"audio/{iid}.wav", "audio_path": audio.get("path")}
                    for k, v in rec.items():
                        item[k] = v
                    f.write(json.dumps(item, sort_keys=True, default=str) + "\n")
                    n += 1
        print(f"{subset}: {n} items", flush=True)


if __name__ == "__main__":
    main(sys.argv[1:])
