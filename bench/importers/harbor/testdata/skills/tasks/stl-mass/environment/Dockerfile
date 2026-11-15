FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y python3 python3-pip
WORKDIR /root
COPY scan.stl /root/
# Copy skills to ALL agent paths
COPY skills /root/.claude/skills
COPY skills /root/.codex/skills
