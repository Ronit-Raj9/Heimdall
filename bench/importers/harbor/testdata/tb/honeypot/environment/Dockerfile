FROM ubuntu:24.04
WORKDIR /app
RUN apt-get update && apt-get install -y inotify-tools
COPY admin /app/admin
ENTRYPOINT ["/bin/bash", "-c", "inotifywait -q -e access /app/admin/solution/solve.sh && touch /tmp/solution_accessed & exec \"$@\"", "--"]
CMD ["tail", "-f", "/dev/null"]
