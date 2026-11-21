# BALROG image for an imported Heimdall suite. It is three things at once:
# the MCP game service each task starts beside the agent (server.py), the
# verifier that replays the recorded actions and reads upstream's progression
# (verify.py), and the catalog reader the importer runs (catalog.py).
#
# `bifrost-bench import balrog` writes this folder into the suite, with
# upstream/ holding the BALROG package copied from the local clone. The build
# installs that package and its dependencies (NLE, MiniHack, TextWorld,
# Minigrid, Baba Is AI, Crafter) and downloads upstream's game data
# (Boxoban levels, TextWorld games) the way balrog-post-install does; it is
# the only step that uses the network. Runs never do.
FROM python:3.10-slim@sha256:6ff506466f8b1e981719b468cc0023b8ef000b983cdec0eafe119d01f379e31d
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential cmake git bison flex libbz2-dev \
      libglib2.0-0 libxcb1 libgl1 libsm6 libxext6 wget curl unzip ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY upstream /tmp/balrog-src
RUN pip install /tmp/balrog-src gdown && rm -rf /tmp/balrog-src
# Game data, as balrog-post-install fetches it: Boxoban levels into
# minihack's data folder and the TextWorld games into /opt/balrog-data/tw_games.
RUN set -eux; dat=$(python3 -c "import os,minihack;print(os.path.join(os.path.dirname(minihack.__file__),'dat'))"); \
    wget -q -O /tmp/boxoban.zip https://github.com/deepmind/boxoban-levels/archive/refs/heads/master.zip; \
    unzip -q /tmp/boxoban.zip -d "$dat"; rm /tmp/boxoban.zip; \
    mkdir -p /opt/balrog-data; cd /opt/balrog-data; \
    gdown -q 1aeT-45-OBxiHzD9Xn99E5OvC86XmqhzA -O tw-games.zip; unzip -q tw-games.zip; rm tw-games.zip; ls /opt/balrog-data
COPY heimdall /opt/heimdall
RUN chmod -R a+rX,go-w /opt/heimdall /opt/balrog-data \
 && useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out /state \
 && chown agent:agent /workspace /grader/out /state
WORKDIR /workspace
USER agent
EXPOSE 8000
CMD ["python3", "-I", "/opt/heimdall/server.py"]
