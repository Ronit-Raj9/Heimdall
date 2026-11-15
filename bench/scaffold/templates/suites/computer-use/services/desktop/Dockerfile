# Image of the desktop service: a virtual display (Xvfb), the openbox window
# manager, three GUI apps (the mousepad text editor, the xfe file manager and
# a small Tk form app in desktop.py) and the MCP server the agent drives
# them through. No terminal emulator, browser or launcher is installed. The
# apt step needs the network, so build the image once by hand or let
# `bifrost-bench suite admit` build it (a plain docker build, which has the
# network); runs never get it. About 860 MB.
#
#   docker build -t <tag in the task's services: image> benchmarks/<suite>/services/desktop
#
# The base is pinned by digest (tag kept for reading), as the suite base is.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends xvfb xdotool openbox mousepad xfe libtk8.6 fonts-dejavu-core \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 desk \
 && mkdir -p /tmp/.X11-unix /home/desk/Documents \
 && chmod 1777 /tmp/.X11-unix \
 && chown -R desk:desk /home/desk
COPY desktop.py openbox.xml test_desktop.py /opt/desktop/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8000 DISPLAY=:99 HOME=/home/desk \
    GSETTINGS_BACKEND=memory NO_AT_BRIDGE=1 LANG=C.UTF-8
USER 1000
WORKDIR /home/desk
EXPOSE 8000
CMD ["python3", "-I", "/opt/desktop/desktop.py"]
