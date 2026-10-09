#!/usr/bin/env python3
"""Generates the realistic Persian accounting document fixtures used by the live
document tests (invoices, receipts, checks; clean, scanned, low quality, rotated,
ambiguous, incomplete). HTML is rendered with headless Chrome (PNG + PDF), then
degraded with Pillow. Re-run after editing: python3 generate.py
All companies, people and numbers are fictitious test data.
"""
import os, subprocess, random, tempfile
from PIL import Image, ImageFilter, ImageEnhance, ImageDraw

HERE = os.path.dirname(os.path.abspath(__file__))
CHROME = os.environ.get("CHROME", "google-chrome")
FA = str.maketrans("0123456789", "۰۱۲۳۴۵۶۷۸۹")

CSS = """<style>@font-face{font-family:V;src:local('Vazirmatn'),local('Noto Sans Arabic'),local('DejaVu Sans')}
body{font-family:V,'Noto Sans Arabic',Tahoma,sans-serif;direction:rtl;margin:0;padding:28px;width:760px;background:#fff;color:#1a1a1a;font-size:15px}
h1{font-size:22px;margin:0 0 6px}.muted{color:#555;font-size:13px}.box{border:1px solid #999;padding:10px;margin:10px 0}
table{width:100%;border-collapse:collapse;margin-top:10px}td,th{border:1px solid #888;padding:6px;text-align:center}th{background:#eee}
.tot td{font-weight:bold}.row{display:flex;justify-content:space-between}.stamp{color:#b00;border:3px solid #b00;border-radius:50%;
width:120px;height:120px;display:flex;align-items:center;justify-content:center;transform:rotate(-15deg);font-weight:bold;opacity:.7}</style>"""

def money(n, persian=False):
    s = f"{n:,}"
    return s.translate(FA).replace(",", "٬") if persian else s

def invoice(no, date, seller, buyer, items, discount=0, vat=0.10, persian=False, extra="", hide=()):
    rows, sub = "", 0
    for i, (desc, qty, price) in enumerate(items, 1):
        line = qty * price; sub += line
        rows += f"<tr><td>{str(i).translate(FA) if persian else i}</td><td>{desc}</td><td>{money(qty, persian)}</td><td>{money(price, persian)}</td><td>{money(line, persian)}</td></tr>"
    taxable = sub - discount
    tax = int(round(taxable * vat))
    total = taxable + tax
    d = lambda s: s.translate(FA) if persian else s
    head = f"<h1>صورتحساب فروش کالا و خدمات</h1><div class='row'><span>شماره: {d(no)}</span>" + ("" if "date" in hide else f"<span>تاریخ: {d(date)}</span>") + "</div>"
    parties = f"<div class='box'><b>فروشنده:</b> {seller['name']} — شناسه ملی {d(seller['nid'])} — {seller['addr']}<br><b>خریدار:</b> {buyer['name']} — شناسه ملی {d(buyer['nid'])}</div>"
    tbl = f"<table><tr><th>ردیف</th><th>شرح کالا / خدمات</th><th>تعداد</th><th>مبلغ واحد (ریال)</th><th>مبلغ کل (ریال)</th></tr>{rows}"
    tbl += f"<tr class='tot'><td colspan=4>جمع</td><td>{money(sub, persian)}</td></tr>"
    if discount:
        tbl += f"<tr class='tot'><td colspan=4>تخفیف</td><td>{money(discount, persian)}</td></tr>"
    if vat:
        tbl += f"<tr class='tot'><td colspan=4>مالیات و عوارض ارزش افزوده ({d(str(int(vat*100)))}٪)</td><td>{money(tax, persian)}</td></tr>"
    if "total" not in hide:
        tbl += f"<tr class='tot'><td colspan=4>مبلغ قابل پرداخت (ریال)</td><td>{money(total, persian)}</td></tr>"
    tbl += "</table>"
    return CSS + head + parties + tbl + extra, total

PARS = {"name": "شرکت پارس نوآور", "nid": "10320845671", "addr": "تهران، خیابان ولیعصر، پلاک ۲۱۴"}
DEMO = {"name": "شرکت نمونه آزمایشی", "nid": "14009876543"}
ITEMS3 = [("لپ‌تاپ ایسوس مدل ExpertBook B1", 2, 385000000), ("ماوس بی‌سیم لاجیتک M185", 2, 9500000), ("نصب و راه‌اندازی شبکه", 1, 45000000)]
ITEMS6 = [("کاغذ A4 کپی‌مکس (بسته ۵۰۰ برگی)", 20, 1850000), ("تونر HP 85A", 3, 14200000), ("خودکار بیک آبی (جعبه)", 5, 2100000),
          ("پوشه کاغذی", 100, 45000), ("منگنه کانگرو", 4, 3350000), ("سرویس ماهانه نگهداری چاپگر", 1, 12000000)]

DOCS = {}
DOCS["invoices/01-clean-digital"], _ = invoice("1405-0712", "1405/07/14", PARS, DEMO, ITEMS3)
DOCS["invoices/02-persian-digits"], _ = invoice("1405-0713", "1405/07/15", PARS, DEMO, ITEMS3, persian=True)
DOCS["invoices/03-tax-discount-multiline"], _ = invoice("ف-۸۸۲۰", "1405/07/10", {"name": "فروشگاه لوازم اداری آریا", "nid": "10861234508", "addr": "اصفهان، خیابان چهارباغ"}, DEMO, ITEMS6, discount=5000000)
DOCS["invoices/04-scanned"] = DOCS["invoices/01-clean-digital"]
DOCS["invoices/05-low-quality"] = DOCS["invoices/03-tax-discount-multiline"]
DOCS["invoices/06-rotated"] = DOCS["invoices/02-persian-digits"]
DOCS["invoices/07-ambiguous-ocr"], _ = invoice("1405-0719", "1405/07/18", PARS, DEMO, [("قرارداد پشتیبانی نرم‌افزار — مهر ۱۴۰۵", 1, 17150000)],
    extra="<div class='row' style='margin-top:-58px'><span></span><div class='stamp'>پرداخت شد</div></div>")
DOCS["invoices/08-missing-fields"], _ = invoice("بدون شماره", "", {"name": "تعمیرگاه خودرو صادقی", "nid": "—", "addr": "کرج"}, DEMO,
    [("تعویض روغن و فیلتر", 1, 6800000), ("لنت ترمز جلو", 1, 9200000)], vat=0, hide=("date", "total"))
DOCS["invoices/09-english-digits-tax"], _ = invoice("INV-2026-0457", "2026-10-06", {"name": "ایرانیکارت", "nid": "10103456789", "addr": "تهران، میدان آرژانتین"}, DEMO,
    [("شارژ حساب ارزی — خرید اشتراک نرم‌افزار", 1, 21000000)], vat=0.10)

DOCS["receipts/01-pos-receipt"] = CSS.replace("width:760px", "width:340px") + """<h1 style='text-align:center'>ایرانیکارت</h1><div class='muted' style='text-align:center'>رسید پرداخت اینترنتی</div>
<div class='box'>مبلغ: ۲۱٬۰۰۰٬۰۰۰ ریال<br>تاریخ: ۱۴۰۵/۰۷/۱۲ — ساعت ۱۴:۳۲<br>شماره پیگیری: ۸۸۴۲۱۷<br>شماره مرجع: ۵۴۲۲۱۹۸۷۷۰۱۲<br>
کارت پرداخت‌کننده: ۶۰۳۷-۹۹۷۵-****-۴۴۱۲<br>بابت: خرید اشتراک سالانه Notion</div><div class='muted' style='text-align:center'>با تشکر از خرید شما</div>"""
DOCS["receipts/02-bank-transfer-parsian"] = CSS.replace("width:760px", "width:420px") + """<h1>بانک پارسیان</h1><div class='muted'>رسید انتقال وجه (کارت به کارت)</div>
<div class='box'>مبلغ: 2,232,000 ریال<br>از کارت: 6221-06**-****-7781 (شرکت نمونه آزمایشی)<br>به کارت: 6037-99**-****-1209 — زهرا معتضد<br>
تاریخ: 1405/07/16 10:05<br>شماره پیگیری: 330198<br>وضعیت: موفق</div>"""
DOCS["receipts/03-payment-receipt"] = CSS + """<h1>رسید دریافت وجه</h1><div class='row'><span>شماره: ۴۵۷</span><span>تاریخ: ۱۴۰۵/۰۷/۲۰</span></div>
<div class='box'>بدین‌وسیله گواهی می‌شود مبلغ <b>۱۲٬۰۰۰٬۰۰۰ ریال</b> (دوازده میلیون ریال) بابت پیش‌پرداخت قرارداد طراحی وب‌سایت از
<b>شرکت نمونه آزمایشی</b> به‌صورت واریز به حساب بانک ملت دریافت شد.</div><div class='row'><span>دریافت‌کننده: محمد رضایی</span><span>امضا</span></div>"""
DOCS["checks/01-sayad-check"] = CSS.replace("width:760px", "width:820px") + """<div style='border:2px solid #2b5;padding:16px;background:#f4fbf6'>
<div class='row'><b>بانک ملت — شعبه مرکزی تهران</b><span>شناسه صیادی: ۷۸۲۰۱۴۵۵۶۰۰۹۳۱۲۷</span></div>
<div class='row' style='margin-top:10px'><span>سری: ۲۳۱/ب &nbsp; سریال: ۴۴۸۹۱۲</span><span>تاریخ سررسید: ۱۴۰۵/۰۹/۳۰</span></div>
<div style='margin-top:14px'>در وجه: <b>شرکت پارس نوآور</b></div>
<div>مبلغ به حروف: هشتصد و پنجاه میلیون ریال</div>
<div class='row'><span>مبلغ: <b>۸۵۰٬۰۰۰٬۰۰۰ ریال</b></span><span>صاحب حساب: شرکت نمونه آزمایشی</span></div>
<div class='muted' style='margin-top:12px'>کد ملی/شناسه صاحب حساب: ۱۴۰۰۹۸۷۶۵۴۳</div></div>"""

def render(html, png, pdf=None):
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, encoding="utf-8") as f:
        f.write("<!doctype html><html lang='fa'><meta charset='utf-8'><body>" + html + "</body></html>")
        src = f.name
    common = [CHROME, "--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars", "--force-device-scale-factor=1"]
    subprocess.run(common + ["--window-size=840,1000", f"--screenshot={png}", "file://" + src], check=True, capture_output=True)
    if pdf:
        subprocess.run(common + ["--no-pdf-header-footer", f"--print-to-pdf={pdf}", "file://" + src], check=True, capture_output=True)
    os.unlink(src)
    im = Image.open(png).convert("RGB")
    # crop the white margin below the content
    bg = Image.new("RGB", im.size, (255, 255, 255))
    from PIL import ImageChops
    box = ImageChops.difference(im, bg).getbbox()
    if box:
        im = im.crop((0, 0, im.width, min(im.height, box[3] + 30)))
    im.save(png)

def scan(path, out, angle=1.6, quality=60):
    random.seed(7)
    im = Image.open(path).convert("L")
    im = im.rotate(angle, expand=True, fillcolor=235)
    d = ImageDraw.Draw(im)
    for _ in range(2500):
        x, y = random.randrange(im.width), random.randrange(im.height)
        d.point((x, y), fill=random.randrange(120, 200))
    im = im.filter(ImageFilter.GaussianBlur(0.8))
    im = ImageEnhance.Contrast(im).enhance(0.85)
    im.save(out, "JPEG", quality=quality)

def lowq(path, out):
    im = Image.open(path).convert("RGB")
    im = im.resize((im.width // 2, im.height // 2))
    im.save(out, "JPEG", quality=18)

def main():
    for name, html in DOCS.items():
        png = os.path.join(HERE, name + ".png")
        pdf = os.path.join(HERE, name + ".pdf") if name in ("invoices/01-clean-digital", "invoices/03-tax-discount-multiline", "receipts/03-payment-receipt") else None
        render(html, png, pdf)
    inv = lambda n: os.path.join(HERE, "invoices", n)
    scan(inv("04-scanned.png"), inv("04-scanned.jpg")); os.unlink(inv("04-scanned.png"))
    lowq(inv("05-low-quality.png"), inv("05-low-quality.jpg")); os.unlink(inv("05-low-quality.png"))
    im = Image.open(inv("06-rotated.png")); im.rotate(90, expand=True).save(inv("06-rotated.png"))
    scan(inv("07-ambiguous-ocr.png"), inv("07-ambiguous-ocr.jpg"), angle=-2.5, quality=35); os.unlink(inv("07-ambiguous-ocr.png"))
    print("generated", sorted(os.listdir(os.path.join(HERE, "invoices"))), sorted(os.listdir(os.path.join(HERE, "receipts"))), os.listdir(os.path.join(HERE, "checks")))

if __name__ == "__main__":
    main()
