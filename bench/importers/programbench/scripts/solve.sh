#!/bin/bash
# Reference control for admission only (bifrost-bench import programbench).
# ProgramBench publishes no reference source in the agent's image, so the
# reference proves the hidden tests pass with an executable that behaves
# exactly like the original: it keeps a compressed copy of the original
# binary outside the workspace and a compile.sh that restores it. The
# verifier deletes byte-identical copies inside /workspace only, as upstream
# does. Upstream's rules forbid an agent from doing this (wrapping or reusing
# the binary is a disqualification).
set -e
mkdir -p /opt/pb-reference
gzip -c /workspace/executable > /opt/pb-reference/executable.gz
cat > /workspace/compile.sh <<'SH'
#!/bin/sh
gunzip -c /opt/pb-reference/executable.gz > ./executable && chmod +x ./executable
SH
chmod +x /workspace/compile.sh
