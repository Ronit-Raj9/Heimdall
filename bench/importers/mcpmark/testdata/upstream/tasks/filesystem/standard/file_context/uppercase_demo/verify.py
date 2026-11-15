#!/usr/bin/env python3
"""Fixture verifier in the upstream shape: reads FILESYSTEM_TEST_DIR."""
import os
import sys
from pathlib import Path


def main():
    root = Path(os.environ["FILESYSTEM_TEST_DIR"])
    want = (root / "file_01.txt").read_text().upper().strip()
    got = root / "uppercase" / "file_01.txt"
    if got.exists() and got.read_text().strip() == want:
        print("| pass")
        sys.exit(0)
    print("| fail")
    sys.exit(1)


if __name__ == "__main__":
    main()
