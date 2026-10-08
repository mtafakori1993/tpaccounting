#!/usr/bin/env node
// Crawls the app (throwaway test stack) to
//   1. generate tafakor-ai/knowledge/ai-map.json (screens, routes, fields, actions) — `node scripts/crawl.mjs map`
//   2. run the tag coverage test: every visible interactive element on every route must
//      carry a data-ai-* tag, and tags must be identical in Persian and English — `node scripts/crawl.mjs coverage`
// Routes come from the router config (frontend/src/App.tsx). Only "open the form" buttons are
// clicked (dialogs are closed with Escape, nothing is saved).
//
// Env: APP_URL (default http://127.0.0.1:18085), AI_EMAIL/AI_PASSWORD (demo admin), CHROME (path).
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, '../../..');
const APP = process.env.APP_URL ?? 'http://127.0.0.1:18085';
const EMAIL = process.env.AI_EMAIL ?? 'admin@demo.test';
const PASSWORD = process.env.AI_PASSWORD ?? 'Demo-Passw0rd!';
const mode = process.argv[2] ?? 'coverage';

function routesFromRouter() {
  const src = fs.readFileSync(path.join(repo, 'frontend/src/App.tsx'), 'utf8');
  const out = [];
  for (const m of src.matchAll(/<Route\s+(index|path="([^"]+)")\s+element=\{<([A-Za-z]+)/g)) {
    const p = m[1] === 'index' ? '/' : '/' + m[2];
    if (p === '/*' || p === '/login' || p === '/setup' || m[3] === 'Navigate') continue;
    out.push({ path: p, component: m[3] });
  }
  return out;
}

async function api(page, method, p, body) {
  return page.evaluate(async ([method, p, body]) => {
    const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
    const r = await fetch('/api/v1' + p, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': m ? decodeURIComponent(m[1]) : '' }, body: body ? JSON.stringify(body) : undefined });
    const t = await r.text();
    return t ? JSON.parse(t) : null;
  }, [method, p, body]);
}

async function sampleIds(page) {
  const first = async (p) => {
    try {
      const d = await api(page, 'GET', p);
      const items = Array.isArray(d) ? d : d?.items ?? [];
      return items[0]?.id;
    } catch { return undefined; }
  };
  return {
    party: await first('/parties?page_size=1'),
    journal: await first('/journal-entries?page_size=1'),
    salesInvoice: await first('/sales/invoices?page_size=1'),
    check: await first('/checks?page_size=1'),
    budget: await first('/budgets?page_size=1'),
    payroll: await first('/payroll/periods?page_size=1'),
    stock: await first('/inventory/documents?page_size=1'),
  };
}

function concrete(routes, ids) {
  const out = [];
  for (const r of routes) {
    let p = r.path;
    if (p.includes(':side/:kind')) {
      for (const [side, kind] of [['sales', 'invoices'], ['sales', 'quotations'], ['sales', 'returns'], ['purchases', 'invoices'], ['purchases', 'returns']]) {
        let q = p.replace(':side', side).replace(':kind', kind);
        if (q.includes(':id')) {
          if (!(side === 'sales' && kind === 'invoices' && ids.salesInvoice)) continue;
          q = q.replace(':id', ids.salesInvoice);
        }
        out.push({ ...r, url: q });
      }
      continue;
    }
    const idFor = p.startsWith('/parties') ? ids.party : p.startsWith('/journal') ? ids.journal : p.startsWith('/checks') ? ids.check
      : p.startsWith('/budgets') ? ids.budget : p.startsWith('/payroll') ? ids.payroll : p.startsWith('/inventory') ? ids.stock : undefined;
    if (p.includes(':id')) {
      if (!idFor) continue;
      p = p.replace(':id', idFor);
    }
    if (p.includes(':report')) {
      if (p.startsWith('/reports')) for (const rep of ['trial-balance', 'profit-loss', 'expenses']) out.push({ ...r, url: p.replace(':report', rep) });
      continue;
    }
    out.push({ ...r, url: p });
  }
  return out;
}

const COLLECT = () => {
  const vis = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none'; };
  const SEL = 'button, a[href], input:not([type="hidden"]), select, textarea, [role="combobox"], [role="tab"], [role="option"], [role="switch"], [role="checkbox"], [role="menuitem"]';
  const dlg = Array.from(document.querySelectorAll('[role="dialog"]')).filter((d) => !d.closest('#tafakor-ai-host')).pop();
  const els = Array.from((dlg ?? document).querySelectorAll(SEL)).filter((el) => !el.closest('#tafakor-ai-host') && vis(el));
  const tagOf = (el) => el.getAttribute('data-ai-action') ? 'action:' + el.getAttribute('data-ai-action')
    : el.closest('[data-ai-field]') ? 'field:' + el.closest('[data-ai-field]').getAttribute('data-ai-field')
    : el.getAttribute('data-ai-tab') ? 'tab:' + el.getAttribute('data-ai-tab')
    : el.getAttribute('data-ai-option-id') ? 'option:' + el.getAttribute('data-ai-option-id')
    : el.getAttribute('data-ai-nav') ? 'nav:' + el.getAttribute('data-ai-nav')
    : el.closest('[data-ai-row-id]') ? 'row:' + el.closest('[data-ai-row-id]').getAttribute('data-ai-row-id')
    : null;
  const tags = [], untagged = [];
  for (const el of els) {
    const t = tagOf(el);
    if (t) tags.push(t);
    else untagged.push(`${el.tagName.toLowerCase()}${el.getAttribute('role') ? '[role=' + el.getAttribute('role') + ']' : ''} "${(el.getAttribute('aria-label') || el.textContent || el.getAttribute('placeholder') || '').trim().slice(0, 40)}" in ${el.closest('[class]')?.className?.toString().slice(0, 40)}`);
  }
  const dialog = Array.from(document.querySelectorAll('[role="dialog"]')).filter((d) => !d.closest('#tafakor-ai-host')).pop();
  return {
    page: document.querySelector('[data-ai-page]')?.getAttribute('data-ai-page') ?? null,
    dialog: dialog?.getAttribute('data-ai-dialog') ?? null,
    tags: [...new Set(tags)].sort(),
    fields: [...new Set(Array.from(document.querySelectorAll('[data-ai-field]')).filter((f) => !f.closest('#tafakor-ai-host')).map((f) => f.getAttribute('data-ai-field')))],
    actions: [...new Set(Array.from(document.querySelectorAll('[data-ai-action]')).filter((f) => !f.closest('#tafakor-ai-host') && vis(f)).map((f) => f.getAttribute('data-ai-action')))],
    untagged,
  };
};

async function settle(page) {
  await page.waitForLoadState('networkidle').catch(() => undefined);
  await page.waitForTimeout(400);
}

async function crawl(page, routes, withDialogs) {
  const screens = [];
  for (const r of routes) {
    await page.goto(APP + r.url);
    await settle(page);
    const s = await page.evaluate(COLLECT);
    const screen = { route: r.path, url: r.url, component: r.component, ...s, dialogs: [] };
    if (withDialogs) {
      const openers = s.actions.filter((a) => /\.new[A-Z]|^common\.new$|newParty|newExpense|newReceipt|newPayment|newTransfer/.test(a));
      for (const a of openers) {
        const btn = page.locator(`[data-ai-action="${a}"]`).first();
        if (!(await btn.isVisible().catch(() => false)) || (await btn.isDisabled().catch(() => true))) continue;
        const before = page.url();
        await btn.click();
        await settle(page);
        const d = await page.evaluate(COLLECT);
        if (d.dialog) {
          screen.dialogs.push({ opened_by: a, dialog: d.dialog, fields: d.fields, actions: d.actions, tags: d.tags, untagged: d.untagged });
          await page.keyboard.press('Escape');
          await settle(page);
        } else if (page.url() !== before) {
          screen.dialogs.push({ opened_by: a, navigates_to: new URL(page.url()).pathname, fields: d.fields, actions: d.actions, untagged: d.untagged });
          await page.goto(APP + r.url);
          await settle(page);
        }
      }
    }
    screens.push(screen);
  }
  return screens;
}

async function main() {
  const browser = await chromium.launch({ executablePath: process.env.CHROME ?? '/usr/bin/google-chrome', args: ['--no-sandbox'] });
  const ctx = await browser.newContext({ viewport: { width: 1366, height: 900 } });
  const page = await ctx.newPage();
  await page.goto(APP + '/login');
  await api(page, 'POST', '/auth/login', { email: EMAIL, password: PASSWORD });
  const me = await api(page, 'GET', '/auth/me');
  const originalLang = me.language;
  const routes = concrete(routesFromRouter(), await sampleIds(page));
  const setLang = async (lang) => {
    await api(page, 'PUT', '/auth/preferences', { language: lang, theme: me.theme });
    await page.goto(APP + '/');
    await page.evaluate((l) => localStorage.setItem('tp.lang', l), lang);
    await page.reload();
    await settle(page);
  };
  try {
    if (mode === 'map') {
      await setLang('fa');
      const screens = await crawl(page, routes, true);
      const map = {
        generated_at: new Date().toISOString(),
        generator: 'tafakor-ai/widget/scripts/crawl.mjs map',
        note: 'Tags are language independent (i18n keys of labels). Regenerate after UI changes.',
        screens: screens.map(({ untagged, tags, ...s }) => s),
      };
      fs.writeFileSync(path.join(repo, 'tafakor-ai/knowledge/ai-map.json'), JSON.stringify(map, null, 2));
      console.log(`ai-map.json: ${screens.length} screens`);
      return;
    }
    // coverage, in both languages
    await setLang('fa');
    const fa = await crawl(page, routes, true);
    await setLang('en');
    const en = await crawl(page, routes, true);
    let failures = 0;
    for (let i = 0; i < fa.length; i++) {
      const a = fa[i], b = en[i];
      const un = [...a.untagged, ...a.dialogs.flatMap((d) => d.untagged.map((u) => `${d.dialog ?? d.opened_by}: ${u}`))];
      if (un.length) {
        failures++;
        console.log(`UNTAGGED ${a.url}:\n  ${[...new Set(un)].slice(0, 15).join('\n  ')}`);
      }
      const ta = JSON.stringify(a.tags), tb = JSON.stringify(b.tags);
      if (ta !== tb) {
        failures++;
        const onlyFa = a.tags.filter((t) => !b.tags.includes(t)), onlyEn = b.tags.filter((t) => !a.tags.includes(t));
        console.log(`LANGUAGE-DEPENDENT TAGS ${a.url}: fa-only ${JSON.stringify(onlyFa)} en-only ${JSON.stringify(onlyEn)}`);
      }
    }
    console.log(`coverage: ${fa.length} routes, ${failures} problems`);
    process.exitCode = failures ? 1 : 0;
  } finally {
    await setLang(originalLang).catch(() => undefined);
    await browser.close();
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
