"""Adaptador HTTP local para o motor GoRules ZEN."""
import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import zen

graph = Path(__file__).with_name("risk.json").read_bytes()
engine = zen.ZenEngine()
decision = engine.create_decision(json.loads(graph))


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path != "/evaluate":
            return self.send_error(404)
        try:
            facts = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            result = decision.evaluate(facts)["result"]
            payload = json.dumps(dict(result, version="risco-1")).encode()
        except Exception:
            return self.send_error(422)
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    ThreadingHTTPServer((os.getenv("ZEN_BIND", "127.0.0.1"), 8182), Handler).serve_forever()

