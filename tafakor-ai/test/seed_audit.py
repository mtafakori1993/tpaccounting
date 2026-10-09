#!/usr/bin/env python3
"""Seeds the reliability-audit dataset into the THROWAWAY demo company (idempotent):
employees, suppliers/customers, a bank account and a cash box with realistic
Persian names. Also repairs the employee whose code an earlier assistant build
stored as the literal "$generate" (bug found by the audit, 2026-10-08).
Usage: seed_audit.py [base_url]
"""
import sys
from seed_demo import Session, ADMIN, BASE  # noqa: F401 (BASE from argv)

EMPLOYEES = [
    ("E101", "زهرا", "معتضد", "0012345679", "09121234567", "180000000"),
    ("E102", "محمد", "رضایی", "0023456787", "09351112233", "150000000"),
    ("E103", "علی", "رضایی", "0034567895", "09199887766", "135000000"),
]
PARTIES = [
    ("شرکت پارس نوآور", "company", {"is_supplier": True}),
    ("فروشگاه لوازم اداری آریا", "company", {"is_supplier": True}),
    ("تعمیرگاه خودرو صادقی", "company", {"is_supplier": True}),
    ("شرکت داده‌پردازان سپهر", "company", {"is_customer": True}),
]


def main():
    s = Session().login(ADMIN)
    emps = s.req("GET", "/employees?page_size=200")["items"]
    for e in emps:
        if e["code"] == "$generate":
            full = s.req("GET", f"/employees/{e['id']}")
            full["code"] = "E001"
            s.req("PUT", f"/employees/{e['id']}", full)
            print("repaired employee code", e["full_name"], "→ E001")
    have = {e["code"] for e in s.req("GET", "/employees?page_size=200")["items"]}
    for code, first, last, nid, mobile, base in EMPLOYEES:
        if code in have:
            continue
        s.req("POST", "/employees", {"code": code, "first_name": first, "last_name": last, "national_id": nid, "mobile": mobile,
                                     "employment_type": "full_time", "marital_status": "married", "hire_date": "2025-03-21",
                                     "base_salary": base, "children_count": 1})
        print("employee", first, last)
    names = {p["name"] for p in s.req("GET", "/parties?page_size=200")["items"]}
    for name, kind, flags in PARTIES:
        if name not in names:
            s.req("POST", "/parties", {"name": name, "kind": kind, **flags})
            print("party", name)
    accts = s.req("GET", "/treasury/accounts")
    accts = accts.get("items", accts) if isinstance(accts, dict) else accts
    an = {a["name"] for a in accts}
    if "بانک پارسیان — جاری ۸۸۱۲" not in an:
        s.req("POST", "/treasury/accounts", {"kind": "bank", "name": "بانک پارسیان — جاری ۸۸۱۲", "bank_name": "بانک پارسیان",
                                             "account_number": "47000881200001", "opening_balance": "0"})
        print("bank account")
    if "صندوق دفتر مرکزی" not in an:
        s.req("POST", "/treasury/accounts", {"kind": "cash", "name": "صندوق دفتر مرکزی", "opening_balance": "0"})
        print("cash box")


if __name__ == "__main__":
    main()
