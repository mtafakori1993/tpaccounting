#!/usr/bin/env python3
"""Verifies that assistant answers STREAM end to end (browser ← core /ai-api proxy ←
AI service ← HTTP proxy :8118 ← OpenRouter) without buffering: records the arrival
time of every `delta` event of a chat answer. Buffering anywhere would deliver all
deltas at once at the end."""
import json, sys, time, urllib.request, http.cookiejar
BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18085").rstrip("/")
jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
r = urllib.request.Request(BASE + "/api/v1/auth/login", data=json.dumps({"email": "admin@demo.test", "password": "Demo-Passw0rd!"}).encode(), method="POST")
r.add_header("Content-Type", "application/json")
op.open(r).read()
csrf = next(c.value for c in jar if c.name == "tp_csrf")
req = urllib.request.Request(BASE + "/ai-api/tasks", data=json.dumps({"message": "Explain in about 120 words how receipts, payments and expenses are recorded in this app."}).encode(), method="POST")
req.add_header("Content-Type", "application/json"); req.add_header("X-CSRF-Token", csrf)
t0 = time.time(); times = []; ev = None
with op.open(req, timeout=180) as resp:
    for line in resp:
        line = line.decode().strip()
        if line.startswith("event:"): ev = line[6:].strip()
        elif line.startswith("data:") and ev == "delta": times.append(round(time.time() - t0, 3))
if len(times) < 3:
    print(f"FAIL: only {len(times)} delta events"); sys.exit(1)
spread = times[-1] - times[0]
print(f"deltas={len(times)} first={times[0]}s last={times[-1]}s spread={spread:.2f}s")
print("PASS: streamed incrementally (not buffered)" if spread > 0.5 else "FAIL: all deltas arrived together (buffered)")
sys.exit(0 if spread > 0.5 else 1)
