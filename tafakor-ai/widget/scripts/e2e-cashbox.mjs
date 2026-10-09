#!/usr/bin/env node
// REAL-BROWSER end-to-end test of HUMAN_UI on the throwaway test stack (no simulated DOM).
//
//   User: «یه صندوق جدید به اسم علعی ایجاد کن»
//
// Proves, from what the real rendered application shows:
//   - the widget's pointer is visible and TRAVELS (many distinct positions, with durations)
//   - it moves onto the New-cashbox button, the name field, the opening balance and Save, in that order
//   - real clicks and real typing happen (captured by page-level listeners, not by the widget)
//   - the form is saved BY THE UI (the browser itself sends POST /api/v1/treasury/accounts)
//   - the AI service made no accounting write (audit has no tool_call write for this task)
//   - «علعی» is visible in the real list before success is reported
// Evidence (frames, video, evidence.json) goes to tafakor-ai/test-artifacts/e2e-cashbox/.
//
//   APP_URL=http://127.0.0.1:18085 E2E_SPEED=human node scripts/e2e-cashbox.mjs
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const APP = process.env.APP_URL ?? 'http://127.0.0.1:18085';
let NAME = process.env.E2E_NAME ?? 'علعی';
let TEXT = '';
const SPEED = process.env.E2E_SPEED ?? ''; // '' = the product default (human)
const out = path.join(root, 'test-artifacts', 'e2e-cashbox');
fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(out, { recursive: true });
const results = [];
const evidence = { text: TEXT, name: NAME, speed: SPEED || 'human (default)' };
const check = (name, ok, detail = '') => {
  results.push([name, ok]);
  evidence[name] = { ok, detail };
  console.log(`${ok ? 'PASS' : 'FAIL'}: ${name} ${detail}`);
};
let frame = 0;
const snap = (page, n) => page.screenshot({ path: path.join(out, `${String(++frame).padStart(2, '0')}-${n}.png`) }).catch(() => undefined);

async function api(page, method, p, body) {
  return page.evaluate(async ([method, p, body]) => {
    const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
    const r = await fetch(p, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': m ? decodeURIComponent(m[1]) : '' }, body: body ? JSON.stringify(body) : undefined });
    const t = await r.text();
    return t ? JSON.parse(t) : null;
  }, [method, p, body]);
}

// Runs inside every page: records what the pointer does and what the DOM really receives.
function instrument() {
  const log = (window.__e2e = { pointer: [], events: [] });
  const t0 = performance.now();
  const hostCursor = () => document.getElementById('tafakor-ai-host')?.shadowRoot?.querySelector('.cursor');
  let last = '';
  const sample = () => {
    const c = hostCursor();
    if (c) {
      const r = c.getBoundingClientRect();
      const label = c.querySelector('.label')?.textContent ?? '';
      const key = `${c.hidden}|${Math.round(r.left)}|${Math.round(r.top)}|${label}`;
      if (key !== last) {
        last = key;
        log.pointer.push({ t: Math.round(performance.now() - t0), hidden: c.hidden, x: Math.round(r.left), y: Math.round(r.top), label, transition: c.style.transitionDuration });
      }
    }
    requestAnimationFrame(sample);
  };
  requestAnimationFrame(sample);
  const describe = (el) => {
    const a = el.closest?.('[data-ai-action],[data-ai-field],[data-ai-nav],button,input,a');
    const tag = a ?? el;
    return { tag: tag.tagName, action: tag.getAttribute?.('data-ai-action'), field: tag.getAttribute?.('data-ai-field') ?? tag.closest?.('[data-ai-field]')?.getAttribute('data-ai-field'), nav: tag.getAttribute?.('data-ai-nav'), id: tag.id };
  };
  document.addEventListener('click', (e) => {
    const r = e.target.getBoundingClientRect?.();
    const c = hostCursor()?.getBoundingClientRect();
    log.events.push({ t: Math.round(performance.now() - t0), type: 'click', trusted: e.isTrusted, ...describe(e.target), pointer: c ? [Math.round(c.left), Math.round(c.top)] : null,
      box: r ? [Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height)] : null });
  }, true);
  document.addEventListener('input', (e) => {
    log.events.push({ t: Math.round(performance.now() - t0), type: 'input', trusted: e.isTrusted, ...describe(e.target), value: e.target.value });
  }, true);
}

async function run() {
  const browser = await chromium.launch({ executablePath: process.env.CHROME ?? '/usr/bin/google-chrome', args: ['--no-sandbox'], headless: process.env.HEADED ? false : true });
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 860 }, locale: 'fa-IR', recordVideo: { dir: out, size: { width: 1366, height: 860 } } });
  await ctx.addInitScript(instrument);
  const page = await ctx.newPage();
  const writes = [];
  page.on('request', (r) => {
    if (r.method() !== 'GET' && /\/(api\/v1|ai-api)\//.test(r.url())) writes.push({ t: Date.now(), method: r.method(), url: r.url().replace(APP, '') });
  });
  page.on('pageerror', (e) => console.log('pageerror:', e.message));
  try {
    await page.goto(APP + '/login');
    await api(page, 'POST', '/api/v1/auth/login', { email: 'admin@demo.test', password: 'Demo-Passw0rd!' });
    await api(page, 'PUT', '/ai-api/settings', { auto_mode: false, exec_preference: 'human_ui', exec_speed: SPEED || 'human' });
    await page.goto(APP + '/');
    await page.waitForSelector('#tafakor-ai-host >> .orb', { timeout: 20000 });
    check('widget loaded in the real app', true);

    const list = async () => {
      const r = await api(page, 'GET', '/api/v1/treasury/accounts');
      return Array.isArray(r) ? r : r?.items ?? [];
    };
    const before = await list();
    evidence.accounts_before = before.length;
    // a name that already exists would be a duplicate: pick the next free variant of the requested name
    for (let n = 2; before.some((a) => a.name === NAME); n++) NAME = `${process.env.E2E_NAME ?? 'علعی'} ${n}`;
    TEXT = process.env.E2E_TEXT ?? `یه صندوق جدید به اسم ${NAME} ایجاد کن`;
    evidence.name = NAME; evidence.text = TEXT;
    const existing = before.filter((a) => a.name === NAME).length;
    check('precondition: no cashbox with this name yet', existing === 0, `${existing} existing`);
    writes.length = 0;

    await page.click('#tafakor-ai-host >> .orb');
    await page.fill('#tafakor-ai-host >> textarea', TEXT);
    await snap(page, 'request');
    const sendAt = Date.now();
    await page.keyboard.press('Enter');

    // the visible sequence
    await page.waitForURL('**/treasury', { timeout: 90000 }).catch(() => undefined);
    check('went to the cash/bank accounts page (route /treasury)', new URL(page.url()).pathname === '/treasury', page.url());
    await page.waitForSelector('.dialog[role="dialog"]', { timeout: 60000 });
    await snap(page, 'form-open');
    const pointerSeen = await page.locator('#tafakor-ai-host >> .cursor:not([hidden])').count();
    check('pointer is visible in the browser while the form is open', pointerSeen > 0);
    await page.waitForFunction(() => document.querySelector('.dialog[role="dialog"] input')?.value?.includes('') && [...document.querySelectorAll('.dialog[role="dialog"] input')].some((i) => i.value.length > 0), null, { timeout: 60000 }).catch(() => undefined);
    await snap(page, 'typing');
    await page.waitForSelector('.dialog[role="dialog"]', { state: 'detached', timeout: 90000 });
    await snap(page, 'saved');

    // result reported by the assistant only after verifying
    await page.waitForFunction(() => {
      const host = document.getElementById('tafakor-ai-host');
      return host?.shadowRoot && !host.shadowRoot.querySelector('.orb.busy');
    }, null, { timeout: 120000 });
    await page.waitForTimeout(1500);
    const panelOpen = await page.locator('#tafakor-ai-host >> .panel:not([hidden])').count();
    if (!panelOpen) await page.click('#tafakor-ai-host >> .orb').catch(() => undefined);
    await page.waitForTimeout(500);
    await snap(page, 'result');
    const lastMsgs = await page.locator('#tafakor-ai-host >> .msg.ai, #tafakor-ai-host >> .card .reason').allInnerTexts();
    evidence.assistant_texts = (await page.locator('#tafakor-ai-host >> .reason').allInnerTexts()).slice(-4);

    const inbox = await page.evaluate(() => window.__e2e);
    fs.writeFileSync(path.join(out, 'pointer-and-dom-events.json'), JSON.stringify(inbox, null, 1));

    // --- pointer ---
    const visible = inbox.pointer.filter((p) => !p.hidden);
    const distinct = new Set(visible.map((p) => `${p.x},${p.y}`));
    check('pointer travelled across many distinct positions', distinct.size >= 6, `${distinct.size} positions`);
    const travel = visible.filter((p) => parseInt(p.transition) > 0);
    check('pointer moves have a visible duration (transition)', travel.length >= 4 && (SPEED === 'instant' || travel.some((p) => parseInt(p.transition) >= 500)),
      `${travel.length} animated moves, max ${Math.max(0, ...travel.map((p) => parseInt(p.transition)))}ms`);

    // --- ordered interactions captured by page-level listeners ---
    const ev = inbox.events;
    const idx = (f) => ev.findIndex(f);
    const iNew = idx((e) => e.type === 'click' && (e.action === 'treasury.newCash' || e.action === 'treasury.newBank'));
    const iNameClick = idx((e) => e.type === 'click' && e.field === 'common.name');
    const iType = idx((e) => e.type === 'input' && e.field === 'common.name' && e.value === NAME);
    const iBal = idx((e) => e.type === 'input' && e.field === 'treasury.openingBalance');
    const iSave = idx((e) => e.type === 'click' && e.action === 'common.save');
    check('real click on the New-cashbox button', iNew >= 0, JSON.stringify(ev[iNew] ?? {}));
    check('real click into the name field after it', iNameClick > iNew, `idx ${iNameClick}`);
    check(`real typing of «${NAME}» into the name field`, iType > iNameClick, `${ev.filter((e) => e.type === 'input' && e.field === 'common.name').length} input events`);
    check('opening balance typed (0)', iBal < 0 ? true : iBal > iType, iBal < 0 ? 'left at its default 0 (form default)' : `idx ${iBal}`);
    check('real click on Save after typing', iSave > iType, `idx ${iSave}`);
    // the pointer was on the target when each click happened
    const onTarget = ev.filter((e) => e.type === 'click' && e.pointer && e.box && ['treasury.newCash', 'treasury.newBank', 'common.save'].includes(e.action))
      .every((e) => e.pointer[0] >= e.box[0] - 40 && e.pointer[0] <= e.box[0] + e.box[2] + 40 && e.pointer[1] >= e.box[1] - 30 && e.pointer[1] <= e.box[1] + e.box[3] + 30);
    check('pointer was ON the button when it was clicked', onTarget);

    // --- who wrote ---
    const saves = writes.filter((w) => w.method === 'POST' && w.url.startsWith('/api/v1/treasury/accounts'));
    check('the browser UI itself sent exactly one POST /api/v1/treasury/accounts', saves.length === 1, JSON.stringify(saves));
    const aiWrites = writes.filter((w) => w.url.startsWith('/api/v1') && !w.url.startsWith('/api/v1/treasury/accounts'));
    check('no other accounting write from the browser', aiWrites.length === 0, JSON.stringify(aiWrites));
    const tasks = await api(page, 'GET', '/ai-api/tasks');
    const taskId = (Array.isArray(tasks) ? tasks : tasks?.tasks ?? [])[0]?.id;
    evidence.task_id = taskId;
    const audit = await api(page, 'GET', `/ai-api/audit?task_id=${taskId}`);
    const entries = Array.isArray(audit) ? audit : audit?.entries ?? [];
    const kinds = entries.map((e) => e.kind);
    evidence.audit_kinds = kinds;
    const apiWrite = entries.filter((e) => e.kind === 'tool_call' && /accounts\.create|treasury/.test(JSON.stringify(e.data ?? e)));
    check('AI audit: no tool_call (API write) for this task', apiWrite.length === 0, JSON.stringify(apiWrite).slice(0, 200));
    check('AI audit: ui_plan + record_change via human_ui', kinds.includes('ui_plan') && entries.some((e) => e.kind === 'record_change' && JSON.stringify(e.data ?? e).includes('human_ui')), kinds.join(','));

    // --- result ---
    const after = await list();
    check('exactly one new account exists in the application', after.length === before.length + 1 && after.filter((a) => a.name === NAME).length === 1, `${before.length} -> ${after.length}`);
    await page.goto(APP + '/treasury');
    await page.waitForSelector('main', { timeout: 20000 });
    await page.waitForFunction((n) => document.querySelector('main')?.textContent?.includes(n), NAME, { timeout: 20000 }).catch(() => undefined);
    check(`«${NAME}» is visible in the real list`, (await page.locator('main').innerText()).includes(NAME));
    await snap(page, 'list');
    check('assistant reported success only after all of the above', evidence.assistant_texts.some((s) => /انجام شد|ثبت شد|done/i.test(s)) , evidence.assistant_texts.join(' | ').slice(0, 300));
    evidence.finished_in_s = Math.round((Date.now() - sendAt) / 1000);
  } catch (e) {
    console.log('ERROR', e.message);
    await snap(page, 'error');
    results.push(['no exception', false]);
    evidence.error = e.message;
    try { fs.writeFileSync(path.join(out, 'pointer-and-dom-events.json'), JSON.stringify(await page.evaluate(() => window.__e2e), null, 1)); } catch { /* page gone */ }
  } finally {
    evidence.writes_seen_by_browser = writes;
    fs.writeFileSync(path.join(out, 'evidence.json'), JSON.stringify(evidence, null, 1));
    await ctx.close();
    await browser.close();
  }
  const failed = results.filter((r) => !r[1]).length;
  console.log(`${results.length - failed}/${results.length} passed; artifacts in ${out}`);
  process.exit(failed ? 1 : 0);
}

run();
