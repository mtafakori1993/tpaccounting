#!/usr/bin/env python3
"""Stress: N salary-component and N employee-compensation workflows with varied
phrasing, digit systems, separators, units and word order, through the real
assistant API (approval included). Each result is read back from the app:
exact amount, exactly one record, nothing else touched.
Usage: stress_payroll.py [N]
"""
import sys, random, json, os, time
sys.path.insert(0, os.path.dirname(__file__))
import live_scenarios as L
from fault_scenarios import say, approve, comps

L.BASE = "http://127.0.0.1:18085"
FA = str.maketrans("0123456789", "۰۱۲۳۴۵۶۷۸۹")
N = int(sys.argv[1]) if len(sys.argv) > 1 else 20
rnd = random.Random(int(time.time()))
LET = "بپتثجچحخدذرزسشصضطظعغفقکگلمنوهی"
WORDS = ["حق تخصص", "حق سختی کار", "کمک هزینه تلفن", "کمک هزینه ناهار", "حق کارانه", "فوق العاده شغل", "حق نوبت کاری", "کمک هزینه مهد"]


def amount_text():
    v = rnd.choice([12, 2232, 21000000, 3000000, 750000, 4500000, 1250000])
    unit = rnd.choice(["تومان", "ریال", ""])
    style = rnd.choice(["plain", "sep", "fa", "fa_sep", "million"])
    if style == "million" and v % 1000000 == 0:
        s = (str(v // 1000000).translate(FA) if rnd.random() < .5 else str(v // 1000000)) + " میلیون"
    elif style == "sep":
        s = f"{v:,}"
    elif style == "fa":
        s = str(v).translate(FA)
    elif style == "fa_sep":
        s = f"{v:,}".translate(FA).replace(",", "٬")
    else:
        s = str(v)
    rial = v * 10 if unit == "تومان" else v
    return (s + " " + unit).strip(), rial


def main():
    c = L.Client("admin@demo.test", "Demo-Passw0rd!")
    res = {"component": [0, 0], "employee": [0, 0]}
    fails = []
    for i in range(N):
        name = rnd.choice(WORDS) + " " + "".join(rnd.choice(LET) for _ in range(4))
        at, rial = amount_text()
        text = rnd.choice([f"به اجزای حقوق {name} اضافه کن {at}", f"{name} را به اجزای حقوق اضافه کن {at}",
                           f"{name} رو با مبلغ {at} به اجزای حقوق اضافه کن", f"لطفا {name} {at} به اجزای حقوق اضافه شود"])
        ev, _ = say(c, text)
        out = approve(c, ev)
        got = comps(c, name)
        ok = len(got) == 1 and float(got[0]["rate"]) == rial and got[0]["method"] == "fixed" and L.last(out, "result") and L.last(out, "result")["result"]["verified"]
        res["component"][0 if ok else 1] += 1
        if not ok:
            fails.append({"text": text, "expected": rial, "got": [(g["rate"], g["method"]) for g in got], "events": [e for e, _ in ev][-4:]})
        print(("PASS" if ok else "FAIL"), "component", text, "→", rial, [g["rate"] for g in got])
    emps = [e for e in c.req("GET", "/api/v1/employees?page_size=200")["items"] if e["full_name"] in ("زهرا معتضد", "محمد رضایی")]
    travel = comps(c, "کمک هزینه سفر")[0]["id"]
    for i in range(N):
        emp = rnd.choice(emps)
        at, rial = amount_text()
        text = rnd.choice([f"برای کارمند {emp['full_name']} کمک هزینه سفر {at} ثبت کن", f"کمک هزینه سفر کارمند {emp['full_name']} را {at} ثبت کن",
                           f"برای {emp['full_name']} (کارمند) کمک هزینه سفر به مبلغ {at} ثبت کن"])
        ev, _ = say(c, text)
        out = approve(c, ev)
        full = c.req("GET", f"/api/v1/employees/{emp['id']}")
        ov = [o for o in full.get("components") or [] if o["component_id"] == travel]
        nochange = any("تغییر نکرد" in json.dumps(d, ensure_ascii=False) for _, d in ev)
        ok = len(ov) == 1 and float(ov[0]["rate"]) == rial and full["base_salary"] == emp["base_salary"] and (L.last(out, "result") or nochange)
        res["employee"][0 if ok else 1] += 1
        if not ok:
            fails.append({"text": text, "expected": rial, "got": ov, "events": [e for e, _ in ev][-4:]})
        print(("PASS" if ok else "FAIL"), "employee", text, "→", rial, [o["rate"] for o in ov])
    print(json.dumps({"results": res, "failures": fails}, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
