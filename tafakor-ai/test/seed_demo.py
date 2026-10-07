#!/usr/bin/env python3
"""Seeds a DEMO company into the throwaway test stack (never a real database).

Creates the company via the setup wizard API, then demo parties, a project cost
center, cash/bank accounts and a restricted user. Idempotent enough to re-run.
Usage: seed_demo.py [base_url]   (default http://127.0.0.1:18085)
"""
import json
import sys
import urllib.request
import urllib.error
import http.cookiejar
import datetime

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:18085").rstrip("/")
ADMIN = {"email": "admin@demo.test", "password": "Demo-Passw0rd!"}
VIEWER = {"email": "viewer@demo.test", "password": "Viewer-Passw0rd!"}


class Session:
    def __init__(self):
        self.jar = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))

    def csrf(self):
        for c in self.jar:
            if c.name == "tp_csrf":
                return c.value
        return ""

    def req(self, method, path, body=None, raw=False):
        data = json.dumps(body).encode() if body is not None else None
        url = path if path.startswith("http") else BASE + "/api/v1" + path
        r = urllib.request.Request(url, data=data, method=method)
        r.add_header("Accept", "application/json")
        if data is not None:
            r.add_header("Content-Type", "application/json")
        if method not in ("GET", "HEAD"):
            r.add_header("X-CSRF-Token", self.csrf())
        try:
            with self.op.open(r, timeout=60) as resp:
                txt = resp.read()
                return json.loads(txt) if txt and not raw else txt
        except urllib.error.HTTPError as e:
            raise RuntimeError(f"{method} {path} -> {e.code} {e.read()[:300]!r}")

    def login(self, who):
        self.req("POST", "/auth/login", who)
        return self


def main():
    s = Session()
    if s.req("GET", "/setup/status")["needs_setup"]:
        today = datetime.date.today()
        s.req("POST", "/setup", {
            "company": {"name": "شرکت نمونه آزمایشی", "name_en": "Demo Test Co", "base_currency": "IRR",
                        "amount_decimals": 0, "calendar": "jalali"},
            "fiscal_year": {"name": f"FY {today.year}", "start_date": f"{today.year}-01-01", "end_date": f"{today.year}-12-31"},
            "seed_chart": True,
            "admin": {"full_name": "Demo Admin", **ADMIN},
            "language": "fa",
        })
        print("company created")
    s.login(ADMIN)

    def find_party(name):
        page = s.req("GET", "/parties?q=" + urllib.parse.quote(name) + "&page_size=50")
        return [p for p in (page.get("items") or []) if p["name"] == name]

    import urllib.parse
    parties = [
        {"name": "محمد تفکری", "kind": "person", "is_other": True, "mobile": "09120000000"},
        {"name": "شرکت ایکس", "kind": "company", "is_customer": True},
        {"name": "Company X", "kind": "company", "is_customer": True, "notes": "English-named customer"},
        {"name": "علی رضایی", "kind": "person", "is_supplier": True, "code": "AR1"},
        {"name": "علی رضایی", "kind": "person", "is_customer": True, "code": "AR2"},
        {"name": "NordVPN", "kind": "company", "is_supplier": True},
    ]
    for p in parties:
        existing = find_party(p["name"])
        if p.get("code"):
            existing = [e for e in existing if e["code"] == p["code"]]
        if not existing:
            s.req("POST", "/parties", p)
    print("parties ok")

    ccs = s.req("GET", "/cost-centers")
    ccs = ccs.get("items", ccs) if isinstance(ccs, dict) else ccs
    if not any(c["code"] == "PRJ-WEB" for c in ccs):
        s.req("POST", "/cost-centers", {"code": "PRJ-WEB", "kind": "project", "name": "پروژه وب‌سایت", "name_en": "Website project"})
    print("project ok")

    tas = s.req("GET", "/treasury/accounts")
    names = {t["name"] for t in tas}
    if "صندوق مرکزی" not in names:
        s.req("POST", "/treasury/accounts", {"kind": "cash", "name": "صندوق مرکزی", "opening_balance": "500000000"})
    if "بانک ملت جاری" not in names:
        s.req("POST", "/treasury/accounts", {"kind": "bank", "name": "بانک ملت جاری", "bank_name": "ملت", "account_number": "1234567890",
                                             "card_number": "6104337812345678", "opening_balance": "2000000000"})
    print("treasury ok")

    users = s.req("GET", "/users")
    users = users.get("items", users) if isinstance(users, dict) else users
    if not any(u["email"] == VIEWER["email"] for u in users):
        roles = s.req("GET", "/roles")
        roles = roles.get("items", roles) if isinstance(roles, dict) else roles
        viewer = next((r for r in roles if r["code"] == "viewer"), roles[-1])
        s.req("POST", "/users", {"full_name": "Demo Viewer", "email": VIEWER["email"], "password": VIEWER["password"],
                                 "role_id": viewer["id"], "language": "en", "is_active": True})
    print("viewer ok")
    print(json.dumps({"base": BASE, "admin": ADMIN, "viewer": VIEWER}))


if __name__ == "__main__":
    main()
