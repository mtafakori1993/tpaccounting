#!/usr/bin/env python3
"""Real E2E: fresh-context recovery (D62) on the THROWAWAY test stack, real model.

The conflicting state is created deterministically through the real application: a
delete of a throwaway employee is approved "on screen", which the screen executor cannot
do (NO_UI_EXECUTOR) — the conversation is left with a blocked task. Then:

  F1  the exact antenna receipt → one fresh context, exact text, questions, approval,
      direct execution, exactly one expense read back; request trace across contexts
  F2  «این رسید خرید را ثبت کن» + an image → same upload id and content hash in the
      fresh context, nothing written before planning
  F3  contextual follow-ups (علی …) stay in the same conversation
  F4  a NO_EXECUTOR/failed task, then «مراکز هزینه رو لیست کن» → processed normally
  F5  open delete of «عیاشی», then «برای پروژه وب‌سایت یه هزینه ثبت کن» → a new task
      that does not inherit the cost centre

Usage: e2e_fresh_context.py [base_url] [only]
"""
import hashlib
import json
import os
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
ARGS = sys.argv[1:]
sys.argv = [sys.argv[0]] + ARGS[:1]
from live_scenarios import Client, last, show  # noqa: E402

ONLY = ARGS[1] if len(ARGS) > 1 else ""
RESULTS = []
c = Client("admin@demo.test", "Demo-Passw0rd!")
ANTENNA = "یه رسید خرید آنتن برای سرایداری شرکت ۱۲۰۰۰۰۰ تومان بزن"
FRESH = "زمینه تازه"


def national_id(seed):
    """A checksum-valid Iranian national id (test data only)."""
    d = [int(x) for x in str(100000000 + seed % 800000000)[-9:]]
    r = sum(v * (10 - i) for i, v in enumerate(d)) % 11
    return "".join(map(str, d)) + str(r if r < 2 else 11 - r)


def check(name, ok, detail=""):
    RESULTS.append({"check": name, "ok": bool(ok), "detail": detail})
    print(f"  {'PASS' if ok else 'FAIL'}: {name} {detail}")


def run(path, body, title):
    ev = c.stream(path, body)
    show(title, ev)
    return ev


def text(ev):
    return " ".join(json.dumps(d, ensure_ascii=False) for _, d in ev)


def items(x):
    return x if isinstance(x, list) else (x or {}).get("items") or []


def request_id(ev):
    for e, d in ev:
        if e == "request":
            return d["request_id"]
    return ""


def blocked_conversation(tag):
    """A conversation whose current task is blocked (NO_UI_EXECUTOR) — real state."""
    name = f"آزمون{tag}"
    seed = int(time.time() * 1000)
    emp = c.req("POST", "/api/v1/employees", {"code": "T%d" % (seed % 10000000), "first_name": "کارمند", "last_name": name, "national_id": national_id(seed),
                                             "hire_date": "2025-03-21", "employment_type": "full_time", "base_salary": "0"})
    ev = run("/ai-api/tasks", {"message": f"کارمند کارمند {name} رو حذف کن"}, "stale state: delete request")
    conv = last(ev, "conversation")["conversation_id"]
    ap = last(ev, "approval")
    if not ap:
        return conv, emp, None
    ev = run(f"/ai-api/tasks/{ap['task_id']}/approve", {"token": ap["token"], "payload_hash": ap["payload_hash"], "exec_mode": "human_ui"}, "stale state: insist on screen")
    return conv, emp, last(ev, "ui_failed")


def answer_until_approval(ev, tid):
    for _ in range(6):
        if last(ev, "approval") or last(ev, "result"):
            return ev
        q = last(ev, "question")
        if not q:
            return ev
        opts = q.get("options") or []
        val = opts[0]["value"] if opts else "شرکت از حساب بانک پرداخت کرد"
        for o in opts:  # the company paid / a bank account, when offered
            if any(k in (o.get("label") or "") for k in ("شرکت", "بانک", "company")):
                val = o["value"]
                break
        ev = run(f"/ai-api/tasks/{tid}/answer", {"question_id": q["id"], "value": val}, f"answer {q.get('field')}={val}")
    return ev


def f1():
    print("\n##### F1 antenna receipt after a blocked task")
    conv, emp, blocked = blocked_conversation("الف")
    check("F1.0 stale state is a blocked task (NO_UI_EXECUTOR)", blocked is not None and blocked.get("failure_code") == "NO_UI_EXECUTOR", str(blocked)[:160])
    before = {x["id"] for x in items(c.req("GET", "/api/v1/expenses?page_size=500"))}
    ev = run("/ai-api/tasks", {"message": ANTENNA, "conversation_id": conv}, "antenna request")
    rid = request_id(ev)
    conv2 = (last(ev, "conversation") or {}).get("conversation_id")
    check("F1.1 conflict detected → fresh context (new conversation)", conv2 and conv2 != conv and FRESH in text(ev), f"{conv} → {conv2}")
    view = c.req("GET", f"/ai-api/conversations/{conv2}")
    first = (view.get("messages") or [{}])[0]
    check("F1.2 exact request is the first message", first.get("role") == "user" and first.get("text") == ANTENNA, repr(first.get("text")))
    tid = (last(ev, "task") or {}).get("task_id")
    ev = answer_until_approval(ev, tid)
    ap = last(ev, "approval")
    check("F1.3 normal approval in the fresh context", ap is not None, text(ev)[-300:] if not ap else "")
    check("F1.4 nothing written before approval", {x["id"] for x in items(c.req("GET", "/api/v1/expenses?page_size=500"))} == before)
    if ap:
        ev = run(f"/ai-api/tasks/{ap['task_id']}/approve", {"token": ap["token"], "payload_hash": ap["payload_hash"], "exec_mode": "direct"}, "approve directly")
        res = last(ev, "result") or {}
        check("F1.5 executed and verified", (res.get("result") or {}).get("verified") is True, text(ev)[-300:] if not res else "")
    new = [x for x in items(c.req("GET", "/api/v1/expenses?page_size=500")) if x["id"] not in before]
    amounts = [str(x.get("amount", x.get("total", ""))).split(".")[0] for x in new]
    check("F1.6 exactly one accounting record of 12,000,000 rial", len(new) == 1 and amounts == ["12000000"], json.dumps(
        [{k: x.get(k) for k in ("id", "amount", "description", "status")} for x in new], ensure_ascii=False))
    check("F1.7 the blocked delete was not executed by the reset", c.req("GET", f"/api/v1/employees/{emp['id']}") is not None)
    for x in new:  # cleanup (verified): the test expense is cancelled through the application
        c.req("POST", f"/api/v1/expenses/{x['id']}/cancel", {"reason": "e2e cleanup"})
    still = [y for y in items(c.req("GET", "/api/v1/expenses?page_size=500")) if y["id"] in {x["id"] for x in new} and y.get("status") != "cancelled"]
    check("F1.10 cleanup verified (test expense cancelled)", not still)
    tr = c.req("GET", f"/ai-api/requests/{rid}/trace")
    atts = tr.get("attempts") or []
    check("F1.8 one request id across both contexts", len(atts) == 2 and atts[0].get("result") == "CONTEXT_CONFLICT" and atts[0].get("conversation_id") == conv
          and atts[1].get("conversation_id") == conv2 and tr.get("context_reset_count") == 1, json.dumps([{k: a.get(k) for k in ("attempt", "conversation_id", "action", "result")} for a in atts], ensure_ascii=False))
    rs = (tr.get("context_resets") or [{}])[0]
    check("F1.9 reset cause recorded", rs.get("causing_state") and rs.get("affected_decision") and "UNRECOVERABLE_TASK_STATE" in json.dumps(rs.get("reasons")), json.dumps(
        {k: rs.get(k) for k in ("causing_state", "affected_decision", "architecture_bug")}, ensure_ascii=False))


def f2():
    print("\n##### F2 attachment preserved")
    conv, emp, blocked = blocked_conversation("ب")
    path = os.path.join(HERE, "fixtures", "invoice-purchase.png")
    up = c.upload(path)
    digest = hashlib.sha256(open(path, "rb").read()).hexdigest()
    before = {x["id"] for x in items(c.req("GET", "/api/v1/expenses?page_size=500"))}
    msg = "این رسید خرید را ثبت کن"
    ev = run("/ai-api/tasks", {"message": msg, "uploads": [up["id"]], "conversation_id": conv}, "receipt with attachment")
    conv2 = (last(ev, "conversation") or {}).get("conversation_id")
    check("F2.1 fresh context", conv2 and conv2 != conv and FRESH in text(ev))
    tid = (last(ev, "task") or {}).get("task_id")
    tr = c.req("GET", f"/ai-api/tasks/{tid}/trace")
    ups = tr.get("uploads") or []
    check("F2.2 exact same text", tr.get("request") == msg, repr(tr.get("request")))
    check("F2.3 same attachment (id) and content hash, no duplicate", len(ups) == 1 and ups[0]["id"] == up["id"] and ups[0]["sha256"] == digest,
          json.dumps(ups, ensure_ascii=False)[:200])
    check("F2.4 no mutation before fresh planning", {x["id"] for x in items(c.req("GET", "/api/v1/expenses?page_size=500"))} == before)
    check("F2.5 planning proceeded normally", bool(last(ev, "question") or last(ev, "approval") or last(ev, "preview")), text(ev)[-200:])
    if last(ev, "approval"):
        c.req("POST", f"/ai-api/tasks/{last(ev, 'approval')['task_id']}/cancel")


def f3():
    print("\n##### F3 contextual follow-ups stay")
    ev = run("/ai-api/tasks", {"message": "یه کارمند به اسم علی بساز"}, "create")
    conv = last(ev, "conversation")["conversation_id"]
    for m in ["حقوقش رو ۳۰ میلیون کن", "حالا بهش کمک هزینه سفر هم اضافه کن"]:
        ev = run("/ai-api/tasks", {"message": m, "conversation_id": conv}, m)
        check(f"F3 «{m}» kept the conversation", (last(ev, "conversation") or {}).get("conversation_id") == conv and FRESH not in text(ev))
    for e, d in ev:
        if e == "approval":
            c.req("POST", f"/ai-api/tasks/{d['task_id']}/cancel")


def f4():
    print("\n##### F4 failed executor state does not contaminate")
    conv, emp, blocked = blocked_conversation("ج")
    # the user does not insist: the blocked task is closed by cancelling it (failed/closed state)
    tid = None
    for t in c.req("GET", "/ai-api/tasks")[:3]:
        if t.get("stage") == "ui_failed":
            tid = t["id"]
            c.req("POST", f"/ai-api/tasks/{tid}/cancel")
            break
    ev = run("/ai-api/tasks", {"message": "مراکز هزینه رو لیست کن", "conversation_id": conv}, "list cost centres")
    check("F4 processed normally (listed, no reset, no executor error)", "پروژه" in text(ev) and FRESH not in text(ev) and "NO_UI_EXECUTOR" not in text(ev)
          and "روی صفحه قابل انجام نیست" not in text(ev), text(ev)[-200:])


def f5():
    print("\n##### F5 contradiction does not inherit the cost centre")
    ccs = items(c.req("GET", "/api/v1/cost-centers?page_size=200"))
    if not [x for x in ccs if x["name"] == "عیاشی"]:
        c.req("POST", "/api/v1/cost-centers", {"code": "CC%d" % (int(time.time()) % 100000), "name": "عیاشی", "kind": "department"})
    ev = run("/ai-api/tasks", {"message": "مرکز هزینه عیاشی رو حذف کن"}, "delete cost centre (left open)")
    conv = last(ev, "conversation")["conversation_id"]
    ev = run("/ai-api/tasks", {"message": "برای پروژه وب‌سایت یه هزینه ثبت کن", "conversation_id": conv}, "new expense")
    tid = (last(ev, "task") or {}).get("task_id")
    tr = c.req("GET", f"/ai-api/tasks/{tid}/trace")
    blob = json.dumps({k: tr.get(k) for k in ("interpretation", "plan", "request")}, ensure_ascii=False)
    check("F5 new independent task without «عیاشی»", "عیاشی" not in blob and "وب" in blob, blob[:200])
    for e, d in ev:
        if e == "approval":
            c.req("POST", f"/ai-api/tasks/{d['task_id']}/cancel")


for name, fn in [("F1", f1), ("F2", f2), ("F3", f3), ("F4", f4), ("F5", f5)]:
    if not ONLY or ONLY == name:
        try:
            fn()
        except Exception as ex:
            check(f"{name} crashed", False, repr(ex)[:300])

# the throwaway employees used for the blocked state are removed (they would pollute other suites)
for e in items(c.req("GET", "/api/v1/employees?page_size=200")):
    if e.get("first_name") == "کارمند" and str(e.get("last_name", "")).startswith("آزمون"):
        try:
            c.req("DELETE", f"/api/v1/employees/{e['id']}")
        except RuntimeError:
            pass

m = c.req("GET", "/ai-api/metrics/context")
print("\nmetrics:", json.dumps(m, ensure_ascii=False))
out = os.path.join(HERE, "..", "test-artifacts", "e2e-fresh-context.json")
json.dump({"at": time.strftime("%Y-%m-%dT%H:%M:%S"), "results": RESULTS, "metrics": m}, open(out, "w"), ensure_ascii=False, indent=1)
fails = [r for r in RESULTS if not r["ok"]]
print(f"\n{len(RESULTS) - len(fails)}/{len(RESULTS)} passed")
sys.exit(1 if fails else 0)
