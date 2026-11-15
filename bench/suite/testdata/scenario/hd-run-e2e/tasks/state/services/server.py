import http.server

STATE = "/tmp/state.json"


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        with open(STATE, "wb") as f:
            f.write(body)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"stored")

    def log_message(self, *args):
        pass


http.server.ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
