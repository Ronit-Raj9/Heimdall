# Runtime image for the bench binary: the fault proxy service in
# deploy/docker-compose.yml runs it as `bifrost-bench proxy`. The binary is
# built on the host, static, so the image needs no Go toolchain:
#
#   CGO_ENABLED=0 GOWORK=off go build -o bin/bifrost-bench ./cmd/bifrost-bench
#   docker build -t bench/bifrost-bench:dev .
#
# The base is pinned by digest (tag kept for reading) to the image the bench
# was tested with.
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc

# Non-root user. /var/lib/bench/ledger is created here, owned by that user,
# so a named volume mounted on it starts writable.
RUN addgroup -S -g 10001 bench \
    && adduser -S -D -H -u 10001 -G bench bench \
    && mkdir -p /var/lib/bench/ledger /etc/bench \
    && chown -R bench:bench /var/lib/bench

COPY bin/bifrost-bench /usr/local/bin/bifrost-bench

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/bifrost-bench"]
