// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest';

// the vision fallback needs a real browser (screenshots); here it finds nothing
vi.mock('../src/agent/vision', () => ({ locateByVision: async () => null }));
import { Cursor } from '../src/agent/cursor';
import { HumanUIEngine } from '../src/agent/engine';
import { runPlan, watchUserSave } from '../src/agent/executor';

// happy-dom has no layout: give every element a box that depends on its position in the DOM,
// so "the pointer moved onto the element" is observable.
let seq = 0;
beforeEach(() => {
  seq = 0;
  document.body.innerHTML = '';
  (Element.prototype as any).scrollIntoView = () => {};
  Element.prototype.getBoundingClientRect = function () {
    const i = ((this as any).__i ??= ++seq);
    return { left: 10 * i, top: 20 * i, width: 100, height: 30, right: 10 * i + 100, bottom: 20 * i + 30, x: 10 * i, y: 20 * i, toJSON() {} } as DOMRect;
  };
});

function setup() {
  const host = document.createElement('div');
  document.body.append(host);
  const cursor = new Cursor(host.attachShadow({ mode: 'open' }));
  cursor.move = async (x, y) => { cursor.x = x; cursor.y = y; moves.push([x, y]); };
  cursor.click = async () => { clicks++; };
  return cursor;
}
let moves: [number, number][] = [];
let clicks = 0;
beforeEach(() => { moves = []; clicks = 0; });

function page() {
  document.body.insertAdjacentHTML('beforeend', `<main data-ai-page="parties">
    <button id="new" data-ai-action="parties.newParty">New</button></main>`);
  document.getElementById('new')!.addEventListener('click', () => {
    document.body.insertAdjacentHTML('beforeend', `<div role="dialog" class="dialog" data-ai-dialog="parties.newParty">
      <label for="n">Name</label><input id="n" data-ai-field="common.name">
      <input id="c" type="checkbox" data-ai-field="parties.customer">
      <div class="dialog-foot"><button id="save" data-ai-action="common.save">Save</button></div></div>`);
    document.getElementById('save')!.addEventListener('click', () => document.querySelector('[role=dialog]')!.remove());
  });
}

describe('human UI engine', () => {
  it('moves the pointer onto every element it operates, then really clicks/types/submits', async () => {
    page();
    const cursor = setup();
    const res = await runPlan({ task_id: 't', steps: [
      { op: 'click', target: 'parties.newParty', expect: 'dialog', label: 'open' },
      { op: 'fill', dialog: '*', target: 'common.name', value: 'شرکت قاسم', label: 'name' },
      { op: 'check', dialog: '*', target: 'parties.customer', value: 'true', label: 'customer' },
      { op: 'submit', dialog: '*', target: 'common.save', label: 'save' },
      { op: 'wait_closed', dialog: '*', label: 'verify' },
    ] }, cursor, () => {}, () => false);
    expect(res).toMatchObject({ ok: true, error: '' });
    // one pointer move + one click per interactive step (4), none skipped
    expect(moves.length).toBeGreaterThanOrEqual(4);
    expect(clicks).toBe(4);
    expect(document.querySelector('[role=dialog]')).toBeNull(); // the real form was saved and closed
  }, 20000);

  it('reports a missing element instead of pretending, and never calls fetch', async () => {
    page();
    const cursor = setup();
    let fetched = 0;
    (globalThis as any).fetch = () => { fetched++; return Promise.reject(new Error('no api')); };
    const engine = new HumanUIEngine(cursor);
    const err = await engine.execute({ op: 'click', target: 'does.not.exist', label: 'x' });
    expect(err).toMatch(/not found/);
    expect(fetched).toBe(0);
  }, 20000);

  // audit 2026-10-08: a salary component's form hides "exemption" for fixed amounts;
  // restating its default 0 must not stop the task ("field payroll.exemption not found")
  it('skips an optional default-value field the form does not show, but still fails a required one', async () => {
    page();
    const cursor = setup();
    const engine = new HumanUIEngine(cursor);
    expect(await engine.execute({ op: 'fill', target: 'payroll.exemption', value: '0', optional: true, label: 'x' })).toBeNull();
    expect(await engine.execute({ op: 'fill', target: 'payroll.exemption', value: '5', label: 'x' })).toMatch(/not found/);
  }, 20000);

  it('adapts to the control that is really there (select / checkbox given as fill)', async () => {
    document.body.insertAdjacentHTML('beforeend', `<main data-ai-page="p"><div data-ai-field="parties.kind"><select><option value="person">p</option><option value="company">c</option></select></div></main>`);
    const cursor = setup();
    const engine = new HumanUIEngine(cursor);
    expect(await engine.execute({ op: 'fill', target: 'parties.kind', value: 'company', label: 'kind' })).toBeNull();
    expect(document.querySelector('select')!.value).toBe('company');
    expect(clicks).toBe(1);
  }, 20000);

  // UI_PREVIEW: the form is filled and left to the user; the engine only watches.
  const previewPlan = { task_id: 't', mode: 'preview' as const, steps: [
    { op: 'click' as const, target: 'parties.newParty', expect: 'dialog', label: 'open' },
    { op: 'fill' as const, dialog: '*', target: 'common.name', value: 'شرکت قاسم', label: 'name' },
  ], watch: [
    { op: 'submit' as const, dialog: '*', target: 'common.save', label: 'save' },
    { op: 'wait_closed' as const, dialog: '*', label: 'verify' },
  ] };

  it('preview never presses save, and reports when the user saves the form themselves', async () => {
    page();
    const cursor = setup();
    const res = await runPlan(previewPlan, cursor, () => {}, () => false);
    expect(res).toMatchObject({ ok: true, preview: true });
    expect(document.querySelector('[role=dialog]')).not.toBeNull(); // still open: nothing submitted
    const before = clicks;
    const watching = watchUserSave(previewPlan, cursor, 5000);
    await new Promise((r) => setTimeout(r, 50));
    document.getElementById('save')!.click(); // the user's own click
    await expect(watching).resolves.toEqual({ closed: true, saved: true });
    expect(clicks).toBe(before); // the assistant clicked nothing
  });

  it('a form closed without pressing save is reported as not saved', async () => {
    page();
    const cursor = setup();
    await runPlan(previewPlan, cursor, () => {}, () => false);
    const watching = watchUserSave(previewPlan, cursor, 5000);
    await new Promise((r) => setTimeout(r, 50));
    document.querySelector('[role=dialog]')!.remove(); // closed with Escape / ✕
    await expect(watching).resolves.toEqual({ closed: true, saved: false });
  });
});
