#!/usr/bin/env python3
"""
fake-http: answer every request with one status code and one body. A test
double for the observer API in deploy/test/selftest.sh.

  fake-http.py --port P --code 503 --body file.json
"""
import argparse
import http.server
import sys


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, required=True)
    ap.add_argument("--code", type=int, default=200)
    ap.add_argument("--body", default=None)
    a = ap.parse_args()
    body = open(a.body, "rb").read() if a.body else b"{}"

    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(a.code)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *args):
            pass

    http.server.ThreadingHTTPServer(("127.0.0.1", a.port), H).serve_forever()


if __name__ == "__main__":
    sys.exit(main())
