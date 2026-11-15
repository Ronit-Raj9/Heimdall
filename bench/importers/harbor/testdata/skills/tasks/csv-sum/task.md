---
schema_version: '1.3'
metadata:
  author_name: Fixture Author
  difficulty: medium
  category: data-processing
  tags:
  - csv
verifier:
  type: test-script
  timeout_sec: 300.0
  service: main
  env:
    SUM_PORT: '8090'
  hardening:
    cleanup_conftests: true
agent:
  timeout_sec: 600.0
oracle:
  env:
    WORKSPACE: /app
sandbox:
  network_mode: public
  cpus: 1
  gpus: 0
  workdir: /app
---

Sum the `amount` column of /app/data.csv and write the total to /app/total.txt.
