#!/usr/bin/env node
// Reliability-audit E2E matrix (2026-10-08) on the THROWAWAY test stack: every
// scenario is typed into the real widget, answered and approved by clicking its
// real cards, carried out by the real assistant (real model) either on the real
// screen (virtual pointer) or directly, and then checked against state READ BACK
// from the accounting API — never against the assistant's own words.
//
//   node scripts/e2e-audit.mjs [filter] [--repeat N]
//
// Writes test-artifacts/e2e-audit/<run>/results.json (+ a screenshot per scenario).
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { execSync } from 'node:child_process';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const FIX = path.join(root, 'test', 'fixtures');
const APP = process.env.APP_URL ?? 'http://127.0.0.1:18085';
const args = process.argv.slice(2);
const filter = args.find((a) => !a.startsWith('--')) ?? '';
const repeat = Number((args.find((a) => a.startsWith('--repeat=')) ?? '--repeat=1').split('=')[1]);
const runId = new Date().toISOString().replace(/[:.]/g, '-');
const out = path.join(root, 'test-artifacts', 'e2e-audit', runId);
fs.mkdirSync(out, { recursive: true });

// phrases that must never be the assistant's answer (lost workflow / generic failure)
const LOST = [/گم کردم/, /مسیر را/, /راه مطمئنی/, /something went wrong/i, /don't know where/i, /lost track/i];

async function api(page, method, p, body) {
  return page.evaluate(async ([method, p, body]) => {
    const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
    const r = await fetch(p, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': m ? decodeURIComponent(m[1]) : '' }, body: body ? JSON.stringify(body) : undefined });
    const t = await r.text();
    try { return t ? JSON.parse(t) : null; } catch { return t; }
  }, [method, p, body]);
}
const items = (x) => (Array.isArray(x) ? x : x?.items ?? []);
const W = '#tafakor-ai-host >> ';

async function busy(page) {
  return page.evaluate(() => !!document.getElementById('tafakor-ai-host')?.shadowRoot?.querySelector('.orb.busy'));
}
let IGNORE = new Set();
async function waitIdle(page, timeout = 240000) {
  const t0 = Date.now();
  await page.waitForTimeout(800);
  let quiet = 0;
  while (Date.now() - t0 < timeout) {
    const b = await busy(page);
    const running = b ? true : items(await api(page, 'GET', '/ai-api/tasks')).filter((t) => !IGNORE.has(t.id)).slice(0, 3).some((t) => t.stage === 'ui_running' || t.stage === 'new');
    quiet = running ? 0 : quiet + 1;
    if (quiet >= 3) return true;
    await page.waitForTimeout(600);
  }
  return false;
}
async function transcript(page) {
  return page.evaluate(() => document.getElementById('tafakor-ai-host').shadowRoot.querySelector('.body')?.innerText ?? '');
}
async function openCard(page) {
  // the last card that still has enabled buttons
  return page.evaluate(() => {
    const sr = document.getElementById('tafakor-ai-host').shadowRoot;
    const cards = [...sr.querySelectorAll('.card')].filter((c) => [...c.querySelectorAll('button')].some((b) => !b.disabled));
    const c = cards.pop();
    if (!c) return null;
    return { question: c.getAttribute('role') === 'group', text: c.innerText.slice(0, 600),
      buttons: [...c.querySelectorAll('button:not([disabled])')].map((b) => b.innerText.trim()), hasInput: !!c.querySelector('input[type=text]') };
  });
}
async function clickInCard(page, label) {
  return page.evaluate((label) => {
    const sr = document.getElementById('tafakor-ai-host').shadowRoot;
    const cards = [...sr.querySelectorAll('.card')].filter((c) => [...c.querySelectorAll('button')].some((b) => !b.disabled));
    const c = cards.pop();
    const b = [...(c?.querySelectorAll('button:not([disabled])') ?? [])].find((x) => x.innerText.trim().includes(label));
    if (!b) return false;
    b.click();
    return true;
  }, label);
}
async function typeAnswer(page, text) {
  const inp = page.locator(W + '.card[role="group"] input[type="text"]').last();
  await inp.fill(text);
  await inp.press('Enter');
}
async function send(page, text, file) {
  if (file) {
    await page.setInputFiles(W + 'input[type="file"]', path.join(FIX, file));
    await page.waitForSelector(W + '.chip button', { timeout: 30000 });
  }
  await page.fill(W + 'textarea', text);
  await page.keyboard.press('Enter');
}

// ---------- scenarios ----------
// mode: ui (virtual pointer on the real screen) | api (application services)
// turns: [text, ...]; answers: list of {match, choose|type}; approve: click the confirm button
// check(ctx) → [ok, detail] against read-back state

const S = [];
const add = (s) => S.push(s);
const comp = async (page, name) => (await api(page, 'GET', '/api/v1/payroll/components')).filter((c) => c.name === name);
const empBy = async (page, full) => {
  const e = items(await api(page, 'GET', '/api/v1/employees?page_size=200')).find((x) => x.full_name === full);
  return e ? api(page, 'GET', `/api/v1/employees/${e.id}`) : null;
};

add({ id: 'P1', cat: 'payroll', mode: 'ui', title: 'salary component (exact regression), on screen',
  turns: ['به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان'], approve: true, noQuestion: true,
  check: async ({ page, text }) => {
    const c = await comp(page, 'کمک هزینه سفر');
    const exp = items(await api(page, 'GET', '/api/v1/expenses?page_size=5')).filter((x) => x.description?.includes('سفر'));
    return [c.length === 1 && c[0].rate === '30000000' && c[0].method === 'fixed' && !/چه کسی پرداخت/.test(text) && exp.length === 0,
      `components=${JSON.stringify(c.map((x) => [x.id, x.rate, x.method]))} travelExpenses=${exp.length}`];
  } });
add({ id: 'P2', cat: 'payroll', mode: 'ui', title: 'same component again: no duplicate, no change',
  turns: ['کمک‌هزینه سفر رو به اجزای حقوق اضافه کن 3,000,000 تومان'], approve: false,
  check: async ({ page, text }) => {
    const c = await comp(page, 'کمک هزینه سفر');
    return [c.length === 1 && /تغییر نکرد/.test(text), `count=${c.length}`];
  } });
add({ id: 'P3', cat: 'payroll', mode: 'api', title: 'employee compensation (direct)',
  turns: ['برای کارمند زهرا معتضد کمک هزینه سفر ۴ میلیون تومان ثبت کن'], approve: true, noQuestion: true,
  check: async ({ page }) => {
    const e = await empBy(page, 'زهرا معتضد');
    const c = (await comp(page, 'کمک هزینه سفر'))[0];
    const ov = (e?.components ?? []).find((o) => o.component_id === c?.id);
    return [ov && Number(ov.rate) === 40000000 && e.base_salary === '180000000', `override=${JSON.stringify(ov)} base=${e?.base_salary}`];
  } });
add({ id: 'P4', cat: 'payroll', mode: 'api', title: 'ambiguous employee → one question → new component + override',
  turns: ['برای کارمند رضایی حق جذب ۲ میلیون تومان ثبت کن'], answers: [{ match: /کدام/, choose: 'محمد رضایی' }], approve: true,
  check: async ({ page, questions }) => {
    const e = await empBy(page, 'محمد رضایی');
    const c = (await comp(page, 'حق جذب'))[0];
    const ov = (e?.components ?? []).find((o) => o.component_id === c?.id);
    const ali = await empBy(page, 'علی رضایی');
    return [questions === 1 && c && c.rate === '0' && ov && Number(ov.rate) === 20000000 && !(ali.components ?? []).some((o) => o.component_id === c.id),
      `questions=${questions} comp=${c?.id}/${c?.rate} override=${JSON.stringify(ov)}`];
  } });
add({ id: 'P5', cat: 'payroll+memory', mode: 'api', title: 'update + remember (company config, not personal memory)',
  turns: ['کمک هزینه سفر را به اجزای حقوق اضافه کن ۳۵ میلیون ریال و برای دفعات بعد هم به خاطر بسپار'], approve: true,
  check: async ({ page, text }) => {
    const c = await comp(page, 'کمک هزینه سفر');
    const rules = items(await api(page, 'GET', '/ai-api/rules'));
    return [c.length === 1 && c[0].rate === '35000000' && /تنظیمات حقوق شرکت/.test(text), `rate=${c[0]?.rate} rules=${rules.length}`];
  } });
add({ id: 'P6', cat: 'memory', mode: 'api', title: 'memory-only with nothing to remember',
  turns: ['اینو برای دفعه بعد یادت باشه'], approve: false,
  check: async ({ text }) => [/چیزی برای به خاطر سپردن پیدا نکردم/.test(text), ''] });

add({ id: 'A1', cat: 'accounting', mode: 'api', title: 'expense without amount asks the amount (only)',
  turns: ['هزینه سفر را ثبت کن'], approve: false, cancelAtEnd: true,
  check: async ({ text, firstQuestion }) => [/مبلغ/.test(firstQuestion ?? '') && !/اجزای حقوق/.test(text), `q=${firstQuestion}`] });
add({ id: 'A2', cat: 'accounting', mode: 'ui', title: 'expense paid from cash box, on screen',
  turns: ['۲۱,۰۰۰,۰۰۰ ریال بابت اینترنت شرکت از صندوق مرکزی پرداخت کردیم'], answers: [{ match: /دسته|category/i, choose: 'تلفن و اینترنت' }], approve: true,
  check: async ({ page, before, dupWarned }) => {
    const all = items(await api(page, 'GET', '/api/v1/expenses?page_size=50'));
    const ex = all.filter((x) => x.id > (before.maxExpense ?? 0));
    // a repeated run must be stopped by the duplicate warning (an identical expense exists)
    const prior = all.filter((x) => Number(x.amount) === 21000000 && x.status !== 'cancelled' && x.id <= (before.maxExpense ?? 0));
    return [(ex.length === 1 && Number(ex[0].amount) === 21000000) || (ex.length === 0 && dupWarned && prior.length >= 1),
      JSON.stringify(ex.map((x) => [x.id, x.amount, x.treasury_account_name, x.status])) + ` dupWarned=${dupWarned}`];
  } });
add({ id: 'A3', cat: 'accounting', mode: 'api', title: 'receipt from customer into Parsian bank',
  turns: ['۱۲ میلیون تومان از شرکت داده‌پردازان سپهر دریافت کردیم و به حساب بانک پارسیان — جاری ۸۸۱۲ واریز شد'], approve: true,
  answers: [{ match: /حساب|account/i, choose: 'پارسیان' }],
  check: async ({ page, before, dupWarned }) => {
    const all = items(await api(page, 'GET', '/api/v1/treasury/transactions?page_size=50'));
    const tx = all.filter((x) => x.id > before.maxTx);
    const prior = all.filter((x) => x.id <= before.maxTx && x.kind === 'receipt' && Number(x.amount) === 120000000 && x.status !== 'cancelled');
    return [(tx.length === 1 && Number(tx[0].amount) === 120000000 && tx[0].kind === 'receipt') || (tx.length === 0 && dupWarned && prior.length >= 1), JSON.stringify(tx.map((x) => [x.id, x.kind, x.amount, x.party_name, x.treasury_account_name]))];
  } });
add({ id: 'A4', cat: 'accounting', mode: 'api', title: 'transfer between own accounts',
  turns: ['۵ میلیون تومان از صندوق مرکزی به بانک ملت جاری منتقل کردیم'], approve: true,
  check: async ({ page, before }) => {
    const tx = items(await api(page, 'GET', '/api/v1/treasury/transactions?page_size=20')).filter((x) => x.id > before.maxTx);
    return [tx.length === 1 && tx[0].kind === 'transfer' && Number(tx[0].amount) === 50000000, JSON.stringify(tx.map((x) => [x.kind, x.amount, x.treasury_account_name]))];
  } });
add({ id: 'A5', cat: 'accounting', mode: 'api', title: 'duplicate counterparty (two «ایرانیکارت») must be asked, not guessed',
  turns: ['۳ میلیون تومان به ایرانیکارت پرداخت کردیم'], approve: false, cancelAtEnd: true,
  answers: [{ match: /چه کسی پرداخت/, choose: 'کارت یا حساب بانکی شرکت' }, { match: /کدام حساب/, choose: 'بانک ملت جاری' }],
  check: async ({ text, questionsText }) => [/ایرانیکارت/.test(questionsText) && /کدام|which/i.test(questionsText), questionsText.slice(0, 200)] });

add({ id: 'C1', cat: 'crm', mode: 'ui', title: 'create customer on screen',
  turns: ['یک مشتری جدید به نام شرکت آرمان تجارت کیش بساز'], approve: true,
  check: async ({ page, before, dupWarned }) => {
    const p = items(await api(page, 'GET', '/api/v1/parties?q=' + encodeURIComponent('آرمان تجارت کیش')));
    return [p.length >= 1 && p[0].is_customer && (p.length === 1 || dupWarned) && p.length <= before.c1, JSON.stringify(p.map((x) => [x.id, x.name, x.is_customer])) + ` dupWarned=${dupWarned}`];
  } });
add({ id: 'C2', cat: 'crm', mode: 'api', title: 'creating an existing supplier warns about the duplicate',
  turns: ['شرکت پارس نوآور را به عنوان تامین‌کننده بساز'], approve: false, cancelAtEnd: true,
  check: async ({ page, text }) => {
    const p = items(await api(page, 'GET', '/api/v1/parties?q=' + encodeURIComponent('پارس نوآور')));
    return [p.length === 1 && /(وجود دارد|already|تکراری|قبلاً|از قبل|ثبت شده است)/.test(text), `parties=${p.length}`];
  } });

add({ id: 'K1', cat: 'checks', mode: 'api', title: 'issued check',
  turns: ['یک چک صادره به مبلغ ۸۵۰ میلیون ریال در وجه شرکت پارس نوآور با سررسید ۱۴۰۵/۰۹/۳۰ و شماره ۴۴۸۹۱۲ از حساب بانک ملت جاری ثبت کن'], approve: true,
  check: async ({ page }) => {
    const c = items(await api(page, 'GET', '/api/v1/checks?page_size=20'));
    const k = c.filter((x) => String(x.check_number ?? '').includes('448912') || String(x.serial_number ?? '').includes('448912'));
    return [k.length === 1 && Number(k[0].amount) === 850000000 && k[0].due_date?.startsWith('2026-12-21'), JSON.stringify(k.map((x) => [x.id, x.check_type, x.amount, x.due_date, x.party_name]))];
  } });
add({ id: 'K2', cat: 'checks', mode: 'api', title: 'check without amount → asks the amount',
  turns: ['یک چک دریافتی از شرکت داده‌پردازان سپهر با سررسید ۱۴۰۵/۱۰/۱۵ ثبت کن'], approve: false, cancelAtEnd: true,
  check: async ({ questionsText }) => [/مبلغ/.test(questionsText), questionsText.slice(0, 200)] });
add({ id: 'K3', cat: 'checks', mode: 'api', title: 'duplicate check number is not registered twice',
  turns: ['یک چک صادره به مبلغ ۸۵۰ میلیون ریال در وجه شرکت پارس نوآور با سررسید ۱۴۰۵/۰۹/۳۰ و شماره ۴۴۸۹۱۲ از حساب بانک ملت جاری ثبت کن'], approve: false, cancelAtEnd: true,
  check: async ({ page, text }) => {
    const k = items(await api(page, 'GET', '/api/v1/checks?page_size=50')).filter((x) => String(x.check_number ?? '').includes('448912') || String(x.serial_number ?? '').includes('448912'));
    return [k.length <= 1 && /(تکراری|وجود دارد|duplicate|already)/i.test(text), `checks with serial=${k.length}`];
  } });
add({ id: 'K4', cat: 'checks', mode: 'api', title: 'invalid date is not accepted',
  turns: ['یک چک دریافتی ۱۵ میلیون تومانی از شرکت داده‌پردازان سپهر با سررسید ۱۴۰۵/۱۳/۴۰ ثبت کن'], approve: false, cancelAtEnd: true,
  check: async ({ page, before, text }) => {
    const k = items(await api(page, 'GET', '/api/v1/checks?page_size=50'));
    return [k.length === before.checks && /(تاریخ|date)/.test(text), `checks ${before.checks}→${k.length}`];
  } });

add({ id: 'F1', cat: 'configuration', mode: 'ui', title: 'new cash box on screen',
  turns: ['یک صندوق جدید به نام صندوق شعبه کرج بساز'], approve: true,
  check: async ({ page, before, dupWarned }) => {
    const a = items(await api(page, 'GET', '/api/v1/treasury/accounts')).filter((x) => x.name === 'صندوق شعبه کرج');
    return [a.length >= 1 && a[0].kind === 'cash' && (a.length === 1 || dupWarned) && a.length <= Math.max(1, before.f1), JSON.stringify(a.map((x) => [x.id, x.kind])) + ` dupWarned=${dupWarned}`];
  } });
add({ id: 'E1', cat: 'payroll', mode: 'api', title: 'new employee',
  turns: ['کارمند جدید ثبت کن: نرگس احمدی، کد پرسنلی E104، حقوق پایه ۹۵ میلیون ریال، تاریخ استخدام ۱۴۰۵/۰۷/۰۱'], approve: true,
  check: async ({ page, dupWarned }) => {
    const e = await empBy(page, 'نرگس احمدی');
    const count = items(await api(page, 'GET', '/api/v1/employees?page_size=200')).filter((x) => x.code === 'E104').length;
    if (count !== 1) return [false, `E104 records=${count}`];
    return [e && e.code === 'E104' && Number(e.base_salary) === 95000000 && e.hire_date?.startsWith('2026-09-23'), JSON.stringify(e && [e.code, e.base_salary, e.hire_date])];
  } });

add({ id: 'Q1', cat: 'report', mode: 'api', title: 'counterparty balance',
  turns: ['مانده حساب شرکت داده‌پردازان سپهر چقدر است؟'], approve: false,
  check: async ({ page, text }) => {
    const p = items(await api(page, 'GET', '/api/v1/parties?q=' + encodeURIComponent('داده‌پردازان سپهر')))[0];
    const bal = Math.abs(Number(p?.balance ?? 0));
    const fa = (n) => n.toLocaleString('fa-IR');
    return [text.includes(bal.toLocaleString('en-US')) || text.includes(fa(bal)) || text.includes(String(bal)), `balance=${p?.balance}`];
  } });
add({ id: 'Q2', cat: 'report', mode: 'api', title: 'this month expenses',
  turns: ['این ماه چقدر هزینه داشتیم؟'], approve: false,
  check: async ({ text }) => [/[0-9۰-۹]/.test(text) && !/خطا/.test(text), ''] });

// documents: the card must show the document's real total (computed from the fixture)
const docCase = (id, file, text, total, extra = {}) => add({ id, cat: 'documents', mode: 'api', title: `document ${file}`, file, turns: [text],
  approve: false, cancelAtEnd: true, ...extra,
  check: async ({ text: tr }) => {
    const plain = tr.replace(/[٬,]/g, '').replace(/[۰-۹]/g, (d) => '۰۱۲۳۴۵۶۷۸۹'.indexOf(d));
    return [plain.includes(String(total)), `expected total ${total}`];
  } });
docCase('D1', 'invoices/01-clean-digital.pdf', 'این فاکتور خرید را ثبت کن', 917400000, { answers: [{ match: /تاریخ فاکتور/, type: '۱۴۰۵/۰۷/۱۴' }] });
docCase('D2', 'invoices/02-persian-digits.png', 'این فاکتور خرید است، ثبتش کن', 917400000);
docCase('D3', 'invoices/03-tax-discount-multiline.png', 'فاکتور خرید ملزومات اداری را ثبت کن', 126500000);
docCase('D4', 'invoices/04-scanned.jpg', 'این فاکتور خرید اسکن‌شده را ثبت کن', 917400000);
docCase('D5', 'invoices/05-low-quality.jpg', 'فاکتور خرید را ثبت کن', 126500000);
docCase('D6', 'invoices/06-rotated.png', 'فاکتور خرید را ثبت کن', 917400000);
docCase('D7', 'invoices/07-ambiguous-ocr.jpg', 'این فاکتور خرید را ثبت کن', 18865000);
add({ id: 'D8', cat: 'documents', mode: 'api', title: 'invoice with missing date/total asks instead of inventing', file: 'invoices/08-missing-fields.png',
  turns: ['این فاکتور تعمیر خودرو را به عنوان هزینه ثبت کن'], approve: false, cancelAtEnd: true,
  check: async ({ questionsText, text }) => [/(تاریخ|مبلغ|date|amount)/.test(questionsText) || /16,000,000|۱۶٬۰۰۰٬۰۰۰|16000000/.test(text), questionsText.slice(0, 160)] });
docCase('D9', 'receipts/01-pos-receipt.png', 'این رسید پرداخت را به عنوان هزینه ثبت کن', 21000000);
docCase('D10', 'receipts/02-bank-transfer-parsian.png', 'این واریز به زهرا معتضد بابت مساعده حقوق است؛ ثبتش کن', 2232000);
docCase('D11', 'checks/01-sayad-check.png', 'این چک صادره را ثبت کن', 850000000);
docCase('D12', 'receipts/03-payment-receipt.pdf', 'این رسید پیش‌پرداخت به محمد رضایی را ثبت کن', 12000000);

// ---------- runner ----------

async function snapshot(page) {
  const max = (xs) => Math.max(0, ...xs.map((x) => x.id));
  return {
    maxTx: max(items(await api(page, 'GET', '/api/v1/treasury/transactions?page_size=100'))),
    maxExpense: max(items(await api(page, 'GET', '/api/v1/expenses?page_size=100'))),
    checks: items(await api(page, 'GET', '/api/v1/checks?page_size=100')).length,
    c1: Math.max(1, items(await api(page, 'GET', '/api/v1/parties?q=' + encodeURIComponent('آرمان تجارت کیش'))).length),
    f1: items(await api(page, 'GET', '/api/v1/treasury/accounts')).filter((x) => x.name === 'صندوق شعبه کرج').length,
    tasks: new Set(items(await api(page, 'GET', '/ai-api/tasks')).map((t) => t.id)),
  };
}

async function runScenario(page, s, iter) {
  const t0 = Date.now();
  await api(page, 'PUT', '/ai-api/settings', { auto_mode: false, exec_preference: s.mode === 'ui' ? 'human_ui' : 'direct' });
  await page.goto(APP + '/');
  await page.waitForSelector(W + '.orb', { timeout: 30000 });
  await page.evaluate(() => { try { localStorage.setItem('tafakor-ai.speed', 'normal'); sessionStorage.clear(); } catch {} });
  if (!(await page.locator(W + '.panel').isVisible().catch(() => false))) await page.click(W + '.orb');
  await page.click(W + `button[aria-label="درخواست جدید"]`).catch(() => undefined);
  const before = await snapshot(page);
  IGNORE = before.tasks;
  let questions = 0, firstQuestion = null, questionsText = '', approved = false, dupWarned = false, log = [];
  for (const [i, turn] of s.turns.entries()) {
    await send(page, turn, i === 0 ? s.file : undefined);
    await waitIdle(page);
    for (let k = 0; k < 6; k++) {
      const card = await openCard(page);
      if (!card) break;
      if (card.question) {
        questions++;
        firstQuestion ??= card.text;
        questionsText += card.text + ' | ';
        const a = (s.answers ?? []).find((x) => x.match.test(card.text));
        if (!a) { log.push('unanswered question: ' + card.text.slice(0, 200)); break; }
        if (a.choose && (await clickInCard(page, a.choose))) { /* chosen */ } else await typeAnswer(page, a.type ?? a.choose);
        await waitIdle(page);
        continue;
      }
      if (s.approve && !approved) {
        if (card.buttons.includes('در هر صورت ثبت شود') || card.buttons.includes('با این حال ثبت کن')) { dupWarned = true; log.push('duplicate warned: not overridden'); break; }
        const btn = card.buttons.find((b) => b === 'تایید و ثبت');
        if (!btn) { log.push('no confirm button: ' + card.buttons.join(',')); break; }
        approved = await clickInCard(page, btn);
        await waitIdle(page, 300000);
        continue;
      }
      break;
    }
  }
  if (s.cancelAtEnd) {
    const card = await openCard(page);
    if (card && card.buttons.includes('انصراف')) await clickInCard(page, 'انصراف');
  }
  const text = await transcript(page);
  await page.screenshot({ path: path.join(out, `${s.id}-${iter}.png`) }).catch(() => undefined);
  const tasks = items(await api(page, 'GET', '/ai-api/tasks')).filter((t) => !before.tasks.has(t.id));
  const traces = [];
  for (const t of tasks) traces.push(await api(page, 'GET', `/ai-api/tasks/${t.id}/trace`));
  let ok = false, detail = '';
  try { [ok, detail] = await s.check({ page, text, before, questions, firstQuestion, questionsText, dupWarned }); } catch (e) { detail = 'check error: ' + e.message; }
  const lost = LOST.filter((r) => r.test(text)).map(String);
  if (s.noQuestion && questions > 0) { ok = false; detail += ` unexpected question: ${questionsText.slice(0, 150)}`; }
  if (lost.length) { ok = false; detail += ` LOST-PHRASE ${lost}`; }
  const res = { id: s.id, iter, cat: s.cat, mode: s.mode, title: s.title, ok, detail, questions, approved, ms: Date.now() - t0, log,
    tail: text.slice(-700), tasks: traces.map((tr) => ({ id: tr?.task_id, stage: tr?.stage, decision: tr?.decision?.domain, counters: tr?.counters,
      plan: tr?.plan && { skill: tr.plan.skill, steps: tr.plan.steps?.map((x) => [x.tool, x.status, x.verified, x.mismatches]) } })) };
  console.log(`${ok ? 'PASS' : 'FAIL'} ${s.id}#${iter} [${s.cat}/${s.mode}] ${s.title} — ${detail} (${Math.round(res.ms / 1000)}s)${log.length ? ' ' + log.join('; ') : ''}`);
  return res;
}

async function main() {
  const browser = await chromium.launch({ executablePath: process.env.CHROME ?? '/usr/bin/google-chrome', args: ['--no-sandbox'] });
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 860 }, locale: 'fa-IR' });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => console.log('pageerror:', e.message));
  await page.goto(APP + '/login');
  await api(page, 'POST', '/api/v1/auth/login', { email: 'admin@demo.test', password: 'Demo-Passw0rd!' });
  const results = [];
  const list = S.filter((s) => !filter || filter.split(',').some((f) => s.id === f || s.id.startsWith(f) || s.cat === f));
  for (let i = 1; i <= repeat; i++) {
    for (const s of list) {
      try { results.push(await runScenario(page, s, i)); } catch (e) {
        console.log(`FAIL ${s.id}#${i} exception ${e.message}`);
        results.push({ id: s.id, iter: i, cat: s.cat, ok: false, detail: 'exception ' + e.message });
      }
      fs.writeFileSync(path.join(out, 'results.json'), JSON.stringify(results, null, 1));
    }
  }
  await browser.close();
  const failed = results.filter((r) => !r.ok);
  console.log(`\n${results.length - failed.length}/${results.length} passed. Results: ${path.join(out, 'results.json')}`);
  try { execSync('true'); } catch {}
}

main();
