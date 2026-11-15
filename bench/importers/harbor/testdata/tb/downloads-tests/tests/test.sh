#!/bin/bash
git clone https://github.com/example/original-sources original && cd original && cd ..
bash /tests/compare.sh && echo 1 > /logs/verifier/reward.txt || echo 0 > /logs/verifier/reward.txt
