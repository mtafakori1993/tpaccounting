#!/usr/bin/env node
// Browser end-to-end test of source documents and record language (D44–D46) on the
// THROWAWAY test stack, with the real model:
//   1. Persian UI + English receipt image → expense; description in Persian; the original
//      image is on the record and the result card's link downloads the same bytes.
//   2. PDF → receipt; the PDF itself is attached.
//   3. File sent alone, instruction in the next message → the record gets the file.
//   4. Multi-step (new counterparty + receipt) → the file goes to the receipt, not the party.
//   5. HUMAN_UI preview: the assistant fills the form, the USER presses Save → the new
//      record is found and the pending file is attached to it.
// Frames go to tafakor-ai/test-artifacts/e2e-attach/.
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const APP = process.env.APP_URL ?? 'http://127.0.0.1:18085';
const out = path.join(root, 'test-artifacts', 'e2e-attach');
const fixtures = path.join(root, 'test', 'fixtures');
fs.mkdirSync(out, { recursive: true });
const only = (process.env.ONLY ?? '').split(',').filter(Boolean);
const run = Date.now().toString().slice(-5);
let frame = 0;
const results = [];
const check = (name, ok, detail = '') => {
  results.push([name, !!ok]);
  console.log(`${ok ? 'PASS' : 'FAIL'}: ${name} ${detail}`);
};

async function api(page, method, p, body) {
  return page.evaluate(async ([method, p, body]) => {
    const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
    const r = await fetch(p, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': m ? decodeURIComponent(m[1]) : '' }, body: body ? JSON.stringify(body) : undefined });
    const t = await r.text();
    try { return t ? JSON.parse(t) : null; } catch { return t; }
  }, [method, p, body]);
}

// downloads a URL as the signed-in user; returns base64 + content type
async function download(page, url) {
  return page.evaluate(async (url) => {
    const r = await fetch(url, { credentials: 'same-origin' });
    const b = new Uint8Array(await r.arrayBuffer());
    let s = '';
    for (const x of b) s += String.fromCharCode(x);
    return { status: r.status, type: r.headers.get('content-type') ?? '', b64: btoa(s) };
  }, url);
}

const snap = (page, name) => page.screenshot({ path: path.join(out, `${String(++frame).padStart(2, '0')}-${name}.png`) });

async function waitIdle(page, timeout = 240000) {
  await page.waitForFunction(() => {
    const host = document.getElementById('tafakor-ai-host');
    return host?.shadowRoot && !host.shadowRoot.querySelector('.orb.busy') && !host.shadowRoot.querySelector('.status .spinner');
  }, null, { timeout });
}

// answers open question cards: options by preferred text, free-text by the first
// matching [pattern, answer] pair (else the first option / a plain "بله")
async function answerQuestions(page, prefs, texts = []) {
  for (let i = 0; i < 8; i++) {
    const card = page.locator('#tafakor-ai-host .card[role="group"]').last();
    if (!(await card.isVisible().catch(() => false))) return;
    const inp = card.locator('.form input:not([disabled])');
    if (await inp.isVisible().catch(() => false)) {
      const q = await card.locator('h4').innerText();
      const hit = texts.find(([re]) => re.test(q));
      if (!hit) { console.log(`  unanswered question: ${q}`); return; }
      console.log(`  answering «${q}» → ${hit[1]}`);
      await inp.fill(hit[1]);
      await card.locator('.actions .btn.primary').click();
      await page.waitForTimeout(500);
      await waitIdle(page);
      continue;
    }
    const enabled = card.locator('.opts button:not([disabled])');
    if ((await enabled.count()) === 0) return;
    const labels = await enabled.allInnerTexts();
    let idx = labels.findIndex((l) => prefs.some((p) => l.includes(p)));
    if (idx < 0) idx = 0;
    console.log(`  answering: ${labels[idx]}`);
    await enabled.nth(idx).click();
    await page.waitForTimeout(500);
    await waitIdle(page);
  }
}

// approves the waiting approval card, if there is one
async function approveIfAsked(page) {
  const card = page.locator('#tafakor-ai-host >> .card:has(.reason:has-text("در انتظار تایید"))').last();
  const btn = card.locator('.actions .btn.primary:not([disabled])');
  if (await btn.isVisible().catch(() => false)) {
    console.log(`  approving: ${await btn.innerText()}`);
    await btn.click();
    await page.waitForTimeout(800);
    await waitIdle(page);
    return true;
  }
  return false;
}

// when the screen cannot express the operation, HUMAN_UI stops and offers to run it
// directly; the user's explicit choice is pressing that button
async function chooseDirectIfOffered(page) {
  const btn = page.locator('#tafakor-ai-host >> .card .btn.primary:not([disabled]):has-text("مستقیم")').last();
  if (await btn.isVisible().catch(() => false)) {
    console.log(`  choosing: ${await btn.innerText()}`);
    await btn.click();
    await page.waitForTimeout(800);
    await waitIdle(page);
    return true;
  }
  return false;
}

// questions, approvals and the explicit direct-run choice until the operation settles
async function settle(page, prefs, texts) {
  for (let i = 0; i < 5; i++) {
    await answerQuestions(page, prefs, texts);
    const approved = await approveIfAsked(page);
    const direct = await chooseDirectIfOffered(page);
    if (!approved && !direct) return;
  }
}

async function send(page, text, file) {
  if (file) {
    await page.setInputFiles('#tafakor-ai-host >> input[type="file"]', file);
    await page.waitForSelector('#tafakor-ai-host >> .chip button', { timeout: 20000 });
  }
  await page.fill('#tafakor-ai-host >> textarea', text);
  await page.keyboard.press('Enter');
  await page.waitForTimeout(1500);
  await waitIdle(page);
}

async function lastResult(page) {
  const card = page.locator('#tafakor-ai-host >> .card:has(.reason)').last();
  const text = await card.innerText().catch(() => '');
  const links = await card.locator('ul.files a').evaluateAll((as) => as.map((a) => ({ href: a.getAttribute('href'), name: a.textContent }))).catch(() => []);
  return { text, links };
}

const maxId = async (page, p) => Math.max(0, ...(((await api(page, 'GET', p))?.items) ?? []).map((x) => x.id));
const attachments = (page, et, id) => api(page, 'GET', `/api/v1/attachments?entity_type=${et}&entity_id=${id}`);
const FREE = [[/نرخ|USD|دلار/, '1000000'], [/حساب|account/i, 'ملت'], [/دسته|category/i, 'اینترنت']];
const b64 = (f) => fs.readFileSync(f).toString('base64');
// Latin words that the source itself contains are allowed in a Persian record
const inventedLatin = (text, allowed) => (text.match(/[A-Za-z][A-Za-z'-]+/g) ?? []).filter((w) => !allowed.some((a) => a.toLowerCase() === w.toLowerCase()));

async function scenario(name, page, fn) {
  if (only.length && !only.includes(name)) return;
  console.log(`\n== ${name}`);
  try {
    // a fresh conversation per scenario (the widget keeps it in sessionStorage)
    await page.evaluate(() => { for (const k of Object.keys(sessionStorage)) if (k.startsWith('tafakor-ai.')) sessionStorage.removeItem(k); }).catch(() => undefined);
    await page.goto(APP + '/');
    await page.waitForSelector('#tafakor-ai-host >> .orb', { timeout: 20000 });
    if (await page.locator('#tafakor-ai-host >> .panel').isHidden().catch(() => true)) await page.click('#tafakor-ai-host >> .orb');
    await fn();
  } catch (e) {
    console.log('ERROR', e.message);
    await snap(page, `${name}-error`).catch(() => undefined);
    results.push([`${name}: no exception`, false]);
  }
}

async function main() {
  const browser = await chromium.launch({ executablePath: process.env.CHROME ?? '/usr/bin/google-chrome', args: ['--no-sandbox'] });
  // a real PDF of a bank receipt, rendered by the browser itself
  const pdfPath = path.join(out, 'receipt-advance.pdf');
  {
    const p = await browser.newPage();
    await p.goto('file://' + path.join(fixtures, 'receipt-advance.html'));
    await p.pdf({ path: pdfPath, width: '480px', height: '640px', printBackground: true });
    await p.close();
  }
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 860 }, recordVideo: { dir: out, size: { width: 1366, height: 860 } } });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => console.log('pageerror:', e.message));
  try {
    await page.goto(APP + '/login');
    await api(page, 'POST', '/api/v1/auth/login', { email: 'admin@demo.test', password: 'Demo-Passw0rd!' });
    const me = await api(page, 'GET', '/api/v1/auth/me');
    check('the user interface is Persian', (me.language ?? 'fa') === 'fa', me.language);
    await api(page, 'PUT', '/ai-api/settings', { auto_mode: false });
    await page.goto(APP + '/');
    await page.evaluate(() => localStorage.setItem('tafakor-ai.exec', JSON.stringify('api')));

    // 1. English receipt image, Persian interface → Persian description, image attached + downloadable
    await scenario('image', page, async () => {
      const before = await maxId(page, '/api/v1/expenses?page_size=100');
      const img = path.join(fixtures, 'receipt-vpn.png');
      await send(page, 'این رسید اشتراک VPN است که با کارت شرکت پرداخت شده؛ به عنوان هزینه مستقیم ثبتش کن', img);
      await settle(page, ['اینترنت', 'نرم', 'کارت', 'ملت'], FREE);
      await snap(page, 'image-result');
      const id = await maxId(page, '/api/v1/expenses?page_size=100');
      check('image: expense registered', id > before, `id ${id}`);
      const exp = await api(page, 'GET', `/api/v1/expenses/${id}`);
      const bad = inventedLatin(exp.description ?? '', ['NordVPN', 'VPN', 'Standard', 'USD', 'Visa', 'NV']);
      check('image: description is Persian', /[؀-ۿ]/.test(exp.description ?? '') && bad.length === 0, `«${exp.description}» ${bad.join(',')}`);
      const att = await attachments(page, 'expense', id);
      check('image: original image attached to the expense', att?.length === 1, JSON.stringify(att?.map((a) => a.file_name)));
      const r = await lastResult(page);
      check('image: result card links the stored file', r.links.length === 1 && r.links[0].href === `/api/v1/attachments/${att?.[0]?.id}/download`, JSON.stringify(r.links));
      if (r.links[0]) {
        const d = await download(page, r.links[0].href);
        check('image: the link downloads the original bytes', d.status === 200 && d.b64 === b64(img), `${d.status} ${d.type}`);
      }
    });

    // 2. PDF → receipt, the PDF attached
    await scenario('pdf', page, async () => {
      const before = await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt');
      await send(page, 'Company X این مبلغ را به عنوان پیش‌پرداخت واریز کرده؛ به عنوان دریافت از مشتری مستقیم ثبتش کن', pdfPath);
      await settle(page, ['ملت', 'Company X', 'پیش'], FREE);
      await snap(page, 'pdf-result');
      const id = await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt');
      check('pdf: receipt registered', id > before, `id ${id}`);
      const att = await attachments(page, 'treasury_transaction', id);
      check('pdf: the PDF is attached', att?.length === 1 && att[0].content_type?.startsWith('application/pdf'), JSON.stringify(att?.map((a) => [a.file_name, a.content_type])));
      if (att?.[0]) {
        const d = await download(page, `/api/v1/attachments/${att[0].id}/download`);
        check('pdf: downloadable and identical', d.status === 200 && d.b64 === b64(pdfPath), d.type);
      }
      const tx = await api(page, 'GET', `/api/v1/treasury/transactions/${id}`);
      check('pdf: description is Persian', !tx.description || inventedLatin(tx.description, ['Company', 'X']).length === 0, `«${tx.description}»`);
    });

    // 3. the file first, the instruction in the next message
    await scenario('carry', page, async () => {
      const before = await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt');
      await send(page, '', path.join(fixtures, 'receipt-loan.png'));
      await snap(page, 'carry-asked');
      check('carry: nothing registered for a bare document', (await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt')) === before);
      await send(page, 'این وام است که از محمد تفکری برای هزینه‌های شرکت گرفتیم؛ ثبتش کن');
      await settle(page, ['وام', 'تسهیلات', 'ملت', 'محمد'], FREE);
      await snap(page, 'carry-result');
      const id = await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt');
      check('carry: registered from the follow-up', id > before, `id ${id}`);
      const att = await attachments(page, 'treasury_transaction', id);
      check('carry: the earlier file is attached', att?.length === 1 && att[0].file_name === 'receipt-loan.png', JSON.stringify(att?.map((a) => a.file_name)));
    });

    // 4. multi-step: a new counterparty, then the receipt — the file goes to the receipt
    await scenario('multistep', page, async () => {
      const name = `شرکت آزمون پیوست ${run}`;
      const before = await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt');
      await send(page, `طرف حساب جدیدی به اسم «${name}» به عنوان مشتری بساز و بعد این واریزی را به عنوان دریافت از همین مشتری به حساب ملت مستقیم ثبت کن`, path.join(fixtures, 'receipt-customer.png'));
      await settle(page, ['ملت', name], FREE);
      await snap(page, 'multistep-result');
      const parties = (await api(page, 'GET', `/api/v1/parties?q=${encodeURIComponent(name)}`))?.items ?? [];
      check('multistep: the counterparty was created', parties.length === 1, `${parties.length}`);
      const id = await maxId(page, '/api/v1/treasury/transactions?page_size=100&kind=receipt');
      check('multistep: the receipt was created', id > before, `id ${id}`);
      const att = await attachments(page, 'treasury_transaction', id);
      check('multistep: file attached to the receipt', att?.length === 1, JSON.stringify(att?.map((a) => a.file_name)));
      if (parties[0]) {
        const pa = await attachments(page, 'party', parties[0].id);
        check('multistep: not attached to the new counterparty', (pa?.length ?? 0) === 0);
      }
      const r = await lastResult(page);
      check('multistep: result card links the file', r.links.length === 1, JSON.stringify(r.links));
    });

    // 5. HUMAN_UI preview: the user presses Save themselves
    await scenario('handsave', page, async () => {
      await page.evaluate(() => localStorage.setItem('tafakor-ai.exec', JSON.stringify('ui')));
      const name = `مشتری دستی ${run}`;
      const img = path.join(fixtures, 'receipt-customer.png');
      await send(page, `یه مشتری به اسم «${name}» با شماره ۰۹۱۲۷۷۷${run.slice(0, 4)} ایجاد کن و این تصویر را فقط به عنوان مدرک پرونده‌اش نگه دار؛ هیچ ثبت مالی انجام نده. روی صفحه نشانم بده`, img);
      await answerQuestions(page, [name]);
      const dlg = page.locator('[role="dialog"][data-ai-dialog]').last();
      await dlg.waitFor({ state: 'visible', timeout: 60000 });
      await page.waitForTimeout(1500);
      await snap(page, 'handsave-filled');
      const typed = (await dlg.locator('input').evaluateAll((xs) => xs.map((x) => x.value))).filter(Boolean).join(' | ');
      check('handsave: the form was filled and left open', typed.includes(name) || typed.includes('0912'), typed);
      check('handsave: nothing registered before the user saves', ((await api(page, 'GET', `/api/v1/parties?q=${encodeURIComponent(name)}`))?.items ?? []).length === 0);
      // the user's own click on the form's save button
      const save = dlg.locator('[data-ai-action="common.save"], .dialog-foot .btn-accent, .dialog-foot button').last();
      await save.click();
      await dlg.waitFor({ state: 'detached', timeout: 30000 }).catch(() => undefined);
      await page.waitForTimeout(1500);
      await waitIdle(page);
      const another = await page.locator('[role="dialog"][data-ai-dialog]').count();
      check('handsave: no further form was opened (attaching is not a form)', another === 0, `${another}`);
      if (await page.locator('#tafakor-ai-host >> .panel').isHidden().catch(() => true)) await page.click('#tafakor-ai-host >> .orb');
      await page.waitForTimeout(800);
      await snap(page, 'handsave-result');
      const parties = (await api(page, 'GET', `/api/v1/parties?q=${encodeURIComponent(name)}`))?.items ?? [];
      check('handsave: exactly one party (saved by the user)', parties.length === 1, `${parties.length}`);
      if (parties[0]) {
        let att = [];
        for (let i = 0; i < 20 && !att?.length; i++) {
          att = await attachments(page, 'party', parties[0].id);
          if (!att?.length) await page.waitForTimeout(1000);
        }
        check('handsave: the pending file was attached to the saved record', att?.length === 1 && att[0].file_name === 'receipt-customer.png', JSON.stringify(att?.map((a) => a.file_name)));
      }
      const r = await lastResult(page);
      check('handsave: the result card reports it with a download link', r.links.length === 1, `${r.text.slice(0, 120)} ${JSON.stringify(r.links)}`);
    });
  } finally {
    await ctx.close();
    await browser.close();
  }
  const failed = results.filter((r) => !r[1]).length;
  console.log(`\n${results.length - failed}/${results.length} passed; artifacts in ${out}`);
  process.exit(failed ? 1 : 0);
}

main();
