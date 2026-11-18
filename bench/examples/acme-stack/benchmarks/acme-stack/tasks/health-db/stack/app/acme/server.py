"""Acme Shop's HTTP API.

GET  /healthz                  service health
GET  /products/<id>            one product
POST /products/<id>/price      body {"price_cents": n}

python -m acme.server [--port 8080]
"""
import argparse
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from . import catalog, db


def health():
    """Status code and body of GET /healthz."""
    return 200, {"status": "ok"}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/healthz":
            self._send(*health())
            return
        parts = self.path.strip("/").split("/")
        if len(parts) == 2 and parts[0] == "products" and parts[1].isdigit():
            product = catalog.get_product(int(parts[1]))
            self._send(200, product) if product else self._send(404, {"error": "no such product"})
            return
        self._send(404, {"error": "not found"})

    def do_POST(self):
        parts = self.path.strip("/").split("/")
        if len(parts) == 3 and parts[0] == "products" and parts[1].isdigit() and parts[2] == "price":
            length = int(self.headers.get("Content-Length", "0"))
            body = json.loads(self.rfile.read(length) or b"{}")
            ok = catalog.update_price(int(parts[1]), int(body["price_cents"]))
            self._send(200, {"updated": True}) if ok else self._send(404, {"error": "no such product"})
            return
        self._send(404, {"error": "not found"})

    def _send(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, fmt, *args):
        pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    ThreadingHTTPServer(("0.0.0.0", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
