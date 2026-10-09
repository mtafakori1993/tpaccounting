#!/usr/bin/env python3
"""Fault-injecting HTTP proxy for live reliability tests (test stack only).

Sits between the AI service and the accounting app:
  AI_ACCOUNTING_URL_HOST=http://127.0.0.1:18086  →  fault_proxy  →  http://127.0.0.1:18085
Arm a fault (applies once, to the next matching request):
  curl '127.0.0.1:18086/__fault?mode=lost&match=POST /api/v1/payroll/components'
modes: lost  = forward (the app commits), then answer 502 (response lost)
       down  = answer 502 without forwarding (nothing committed)
       slow  = forward after 20 s (client timeout)
       e500  = answer 500 with an error body, without forwarding
       fake200 = answer 200 with an invented record WITHOUT forwarding (the app "says"
                 it saved something it did not): verification must catch it
"""
import http.server, socketserver, urllib.request, urllib.parse, sys, time, threading, json

UP = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18085"
PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 18086
FAULT = {}
LOG = []
lock = threading.Lock()


class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _send(self, code, body, headers=()):
        self.send_response(code)
        for k, v in headers:
            if k.lower() not in ("transfer-encoding", "connection", "content-length"):
                self.send_header(k, v)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def handle_any(self):
        u = urllib.parse.urlparse(self.path)
        if u.path == "/__fault":
            q = dict(urllib.parse.parse_qsl(u.query))
            with lock:
                FAULT.clear()
                FAULT.update(q)
            return self._send(200, json.dumps({"armed": q}).encode())
        if u.path == "/__log":
            with lock:
                return self._send(200, json.dumps(LOG[-50:]).encode())
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else None
        key = f"{self.command} {u.path}"
        with lock:
            mode = FAULT.get("mode") if FAULT.get("match") == key else None
            if mode:
                FAULT.clear()
            LOG.append({"req": key, "fault": mode, "at": time.time()})
        if mode in ("down", "e500"):
            code = 502 if mode == "down" else 500
            return self._send(code, json.dumps({"error": {"code": "injected", "message": "injected " + mode}}).encode(), [("Content-Type", "application/json")])
        if mode == "fake200":
            sent = json.loads(body or b"{}") if body else {}
            sent.update({"id": 987654321, "number": 987654321, "status": "posted"})
            return self._send(200, json.dumps(sent).encode(), [("Content-Type", "application/json")])
        if mode == "slow":
            time.sleep(20)
        req = urllib.request.Request(UP + self.path, data=body, method=self.command)
        for k, v in self.headers.items():
            if k.lower() not in ("host", "content-length", "connection"):
                req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                data, code, hdrs = r.read(), r.status, r.getheaders()
        except urllib.error.HTTPError as e:
            data, code, hdrs = e.read(), e.code, e.headers.items()
        if mode == "lost":
            return self._send(502, b'{"error":{"code":"bad_gateway","message":"injected lost response"}}', [("Content-Type", "application/json")])
        self._send(code, data, hdrs)

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = handle_any


class S(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True


if __name__ == "__main__":
    S(("127.0.0.1", PORT), H).serve_forever()
