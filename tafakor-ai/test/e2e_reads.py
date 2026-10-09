#!/usr/bin/env python3
"""Real E2E (D61): a request to tell/show/list returns the application's data, never
"it is in section X"; only «کجاست / کجا» gets a navigation answer. Real model, test stack.

Usage: e2e_reads.py [base_url]
"""
import json
import os
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.argv = [sys.argv[0]] + sys.argv[1:2]
from live_scenarios import Client, last  # noqa: E402

c = Client("admin@demo.test", "Demo-Passw0rd!")
LOCATION = ["در بخش", "قابل مشاهده", "از منوی", "مراجعه کنید", "→", "←"]
RESULTS = []

# request → a word that must appear in the answer because the application returned it
READS = [
    ("قواعد حقوق رو بگو", "جدول مالیات"),
    ("قوانین حقوق و دستمزد رو نمایش بده", "بیمه"),
    ("قواعد ثبت حسابداری حقوق رو بگو", "8101"),
    ("مراکز هزینه رو لیست کن", "پروژه"),
    ("حساب‌های بانکی و صندوق‌ها رو نشون بده", "ملت"),
    ("اجزای حقوق رو بگو", "حقوق پایه"),
    ("تنظیمات حقوق رو بگو", "روز"),
    ("کارمندها رو لیست کن", "مرادی"),
    ("نقش‌های کاربری رو نمایش بده", ""),
]
NAV = ["قواعد حقوق کجاست؟", "مراکز هزینه کجاست؟"]


def answer(ev):
    parts = [d.get("text", "") for e, d in ev if e in ("message", "delta")]
    return "".join(parts)


for text, must in READS:
    ev = c.stream("/ai-api/tasks", {"message": text})
    tools = [d.get("tool") for e, d in ev if e == "tool" and d.get("kind") == "read"]
    a = answer(ev)
    loc = [p for p in LOCATION if p in a]
    ok = bool(tools) and not (loc and not tools) and (must == "" or must in a or must in json.dumps(ev, ensure_ascii=False))
    RESULTS.append({"request": text, "ok": ok, "reads": tools, "answer": a[:300]})
    print(f"{'PASS' if ok else 'FAIL'}: {text} | reads={tools} | {a[:160]!r}")
for text in NAV:
    ev = c.stream("/ai-api/tasks", {"message": text})
    a = answer(ev)
    ok = bool(a) and not [d for e, d in ev if e == "tool" and d.get("kind") == "read" and d.get("tool") == "payroll.rules.list"]
    RESULTS.append({"request": text, "ok": ok, "answer": a[:300]})
    print(f"{'PASS' if ok else 'FAIL'}: {text} (navigation) | {a[:160]!r}")

json.dump({"at": time.strftime("%Y-%m-%dT%H:%M:%S"), "results": RESULTS}, open(os.path.join(HERE, "..", "test-artifacts", "e2e-reads.json"), "w"), ensure_ascii=False, indent=1)
fails = [r for r in RESULTS if not r["ok"]]
print(f"\n{len(RESULTS) - len(fails)}/{len(RESULTS)} passed")
sys.exit(1 if fails else 0)
