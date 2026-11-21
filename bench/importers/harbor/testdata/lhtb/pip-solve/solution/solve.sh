#!/bin/bash
set -euo pipefail
cd /app
python3 -m pip install -e . pytest==8.4.1
cat /solution/greeting.txt > /app/hello.txt
