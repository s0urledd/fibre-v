#!/usr/bin/env python3
"""
fake-rpc: enough of a CometBFT RPC for deploy/test/rpc-check.sh to run
against, with the knobs the check's decisions depend on. A test double.

  fake-rpc.py --port P [--chain mocha-5] [--app-version 9] [--fibre-code 6]
              [--tip 100000] [--earliest 1] [--no-block-results] [--hash-salt x]
              [--catching-up] [--block-age-s 5]
"""
import argparse
import hashlib
import http.server
import json
import sys
import time
import urllib.parse


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, required=True)
    ap.add_argument("--chain", default="mocha-5")
    ap.add_argument("--app-version", default="9")
    ap.add_argument("--fibre-code", type=int, default=6)
    ap.add_argument("--tip", type=int, default=100000)
    ap.add_argument("--earliest", type=int, default=1)
    ap.add_argument("--no-block-results", action="store_true")
    ap.add_argument("--hash-salt", default="")
    ap.add_argument("--catching-up", action="store_true")
    ap.add_argument("--block-age-s", type=int, default=5)
    a = ap.parse_args()

    def block_hash(h):
        return hashlib.sha256(f"{a.chain}:{h}:{a.hash_salt}".encode()).hexdigest().upper()

    def rpc_error(code, data):
        return {"jsonrpc": "2.0", "id": -1, "error": {"code": code, "message": "Internal error", "data": data}}

    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            u = urllib.parse.urlparse(self.path)
            q = urllib.parse.parse_qs(u.query)
            h = int(q.get("height", [a.tip])[0])
            path = u.path
            if path == "/status":
                t = time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(time.time() - a.block_age_s)) + ".000000000Z"
                body = {"result": {"node_info": {"network": a.chain, "moniker": "fake", "version": "0.38.17"},
                                   "sync_info": {"latest_block_height": str(a.tip), "latest_block_time": t,
                                                 "earliest_block_height": str(a.earliest), "catching_up": a.catching_up}}}
            elif path == "/abci_info":
                body = {"result": {"response": {"app_version": a.app_version, "last_block_height": str(a.tip)}}}
            elif path == "/block":
                body = rpc_error(-32603, f"height {h} is not available, lowest height is {a.earliest}") if h < a.earliest else \
                    {"result": {"block_id": {"hash": block_hash(h)}, "block": {"header": {"height": str(h)}}}}
            elif path == "/block_results":
                if a.no_block_results:
                    body = rpc_error(-32603, "node is not persisting finalize block responses")
                elif h < a.earliest:
                    body = rpc_error(-32603, f"could not find results for height #{h}")
                else:
                    body = {"result": {"height": str(h), "txs_results": None, "finalize_block_events": []}}
            elif path == "/validators":
                body = {"result": {"block_height": str(h), "validators": [{"address": "A" * 40}] * 79, "count": "79", "total": "79"}}
            elif path == "/abci_query":
                p = q.get("path", [""])[0].strip('"')
                if p.startswith("/celestia.fibre."):
                    body = {"result": {"response": {"code": a.fibre_code, "log": "unknown query path: unknown request" if a.fibre_code == 6 else "", "value": ""}}}
                else:
                    body = {"result": {"response": {"code": 0, "log": "", "value": "CgA="}}}
            else:
                body = rpc_error(-32601, "method not found")
            data = json.dumps(body).encode()
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *args):
            pass

    http.server.ThreadingHTTPServer(("127.0.0.1", a.port), H).serve_forever()


if __name__ == "__main__":
    sys.exit(main())
