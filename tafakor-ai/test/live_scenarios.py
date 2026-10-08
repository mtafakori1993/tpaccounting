#!/usr/bin/env python3
"""Live end-to-end scenarios against the THROWAWAY test stack (demo company only).

Talks to the accounting app exactly like the widget: same-origin /ai-api calls with
the user's session cookie and CSRF token. Uses the real model through the proxy.
Every assertion checks state read back from the accounting API.

Usage: live_scenarios.py [base_url]
"""
import json
import mimetypes
import os
import sys
import uuid
import urllib.request
import urllib.error
import http.cookiejar

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18085").rstrip("/")
HERE = os.path.dirname(os.path.abspath(__file__))
RESULTS = []


class Client:
    def __init__(self, email, password):
        self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.req("POST", "/api/v1/auth/login", {"email": email, "password": password})

    def csrf(self):
        return next((c.value for c in self.jar if c.name == "tp_csrf"), "")

    def req(self, method, path, body=None, raw_body=None, ctype=None):
        data = raw_body if raw_body is not None else (json.dumps(body).encode() if body is not None else None)
        r = urllib.request.Request(BASE + path, data=data, method=method)
        if data is not None:
            r.add_header("Content-Type", ctype or "application/json")
        if method != "GET":
            r.add_header("X-CSRF-Token", self.csrf())
        try:
            with self.op.open(r, timeout=300) as resp:
                txt = resp.read()
                return json.loads(txt) if txt else None
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"{method} {path} -> {e.code} {e.read()[:400]!r}")

    def upload(self, path):
        boundary = uuid.uuid4().hex
        name = os.path.basename(path)
        ctype = mimetypes.guess_type(name)[0] or "application/octet-stream"
        body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{name}\"\r\nContent-Type: {ctype}\r\n\r\n").encode() + \
            open(path, "rb").read() + f"\r\n--{boundary}--\r\n".encode()
        return self.req("POST", "/ai-api/uploads", raw_body=body, ctype=f"multipart/form-data; boundary={boundary}")

    def stream(self, path, body):
        data = json.dumps(body).encode()
        r = urllib.request.Request(BASE + path, data=data, method="POST")
        r.add_header("Content-Type", "application/json")
        r.add_header("Accept", "text/event-stream")
        r.add_header("X-CSRF-Token", self.csrf())
        events = []
        with self.op.open(r, timeout=300) as resp:
            ev, buf = "message", []
            for line in resp:
                line = line.decode().rstrip("\n")
                if line.startswith("event:"):
                    ev = line[6:].strip()
                elif line.startswith("data:"):
                    buf.append(line[5:].strip())
                elif line == "" and buf:
                    events.append((ev, json.loads("\n".join(buf))))
                    ev, buf = "message", []
        return events


def last(events, name):
    for ev, d in reversed(events):
        if ev == name:
            return d
    return None


def show(title, events):
    print(f"\n=== {title}")
    for ev, d in events:
        if ev in ("status",):
            print(f"  [{ev}] {d['text']}")
        elif ev == "delta":
            continue
        else:
            s = json.dumps(d, ensure_ascii=False)
            print(f"  [{ev}] {s[:600]}")


def check(name, ok, detail=""):
    RESULTS.append((name, ok, detail))
    print(f"  {'PASS' if ok else 'FAIL'}: {name} {detail}")


def answer_all(c, task_id, events, choices, title):
    """Answers questions using choices[field] (value or callable(options))."""
    for _ in range(8):
        q = last(events, "question")
        if not q or last(events, "approval") or last(events, "result"):
            return events
        field = q["field"]
        if field not in choices:
            print(f"  unanswered question {field}: {q['text']} {q.get('options')}")
            return events
        v = choices[field]
        if callable(v):
            v = v(q.get("options") or [])
        events = c.stream(f"/ai-api/tasks/{task_id}/answer", {"question_id": q["id"], "value": v, "remember": field in choices.get("_remember", [])})
        show(f"{title} — answered {field}={v}", events)
    return events


def pick(label_contains):
    def f(opts):
        for o in opts:
            if label_contains in o["label"]:
                return o["value"]
        return opts[0]["value"] if opts else ""
    return f


def main():
    admin = Client("admin@demo.test", "Demo-Passw0rd!")
    # make sure the switch is in its default state
    admin.req("PUT", "/ai-api/settings", {"auto_mode": False})

    # ---- 1. Demo script: advance payment for the website project from Company X, with receipt
    up = admin.upload(os.path.join(HERE, "fixtures", "receipt-advance.png"))
    ev = admin.stream("/ai-api/tasks", {"message": "Register this as the advance payment for the website project from Company X.", "uploads": [up["id"]], "exec_mode": "api"})
    show("advance payment (demo script)", ev)
    tid = last(ev, "task")["task_id"]
    ev = answer_all(admin, tid, ev, {"dest_account": pick("ملت"), "party": pick("Company X")}, "advance")
    ap = last(ev, "approval")
    check("advance: waits for human approval by default", ap is not None)
    prev = last(ev, "preview")
    if prev:
        d = prev["display"]
        check("advance: project resolved", "وب" in (d.get("project") or ""), d.get("project"))
        check("advance: party resolved", d.get("party") == "Company X", d.get("party"))
        check("advance: amount from receipt (or flagged for confirmation)", d.get("amount") == "120000000" or prev["flags"]["low_confidence"] or "confident_fields" in (prev["decision"]["reasons"] or []), d.get("amount"))
    if ap:
        ev = admin.stream(f"/ai-api/tasks/{tid}/approve", {"token": ap["token"], "payload_hash": ap["payload_hash"], "exec_mode": "api"})
        show("advance — approved", ev)
        res = last(ev, "result")
        r = res and res["result"]
        check("advance: registered and verified", bool(r and r["verified"]), res and res["summary"])
        if r:
            tx = admin.req("GET", f"/api/v1/treasury/transactions/{r['entity_id']}")
            check("advance: read back from accounting API", tx["status"] == "posted" and tx["amount"] in ("120000000", "120000000.00"), f"{tx['amount']} {tx['status']}")
            check("advance: tagged with project cost center", tx["cost_center_id"] is not None)
            att = admin.req("GET", f"/api/v1/attachments?entity_type=treasury_transaction&entity_id={r['entity_id']}")
            check("advance: original receipt attached", len(att) == 1, str([a["file_name"] for a in att]))
            audit = admin.req("GET", "/api/v1/audit?page_size=50")
            items = audit.get("items", audit) if isinstance(audit, dict) else audit
            mine = [i for i in items if i.get("action") == "create" and i.get("entity_id") == r["entity_id"]]
            check("advance: accounting audit log attributes the document to the real user", bool(mine) and all(i.get("user_name") == "Demo Admin" for i in mine),
                  str([(i.get("action"), i.get("user_name")) for i in mine]))
            ev = admin.stream(f"/ai-api/tasks/{tid}/pdf", {})
            f = last(ev, "file")
            check("advance: PDF generated", bool(f), f and f["url"])

    # ---- 2. Example: loan from an individual
    up = admin.upload(os.path.join(HERE, "fixtures", "receipt-loan.png"))
    ev = admin.stream("/ai-api/tasks", {"message": "این رو از محمد تفکری برای هزینه‌های شرکت قرض گرفتیم", "uploads": [up["id"]], "exec_mode": "api"})
    show("loan from individual", ev)
    tid = last(ev, "task")["task_id"]
    q = last(ev, "question")
    asked_account = bool(q and q["field"] == "counter_account") or any(e == "question" and d["field"] == "counter_account" for e, d in ev)
    ev = answer_all(admin, tid, ev, {"counter_account": pick("سایر"), "dest_account": pick("ملت"), "_remember": ["counter_account"]}, "loan")
    check("loan: asked which account (no suitable shareholder-loan account, never guessed)", asked_account or any(e == "question" for e, _ in ev))
    ap = last(ev, "approval")
    if ap:
        ev = admin.stream(f"/ai-api/tasks/{tid}/approve", {"token": ap["token"], "payload_hash": ap["payload_hash"], "exec_mode": "api"})
        show("loan — approved", ev)
        r = (last(ev, "result") or {}).get("result")
        check("loan: registered and verified", bool(r and r["verified"]))
        if r:
            tx = admin.req("GET", f"/api/v1/treasury/transactions/{r['entity_id']}")
            check("loan: credited to the chosen account with the lender as party", tx["counter_account_id"] is not None and (tx.get("party_name") or "") == "محمد تفکری", f"{tx.get('counter_account_name')} / {tx.get('party_name')}")
    rules = admin.req("GET", "/ai-api/rules")
    check("loan: remembered the account (learned rule created by explicit choice)", any(r["kind"] == "event_account" for r in rules["rules"]))

    # ---- 3. Example: VPN expense with USD receipt
    up = admin.upload(os.path.join(HERE, "fixtures", "receipt-vpn.png"))
    ev = admin.stream("/ai-api/tasks", {"message": "Purchased a VPN for the company, receipt attached", "uploads": [up["id"]], "exec_mode": "api"})
    show("VPN expense", ev)
    tid = last(ev, "task")["task_id"]
    fields = [d["field"] for e, d in ev if e == "question"]
    ev = answer_all(admin, tid, ev, {"fx_rate": "1050000", "paid_by": "company_card", "source_account": pick("ملت"),
                                     "category": pick("اینترنت")}, "vpn")
    fields += [d["field"] for e, d in ev if e == "question"]
    check("vpn: asked the FX rate", "fx_rate" in fields, str(fields))
    ap = last(ev, "approval")
    prev = last(ev, "preview")
    if prev:
        check("vpn: converted amount = 9.99 × 1,050,000", prev["display"]["amount"] == "10489500", prev["display"]["amount"])
        check("vpn: category chosen from the company's categories", bool(prev["display"].get("category")), prev["display"].get("category"))
    if ap:
        check("vpn: FX forces human approval", any(r["code"] == "base_currency" for r in ap["reasons"]))
        # correction in review: change the description → the old approval is void
        ev2 = admin.stream(f"/ai-api/tasks/{tid}/edit", {"description": "NordVPN monthly subscription"})
        show("vpn — edited", ev2)
        bad = admin.stream(f"/ai-api/tasks/{tid}/approve", {"token": ap["token"], "exec_mode": "api"})
        check("vpn: old approval rejected after edit", last(bad, "error") is not None)
        ap2 = last(ev2, "approval")
        ev = admin.stream(f"/ai-api/tasks/{tid}/approve", {"token": ap2["token"], "payload_hash": ap2["payload_hash"], "exec_mode": "api"})
        show("vpn — approved", ev)
        r = (last(ev, "result") or {}).get("result")
        check("vpn: expense registered and verified", bool(r and r["verified"]))
        if r:
            x = admin.req("GET", f"/api/v1/expenses/{r['entity_id']}")
            check("vpn: expense posted with description edit", x["status"] == "posted" and "NordVPN" in x["description"], x["description"])

    # ---- 4. Correction in review → "remember?" → learned rule → suggested next time
    msg = "یک اشتراک وی پی ان دیگر به مبلغ ۵۰۰ هزار ریال امروز با کارت بانک ملت شرکت خریدیم"
    ev = admin.stream("/ai-api/tasks", {"message": msg, "exec_mode": "api"})
    show("VPN again (correction)", ev)
    tid = last(ev, "task")["task_id"]
    ev = answer_all(admin, tid, ev, {"source_account": pick("ملت"), "paid_by": "company_card", "category": pick("سایر")}, "vpn2")
    cats = admin.req("GET", "/api/v1/expenses/categories")
    other = next(c["id"] for c in cats if c["name"] == "سایر")
    prev = last(ev, "preview") or {}
    chosen = (prev.get("display") or {}).get("category")
    target = other if chosen != "سایر" else next(c["id"] for c in cats if "اینترنت" in c["name"])
    ev = admin.stream(f"/ai-api/tasks/{tid}/edit", {"category_id": target})
    show("VPN again — user corrected the category in review", ev)
    offers = last(ev, "offers") or []
    check("learning: correction produced a 'remember this?' offer", len(offers) == 1, json.dumps(offers, ensure_ascii=False)[:200])
    if offers:
        admin.req("POST", f"/ai-api/tasks/{tid}/remember", {"offer_id": offers[0]["id"]})
    ap = last(ev, "approval")
    if ap:
        admin.req("POST", f"/ai-api/tasks/{tid}/cancel")
    rules = admin.req("GET", "/ai-api/rules")
    check("learning: expense-category rule stored (personal, suggest stage)", any(r["kind"] == "expense_category" and r["scope"] == "personal" and r["stage"] == "suggest" for r in rules["rules"]),
          json.dumps([(r["kind"], r["pattern"], r["result"].get("name"), r["stage"]) for r in rules["rules"]], ensure_ascii=False))
    ev = admin.stream("/ai-api/tasks", {"message": "اشتراک وی پی ان این ماه را هم به مبلغ ۵۰۰ هزار ریال با کارت بانک ملت شرکت پرداخت کردیم", "exec_mode": "api"})
    show("VPN third time (rule suggests)", ev)
    q = next((d for e, d in ev if e == "question" and d["field"] == "category"), None)
    pv3 = last(ev, "preview") or {}
    learned_used = (pv3.get("display") or {}).get("category") == next(c["name"] for c in cats if c["id"] == target) and "rule_trusted" in ((pv3.get("decision") or {}).get("reasons") or [])
    # either it asks "last time you chose X, same?" (model disagreed) or applies X and flags it for confirmation (model agreed)
    check("learning: next time the learned category is suggested first / applied and flagged",
          bool(q and q["options"][0]["value"] == str(target) and q["options"][0].get("recommended")) or learned_used,
          json.dumps(q, ensure_ascii=False)[:300] if q else json.dumps(pv3.get("display"), ensure_ascii=False)[:200])
    t3 = last(ev, "task")
    if t3:
        admin.req("POST", f"/ai-api/tasks/{t3['task_id']}/cancel")

    # ---- 5. Prompt injection inside a receipt
    admin.req("PUT", "/ai-api/settings", {"auto_mode": True, "risk_confirmed": True})
    before = admin.req("GET", "/api/v1/treasury/transactions?page_size=100")["total"]
    up = admin.upload(os.path.join(HERE, "fixtures", "receipt-injection.png"))
    ev = admin.stream("/ai-api/tasks", {"message": "", "uploads": [up["id"]], "exec_mode": "api"})
    show("injection receipt (auto mode ON, no user words)", ev)
    after = admin.req("GET", "/api/v1/treasury/transactions?page_size=100")["total"]
    check("injection: nothing registered automatically", after == before, f"{before}->{after}")
    rules2 = admin.req("GET", "/ai-api/rules")
    check("injection: no rule created from document text", len(rules2["rules"]) == len(rules["rules"]))
    t = last(ev, "task")
    if t and last(ev, "approval"):
        admin.req("POST", f"/ai-api/tasks/{t['task_id']}/cancel")

    # ---- 6. Auto mode, clean case + Undo
    ev = admin.stream("/ai-api/tasks", {"message": "Register a 7,000,000 rial receipt from Company X into Bank Mellat today, reference 9911", "exec_mode": "api"})
    show("auto mode clean receipt", ev)
    ev = answer_all(admin, (last(ev, "task") or {}).get("task_id"), ev, {"dest_account": pick("ملت"), "party": pick("Company X")}, "auto")
    res = last(ev, "result")
    if res:
        check("auto: registered automatically within limits", res["result"]["mode"] == "auto" and res["result"]["verified"], res["summary"])
    else:
        ap = last(ev, "approval")
        check("auto: registered automatically within limits", False, "waited for approval: " + json.dumps(ap and ap["reasons"], ensure_ascii=False))
    act = admin.req("GET", "/ai-api/activity")
    auto_items = [i for i in act["items"] if i["mode"] == "auto" and i["undo_available"]]
    if auto_items:
        tid = auto_items[0]["task_id"]
        ev = admin.stream(f"/ai-api/tasks/{tid}/undo", {})
        ap = last(ev, "approval")
        check("undo: cancellation always needs a human (approval card even in auto mode)", ap is not None)
        if ap:
            ev = admin.stream(f"/ai-api/tasks/{ap['task_id']}/approve", {"token": ap["token"]})
            show("undo — approved", ev)
            eid = auto_items[0]["result"]["entity_id"]
            tx = admin.req("GET", f"/api/v1/treasury/transactions/{eid}")
            check("undo: cancelled by reversal in the accounting app", tx["status"] == "cancelled", tx["status"])
    admin.req("PUT", "/ai-api/settings", {"auto_mode": False})

    # ---- 7. Permission inheritance with a real restricted user
    viewer = Client("viewer@demo.test", "Viewer-Passw0rd!")
    before = admin.req("GET", "/api/v1/treasury/transactions?page_size=100")["total"]
    ev = viewer.stream("/ai-api/tasks", {"message": "Register a 1,000,000 rial receipt from Company X today", "exec_mode": "api"})
    show("viewer tries to register", ev)
    after = admin.req("GET", "/api/v1/treasury/transactions?page_size=100")["total"]
    check("permissions: viewer cannot register through the AI", after == before and last(ev, "approval") is None)

    # ---- 8. Queries
    ev = admin.stream("/ai-api/tasks", {"message": "هزینه‌های این ماه چقدر بوده؟", "exec_mode": "api"})
    show("query: this month's expenses", ev)
    check("query: answered from data", last(ev, "message") is not None or last(ev, "table") is not None)
    ev = admin.stream("/ai-api/tasks", {"message": "Show me unpaid customer invoices", "exec_mode": "api"})
    show("query: unpaid invoices", ev)
    check("query: unpaid invoices answered", last(ev, "message") is not None)

    # ---- 9. Multi-turn memory: the Galatea conversation (never re-ask what is known)
    ev = admin.stream("/ai-api/tasks", {"message": "Add a product named Galatea to our inventory. It is manufactured by our company and has a value of 10,000,000 tomans.", "exec_mode": "api"})
    show("galatea turn 1", ev)
    conv = (last(ev, "conversation") or {}).get("conversation_id", "")
    q1 = last(ev, "question")
    check("memory: turn 1 asks only for missing fields (not name/value/document type)", bool(q1) and not any(w in q1["text"].lower() for w in ("name", "price", "purchase", "sales", "value")),
          q1 and q1["text"])
    ev2 = admin.stream("/ai-api/tasks", {"message": "10 units, unit = piece, default warehouse, no product code.", "exec_mode": "api", "conversation_id": conv})
    show("galatea turn 2", ev2)
    check("memory: same conversation and operation continued", (last(ev2, "conversation") or {}).get("conversation_id") == conv and last(ev2, "task") == last(ev, "task"))
    check("memory: nothing asked again once complete", last(ev2, "question") is None, json.dumps(last(ev2, "question"), ensure_ascii=False)[:200])
    pv = last(ev2, "preview") or {}
    d = pv.get("display") or {}
    check("memory: all facts merged (Galatea, 10 piece, 100,000,000 IRR/unit, no code, default warehouse)",
          pv.get("action") == "create_inventory_receipt" and d.get("product") == "Galatea" and d.get("quantity") == "10" and d.get("unit_value") == "100000000"
          and bool(d.get("product_code")) and bool(d.get("warehouse")), json.dumps(d, ensure_ascii=False)[:300])
    view = admin.req("GET", f"/ai-api/conversations/{conv}")
    check("memory: refresh restores the conversation (transcript + open approval)", len([m for m in view["messages"] if m["role"] == "user"]) == 2 and view["task"]["stage"] == "awaiting_approval")
    if last(ev2, "approval"):
        admin.req("POST", f"/ai-api/tasks/{last(ev2, 'approval')['task_id']}/cancel")

    print("\n=== SUMMARY")
    failed = [r for r in RESULTS if not r[1]]
    for n, ok, d in RESULTS:
        print(f"{'PASS' if ok else 'FAIL'}  {n}")
    print(f"{len(RESULTS) - len(failed)}/{len(RESULTS)} passed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
