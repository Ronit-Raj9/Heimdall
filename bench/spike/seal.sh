#!/usr/bin/env bash
# seal.sh seal DIR    hash every file under DIR into DIR/MANIFEST.sha256 (the manifest never lists
#                     itself); prints the manifest's own sha256 as the seal hash.
# seal.sh verify DIR  re-hash; fails on any changed, missing or extra file.
set -euo pipefail
cmd=$1 dir=$2
manifest=MANIFEST.sha256
list() { (cd "$dir" && find . -type f ! -path "./$manifest" -print0 | LC_ALL=C sort -z | xargs -0 -r sha256sum); }
case $cmd in
  seal)
    tmp=$(mktemp)
    list > "$tmp"
    mv "$tmp" "$dir/$manifest"
    echo "sealed $(wc -l < "$dir/$manifest") files seal=$(sha256sum "$dir/$manifest" | cut -d' ' -f1)"
    ;;
  verify)
    [ -f "$dir/$manifest" ] || { echo "verify FAIL: no manifest"; exit 1; }
    if ! diff <(awk '{print $2}' "$dir/$manifest") <(list | awk '{print $2}') > /dev/null; then
      echo "verify FAIL: file set differs"; diff <(awk '{print $2}' "$dir/$manifest") <(list | awk '{print $2}') || true; exit 1
    fi
    if (cd "$dir" && sha256sum --quiet -c "$manifest"); then
      echo "verify OK $(wc -l < "$dir/$manifest") files seal=$(sha256sum "$dir/$manifest" | cut -d' ' -f1)"
    else
      echo "verify FAIL: hash mismatch"; exit 1
    fi
    ;;
  *) echo "usage: seal.sh seal|verify DIR" >&2; exit 2 ;;
esac
