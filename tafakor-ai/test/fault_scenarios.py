#!/usr/bin/env python3
"""Live failure-injection scenarios (test stack, real app + DB, AI service routed
through fault_proxy.py). They use the deterministic payroll skills, so they need
no model credit. Every assertion reads state back from the accounting API.
Usage: fault_scenarios.py   (app on :18085, fault proxy on :18086)
"""
import json, sys, threading, urllib.request, time, random
sys.path.insert(0, __import__("os").path.dirname(__file__))
import live_scenarios as L

L.BASE = "http://127.0.0.1:18085"
PROXY = "http://127.0.0.1:18086"
SUFFIX = "".join(random.choice("بپتثجچحخدذرزسشصضطظعغفقکگلمنوهی") for _ in range(4))
results = []


def check(name, ok, detail=""):
    results.append((name, ok, detail))
    print(("PASS" if ok else "FAIL") + ": " + name + " " + str(detail))


def arm(mode, match):
    urllib.request.urlopen(PROXY + "/__fault?" + urllib.parse.urlencode({"mode": mode, "match": match})).read()


def comps(c, name):
    return [x for x in c.req("GET", "/api/v1/payroll/components") if x["name"] == name]


def say(c, text, conv=""):
    ev = c.stream("/ai-api/tasks", {"message": text, "exec_mode": "api", "ui_capable": False, "conversation_id": conv})
    conv = L.last(ev, "conversation")["conversation_id"]
    return ev, conv


def approve(c, ev):
    ap = L.last(ev, "approval")
    if not ap:
        L.show("no approval card", ev)
        return []
    return c.stream(f"/ai-api/tasks/{ap['task_id']}/approve", {"token": ap["token"]})


def trace(c, ev):
    tid = (L.last(ev, "task") or L.last(ev, "approval") or {}).get("task_id") or c.req("GET", "/ai-api/tasks")[0]["id"]
    return c.req("GET", f"/ai-api/tasks/{tid}/trace")


def main():
    import urllib.parse  # noqa
    c = L.Client("admin@demo.test", "Demo-Passw0rd!")

    # 1. lost response: the app committed, the AI service saw 502 → reconcile, no duplicate
    name = "کمک هزینه " + SUFFIX
    arm("lost", "POST /api/v1/payroll/components")
    ev, _ = say(c, f"به اجزای حقوق {name} اضافه کن ۵ میلیون تومان")
    res = approve(c, ev)
    r = L.last(res, "result")
    tr = trace(c, ev)
    check("lost response → one record, step recovered and verified", len(comps(c, name)) == 1 and r and r["result"]["verified"]
          and tr["counters"]["recoveries"] == 1, f"records={len(comps(c, name))} recoveries={tr['counters']}")

    # 2. app down for the write: verified NOT saved, user told so; retry sends once
    name = "مزایای ویژه " + SUFFIX
    arm("down", "POST /api/v1/payroll/components")
    ev, conv = say(c, f"به اجزای حقوق {name} اضافه کن ۲ میلیون تومان")
    res = approve(c, ev)
    err = L.last(res, "error") or {}
    check("write not reaching the app → reported as not saved (checked)", len(comps(c, name)) == 0 and "ثبت نشده" in err.get("message", ""), err.get("message", "")[:120])
    ev2, _ = say(c, "دوباره امتحان کن", conv)
    res2 = approve(c, ev2)
    check("retry → exactly one record", len(comps(c, name)) == 1 and L.last(res2, "result") is not None, f"records={len(comps(c, name))}")

    # 3. 500 on an employee update: the record is read back; not applied → not saved; retry ok
    emp = next(e for e in c.req("GET", "/api/v1/employees?page_size=200")["items"] if e["full_name"] == "علی رضایی")
    arm("e500", f"PUT /api/v1/employees/{emp['id']}")
    amt = random.randint(11, 99)  # a value the employee does not have yet
    ev, conv = say(c, f"برای کارمند علی رضایی کمک هزینه سفر {amt} هزار تومان ثبت کن")
    res = approve(c, ev)
    full = c.req("GET", f"/api/v1/employees/{emp['id']}")
    travel = comps(c, "کمک هزینه سفر")[0]["id"]
    has = [o for o in (full.get("components") or []) if o["component_id"] == travel]
    check("500 on update → nothing applied, user told", not [o for o in has if float(o["rate"]) == amt * 10000] and L.last(res, "error") is not None, (L.last(res, "error") or {}).get("message", "")[:100])
    ev2, _ = say(c, "دوباره امتحان کن", conv)
    approve(c, ev2)
    full = c.req("GET", f"/api/v1/employees/{emp['id']}")
    has = [o for o in (full.get("components") or []) if o["component_id"] == travel]
    check("retry applies the override once, other fields kept", len(has) == 1 and float(has[0]["rate"]) == amt * 10000 and full["code"] == emp["code"],
          json.dumps(has))

    # 4. expired / invalid session: refused, nothing done, plain message
    bad = L.Client.__new__(L.Client)
    import http.cookiejar
    bad.jar = http.cookiejar.CookieJar()
    bad.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(bad.jar))
    try:
        bad.stream("/ai-api/tasks", {"message": "به اجزای حقوق تست اضافه کن ۱ تومان"})
        check("expired session refused", False, "accepted")
    except Exception as e:  # noqa
        check("expired session refused", "401" in str(e) or "403" in str(e), str(e)[:80])

    # 5. two requests at once in one conversation: serialized, both complete, no corruption
    n1, n2 = "حق تخصص " + SUFFIX + "الف", "حق تخصص " + SUFFIX + "ب"
    _, conv = say(c, "اینو برای دفعه بعد یادت باشه")
    out = {}

    def go(k, text):
        out[k] = c.stream("/ai-api/tasks", {"message": text, "exec_mode": "api", "ui_capable": False, "conversation_id": conv})
    t1 = threading.Thread(target=go, args=("a", f"به اجزای حقوق {n1} اضافه کن ۱ میلیون تومان"))
    t2 = threading.Thread(target=go, args=("b", f"به اجزای حقوق {n2} اضافه کن ۲ میلیون تومان"))
    t1.start(); t2.start(); t1.join(); t2.join()
    aps = [L.last(out[k], "approval") for k in "ab"]
    texts = json.dumps([out[k] for k in "ab"], ensure_ascii=False)
    for k in "ab":
        approve(c, out[k])
    a, b = len(comps(c, n1)), len(comps(c, n2))
    told = "کنار گذاشته شد" in texts
    check("concurrent requests in one conversation: serialized; nothing lost silently, no duplicates",
          a <= 1 and b <= 1 and (a + b == 2 or (a + b == 1 and told)), f"{a},{b} superseded_told={told}")
    # 6–8 (D65): deletes and edits whose outcome is uncertain are reconciled from
    # application state — never resent blindly, never "not saved" without checking.
    # Isolated data: throwaway employees created here, verified removed at the end.
    def throwaway(tag):
        seed = int(time.time() * 1000) % 100000000
        d = [int(x) for x in str(100000000 + seed)[-9:]]
        r = sum(v * (10 - i) for i, v in enumerate(d)) % 11
        nid = "".join(map(str, d)) + str(r if r < 2 else 11 - r)
        last = "خطا" + tag + SUFFIX
        e = c.req("POST", "/api/v1/employees", {"code": "F" + str(seed % 1000000), "first_name": "آزمایش", "last_name": last, "national_id": nid,
                                                 "hire_date": "2025-03-21", "employment_type": "full_time", "base_salary": "0"})
        return e, "آزمایش " + last

    def deletes(eid):
        log = json.loads(urllib.request.urlopen(PROXY + "/__log").read())
        return len([x for x in log if x["req"] == f"DELETE /api/v1/employees/{eid}"])

    def gone(eid):
        try:
            c.req("GET", f"/api/v1/employees/{eid}")
            return False
        except RuntimeError as ex:
            return "404" in str(ex)

    e6, n6 = throwaway("الف")
    arm("lost", f"DELETE /api/v1/employees/{e6['id']}")
    ev, _ = say(c, f"کارمند {n6} رو حذف کن")
    res = approve(c, ev)
    r = L.last(res, "result")
    check("lost response on a delete → reconciled as done (gone), verified, sent once", gone(e6["id"]) and r and r["result"]["verified"] and deletes(e6["id"]) == 1,
          f"gone={gone(e6['id'])} deletes={deletes(e6['id'])} result={bool(r)}")

    e7, n7 = throwaway("ب")
    arm("down", f"DELETE /api/v1/employees/{e7['id']}")
    ev, conv = say(c, f"کارمند {n7} رو حذف کن")
    res = approve(c, ev)
    err = L.last(res, "error") or {}
    check("delete not reaching the app → verified still present, reported not saved", not gone(e7["id"]) and "ثبت نشده" in err.get("message", ""), err.get("message", "")[:120])
    ev2, _ = say(c, "دوباره امتحان کن", conv)
    approve(c, ev2)
    check("retry → deleted exactly once", gone(e7["id"]) and deletes(e7["id"]) == 2, f"deletes_seen_by_proxy={deletes(e7['id'])} (1 blocked + 1 real)")

    e8, n8 = throwaway("ج")
    arm("lost", f"PUT /api/v1/employees/{e8['id']}")
    ev, _ = say(c, f"موبایل کارمند {n8} رو 09121112233 کن")
    res = approve(c, ev) if L.last(ev, "approval") else ev  # a routine edit runs without a card
    full = c.req("GET", f"/api/v1/employees/{e8['id']}")
    r = L.last(res, "result") or L.last(ev, "result")
    check("lost response on an edit → reconciled (values read back), not sent twice", full.get("mobile") == "09121112233" and r is not None and r["result"]["verified"],
          f"mobile={full.get('mobile')} result={bool(r)}")
    # cleanup, verified
    for e in (e6, e7, e8):
        if not gone(e["id"]):
            c.req("DELETE", f"/api/v1/employees/{e['id']}")
    check("cleanup: throwaway employees removed", all(gone(e["id"]) for e in (e6, e7, e8)))
    # 9. the application answers 200 but saved nothing: never reported as done/verified
    name9 = "مزایای دروغین " + SUFFIX
    arm("fake200", "POST /api/v1/payroll/components")
    ev, _ = say(c, f"به اجزای حقوق {name9} اضافه کن ۱ میلیون تومان")
    res = approve(c, ev) if L.last(ev, "approval") else ev
    r = L.last(res, "result") or L.last(ev, "result") or {}
    txt = json.dumps(res, ensure_ascii=False) + json.dumps(ev, ensure_ascii=False)
    check("HTTP 200 without a stored record → not verified, no false success", len(comps(c, name9)) == 0 and r and r["result"].get("verified") is False,
          f"records={len(comps(c, name9))} verified={(r.get('result') or {}).get('verified')}")

    # the salary components this run created (names end with this run's suffix): the
    # application has no delete for components, so they are deactivated — and verified
    mine = [x for x in c.req("GET", "/api/v1/payroll/components") if SUFFIX in x["name"] and x.get("is_active")]
    for x in mine:
        body = {k: v for k, v in x.items() if k not in ("id", "created_at", "updated_at")}
        body["is_active"] = False
        c.req("PUT", f"/api/v1/payroll/components/{x['id']}", body)
    left = [x for x in c.req("GET", "/api/v1/payroll/components") if SUFFIX in x["name"] and x.get("is_active")]
    check("cleanup: this run's salary components deactivated", not left, f"deactivated={len(mine)} still_active={len(left)}")

    failed = [r for r in results if not r[1]]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")


if __name__ == "__main__":
    main()
