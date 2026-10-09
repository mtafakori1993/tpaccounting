#!/usr/bin/env node
// Verifies the generated form map (service/internal/catalog/capabilities.json → "ui", D41)
// against the LIVE application on the throwaway test stack: for every discovered form it
// opens the route, uses the discovered opener (button / variant button / the record's row /
// the edit button on the record's page) and checks that the dialog and every field tag are
// really in the DOM. Read-only: forms are opened and closed, never saved.
//
//   APP_URL=http://127.0.0.1:18085 node scripts/verify-forms.mjs
import { chromium } from 'playwright-core';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..', '..');
const APP = process.env.APP_URL ?? 'http://127.0.0.1:18085';
const catalog = JSON.parse(fs.readFileSync(path.join(root, 'service', 'internal', 'catalog', 'capabilities.json'), 'utf8'));
const out = path.join(root, 'test-artifacts', 'forms');
fs.mkdirSync(out, { recursive: true });

async function api(page, method, p) {
  return page.evaluate(async ([method, p]) => {
    const r = await fetch('/api/v1' + p, { method, credentials: 'same-origin' });
    return r.ok ? r.json() : null;
  }, [method, p]);
}

// an existing record id for edit forms: the first item of the collection
async function someId(page, op, kind) {
  const list = op.path.replace(/\/\{id\}.*$/, '').replace('{kind}', kind || 'invoices');
  const r = await api(page, 'GET', list + (list.includes('?') ? '&' : '?') + 'page_size=50');
  const items = Array.isArray(r) ? r : r?.items ?? r?.rules ?? r?.tax_tables ?? r?.insurance_rules;
  if (!items?.length) return undefined;
  // documents are editable only as drafts: never fall back to a posted one
  if ('status' in items[0]) return items.find((x) => x.status === 'draft')?.id;
  return items[0].id;
}

function routesFor(op, id) {
  const r = op.ui.route;
  const side = op.path.split('/')[1];
  const kinds = r.includes(':kind') ? (side === 'purchases' ? ['invoices', 'returns'] : ['invoices', 'quotations', 'returns']) : [''];
  return kinds.map((k) => r.replace(':side', side).replace(':kind', k).replace(':id', String(id ?? '')));
}

const dialogSel = (tags) => tags.map((t) => (t === '*' ? '[role="dialog"]' : `[data-ai-dialog="${t}"]`)).join(', ');

const browser = await chromium.launch({ executablePath: process.env.CHROME ?? '/usr/bin/google-chrome', args: ['--no-sandbox'] });
const page = await browser.newPage({ viewport: { width: 1400, height: 900 }, locale: 'fa-IR' });
await page.goto(APP + '/login');
await page.evaluate(async () => {
  await fetch('/api/v1/auth/login', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: 'admin@demo.test', password: 'Demo-Passw0rd!' }) });
});

const results = [];
for (const op of catalog.operations.filter((o) => o.ui)) {
  const ui = op.ui;
  const needsId = op.kind === 'update' && op.path.includes('{id}');
  for (const route of routesFor(op, 0)) {
    const kind = route.split('/')[2];
    const id = needsId ? await someId(page, op, op.ui.route.includes(':kind') ? kind : undefined) : undefined;
    if (needsId && !id) {
      results.push({ op: op.id, route, status: 'SKIP', detail: 'no editable (draft) record on the test stack' });
      continue;
    }
    const realRoute = route.replace(/\/0(\/|$)/, `/${id}$1`);
    const r = { op: op.id, route: realRoute, status: 'PASS', found: 0, fields: Object.keys(ui.fields).length, missing: [] };
    const seen = new Set();
    // every way the form opens (variants show different fields: bank vs cash, receipt vs transfer)
    const openers = ui.row ? ['row'] : ui.open?.length ? ui.open : [null];
    try {
      for (const opener of openers) {
        await page.goto(APP + realRoute, { waitUntil: 'networkidle' });
        await page.waitForSelector('main', { timeout: 8000 });
        if (opener === 'row') await page.locator(`[data-ai-row-id="${id}"]`).first().click({ timeout: 6000 });
        else if (opener) await page.locator(`[data-ai-action="${opener.tag}"]:visible`).first().click({ timeout: 6000 });
        let scope = page.locator('main');
        if (ui.dialogs?.length) {
          scope = page.locator(dialogSel(ui.dialogs)).first();
          await scope.waitFor({ timeout: 6000 });
        }
        await page.waitForTimeout(300);
        for (const [field, f] of Object.entries(ui.fields)) {
          if (seen.has(field)) continue;
          if (f.tab) {
            const tab = scope.locator(`[data-ai-tab="${f.tab}"]`).first();
            if (await tab.count()) await tab.click();
          }
          for (const tag of [f.tag, ...(f.alts ?? [])]) {
            if ((await scope.locator(`[data-ai-field="${tag}"]`).count()) > 0) {
              seen.add(field);
              break;
            }
          }
        }
        await page.screenshot({ path: path.join(out, `${op.id}${realRoute.replace(/\//g, '_')}${opener?.value ? '-' + opener.value : ''}.png`) });
        await page.keyboard.press('Escape');
      }
      r.found = seen.size;
      r.missing = Object.entries(ui.fields).filter(([k]) => !seen.has(k)).map(([k, f]) => `${k}→${f.tag}`);
      if (r.found === 0) r.status = 'FAIL';
      else if (r.missing.length) r.status = 'PARTIAL';
    } catch (e) {
      r.status = 'FAIL';
      r.detail = String(e.message ?? e).split('\n')[0];
    }
    results.push(r);
  }
}
await browser.close();

for (const r of results) {
  console.log(`${r.status.padEnd(7)} ${r.op.padEnd(34)} ${(r.route ?? '').padEnd(28)} ${r.fields ? `${r.found}/${r.fields}` : ''} ${r.missing?.length ? 'missing: ' + r.missing.join(', ') : ''} ${r.detail ?? ''}`);
}
fs.writeFileSync(path.join(out, 'results.json'), JSON.stringify(results, null, 2));
const counts = results.reduce((m, r) => ((m[r.status] = (m[r.status] ?? 0) + 1), m), {});
console.log(JSON.stringify(counts));
process.exit(results.some((r) => r.status === 'FAIL') ? 1 : 0);
