#!/usr/bin/env node
// Browser end-to-end test of the widget + UI agent on the THROWAWAY test stack:
// the user uploads a receipt and asks to register the advance payment; after approval
// the virtual cursor opens Receipts, fills the real form and saves; the result shown
// must come from verified state. Frames and a video go to tafakor-ai/test-artifacts/e2e/.
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const APP = process.env.APP_URL ?? 'http://127.0.0.1:18085';
const out = path.join(root, 'test-artifacts', 'e2e');
fs.mkdirSync(out, { recursive: true });
let frame = 0;
const results = [];
const check = (name, ok, detail = '') => {
  results.push([name, ok]);
  console.log(`${ok ? 'PASS' : 'FAIL'}: ${name} ${detail}`);
};

async function api(page, method, p, body) {
  return page.evaluate(async ([method, p, body]) => {
    const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
    const r = await fetch(p, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': m ? decodeURIComponent(m[1]) : '' }, body: body ? JSON.stringify(body) : undefined });
    const t = await r.text();
    return t ? JSON.parse(t) : null;
  }, [method, p, body]);
}

async function snap(page, name) {
  const f = path.join(out, `${String(++frame).padStart(2, '0')}-${name}.png`);
  await page.screenshot({ path: f });
  return f;
}

async function answerQuestions(page, prefs) {
  // answer any open question card using preferred option texts
  for (let i = 0; i < 6; i++) {
    const card = page.locator('#tafakor-ai-host .card[role="group"]').last();
    if (!(await card.isVisible().catch(() => false))) return;
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

async function waitIdle(page, timeout = 240000) {
  await page.waitForFunction(() => {
    const host = document.getElementById('tafakor-ai-host');
    return host?.shadowRoot && !host.shadowRoot.querySelector('.orb.busy');
  }, null, { timeout });
}

async function run() {
  const browser = await chromium.launch({ executablePath: process.env.CHROME ?? '/usr/bin/google-chrome', args: ['--no-sandbox'] });
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 860 }, recordVideo: { dir: out, size: { width: 1366, height: 860 } } });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => console.log('pageerror:', e.message));
  page.on('console', (m) => { if (m.text().includes('tafakor-ai')) console.log('console:', m.text()); });
  page.on('framenavigated', (f) => { if (f === page.mainFrame()) console.log('navigated:', f.url()); });
  try {
    await page.goto(APP + '/login');
    await api(page, 'POST', '/api/v1/auth/login', { email: 'admin@demo.test', password: 'Demo-Passw0rd!' });
    await api(page, 'PUT', '/ai-api/settings', { auto_mode: false });
    await page.goto(APP + '/');
    await page.evaluate(() => localStorage.setItem('tafakor-ai.exec', JSON.stringify('ui')));
    await page.waitForSelector('#tafakor-ai-host >> .orb', { timeout: 20000 });
    check('widget loads through the one <script> touchpoint', true);
    const maxId = async () => Math.max(0, ...((await api(page, 'GET', '/api/v1/treasury/transactions?page_size=100&kind=receipt')).items ?? []).map((x) => x.id));
    const before = (await api(page, 'GET', '/api/v1/treasury/transactions?page_size=1&kind=receipt')).total;
    const beforeMax = await maxId();

    await page.click('#tafakor-ai-host >> .orb');
    await snap(page, 'panel-open');
    await page.setInputFiles('#tafakor-ai-host >> input[type="file"]', path.join(root, 'test', 'fixtures', process.env.E2E_FILE ?? 'receipt-customer.png'));
    await page.waitForSelector('#tafakor-ai-host >> .chip button', { timeout: 20000 });
    await page.fill('#tafakor-ai-host >> textarea', process.env.E2E_TEXT ?? 'Company X این مبلغ را برای تسویه حسابش واریز کرده؛ برای پروژه وب‌سایت به عنوان دریافت از مشتری ثبتش کن');
    await snap(page, 'request');
    await page.keyboard.press('Enter');
    await page.waitForTimeout(1500);
    await waitIdle(page);
    await answerQuestions(page, ['ملت', 'Company X', 'سند']);
    await snap(page, 'approval-card');
    const confirm = page.locator('#tafakor-ai-host >> .card .btn.primary').last();
    check('approval card shown (default: ask me before registering)', await confirm.isVisible());
    const reason = await page.locator('#tafakor-ai-host >> .card .reason').last().innerText();
    console.log('  reason:', reason);
    await confirm.click();

    // the UI agent drives the real form
    await page.waitForURL('**/treasury/transactions', { timeout: 30000 });
    check('UI agent navigated to Receipts', true);
    await page.waitForSelector('[data-ai-dialog="treasury.newReceipt"]', { timeout: 30000 });
    check('UI agent opened the receipt form', true);
    await page.waitForTimeout(2500);
    await snap(page, 'filling');
    const cursorVisible = await page.locator('#tafakor-ai-host >> .cursor:not([hidden])').count();
    check('virtual cursor visible while acting', cursorVisible > 0);
    await page.waitForTimeout(4000);
    await snap(page, 'filled');
    await page.waitForSelector('[data-ai-dialog="treasury.newReceipt"]', { state: 'detached', timeout: 60000 });
    check('form saved and closed', true);
    await waitIdle(page);
    await page.waitForTimeout(1000);
    await snap(page, 'result');
    const resText = await page.locator('#tafakor-ai-host >> .reason.ok').last().innerText().catch(() => '');
    check('result card reports verified state', resText.includes('#'), resText);
    const after = await api(page, 'GET', '/api/v1/treasury/transactions?page_size=1&kind=receipt');
    check('receipt really exists in the accounting app', after.total === before + 1, `${before} -> ${after.total}`);
    const tx = { id: await maxId() };
    check('a new receipt was created by this run', tx.id > beforeMax, `id ${tx.id}`);
    const detail = await api(page, 'GET', `/api/v1/treasury/transactions/${tx.id}`);
    check('receipt values match (project, party, posted)', detail.cost_center_id !== null && detail.party_name === 'Company X' && detail.status === 'posted',
      `${detail.amount} ${detail.party_name} cc=${detail.cost_center_id} ${detail.counter_account_name ?? ''}`);
    const att = await api(page, 'GET', `/api/v1/attachments?entity_type=treasury_transaction&entity_id=${tx.id}`);
    check('original receipt attached', att.length === 1);

    // AI Activity tab shows it with Undo
    await page.click('#tafakor-ai-host >> .tabs button:nth-child(2)');
    await page.waitForTimeout(1200);
    await snap(page, 'activity');
    check('AI Activity lists the registration', (await page.locator('#tafakor-ai-host >> .item').count()) > 0);
    await page.click('#tafakor-ai-host >> .tabs button:nth-child(3)');
    await page.waitForTimeout(800);
    await snap(page, 'learned');
    await page.click('#tafakor-ai-host >> .tabs button:nth-child(4)');
    await page.waitForTimeout(500);
    await snap(page, 'settings');

    // English / LTR and dark theme render
    await page.evaluate(() => { document.documentElement.lang = 'en'; document.documentElement.dir = 'ltr'; document.documentElement.setAttribute('data-theme', 'dark'); });
    await page.waitForTimeout(600);
    await page.click('#tafakor-ai-host >> .tabs button:nth-child(1)');
    await snap(page, 'english-dark');
    const title = await page.locator('#tafakor-ai-host >> .head .title').innerText();
    check('widget follows app language (en)', title === 'Tafakor AI', title);
  } catch (e) {
    console.log('ERROR', e.message);
    await snap(page, 'error').catch(() => undefined);
    results.push(['no exception', false]);
  } finally {
    await ctx.close();
    await browser.close();
  }
  const failed = results.filter((r) => !r[1]).length;
  console.log(`${results.length - failed}/${results.length} passed; artifacts in ${out}`);
  process.exit(failed ? 1 : 0);
}

run();
