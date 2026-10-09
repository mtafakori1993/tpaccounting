// The ONE human-interaction engine. In HUMAN_UI mode every action on the application —
// navigation, clicks, typing, selecting, scrolling, opening rows — goes through
// executeHumanUIAction(). The visible pointer is intrinsic to it: the engine resolves the
// real rendered element, scrolls it into view, moves the pointer onto it (re-resolving
// after scrolls and re-renders), performs the real interaction and observes the result.
// Features never call the pointer or the DOM drivers themselves, so a new module with a
// normal, tagged UI is operated with a pointer automatically.
//
// Targets are found by (1) stable data-ai-* tags, (2) accessibility role/name, (3) the
// server-side vision fallback — never by screen coordinates. Nothing here ever calls the
// application's API: if an element cannot be found the action FAILS (structured error);
// it does not pretend, and it does not fall back to DIRECT.

import { Cursor } from './cursor';
import { blur, click, comboPick, navigate as routerNavigate, selectValue, setChecked, setDate, type, digitsToAscii } from './drivers';
import { snapshot, waitFor } from './observer';
import { locateByVision } from './vision';

export type HumanOp =
  | 'navigate' | 'click' | 'double_click' | 'hover' | 'fill' | 'select' | 'combo' | 'date' | 'tab' | 'check'
  | 'submit' | 'wait_closed' | 'wait_leave' | 'verify_text' | 'open_row' | 'ensure_rows' | 'scroll' | 'back' | 'forward';

export interface UIStep {
  op: HumanOp;
  page?: string;
  /** data-ai-dialog tag; "*" = the topmost open dialog, whatever its title */
  dialog?: string;
  target?: string;
  /** Alternative tags for the same target or scope, tried in order. */
  alts?: string[];
  value?: string;
  search?: string;
  /** Cell of a line grid: 1-based row, 0-based cell. */
  row?: number;
  col?: number;
  /** What a click must cause: "dialog" | "route:<prefix>" | "none". Empty = legacy. */
  expect?: string;
  /** The value only restates the form's default: skip it when the form hides the control. */
  optional?: boolean;
  label: string;
}

// Secondary (accessibility) names for tags, used only when a tag is missing (older app build).
const LABELS: Record<string, string[]> = {
  'common.date': ['Date', 'تاریخ'],
  'common.amount': ['Amount', 'مبلغ'],
  'treasury.treasuryAccount': ['Cash / bank account', 'صندوق / حساب بانکی'],
  'treasury.payee': ['Party', 'شخص'],
  'journal.account': ['Account', 'حساب'],
  'journal.costCenter': ['Cost center', 'مرکز هزینه'],
  'common.reference': ['Reference', 'عطف'],
  'common.description': ['Description', 'شرح'],
  'treasury.autoAllocate': ['Settle oldest invoices first', 'تسویه خودکار از قدیمی‌ترین فاکتور'],
  'common.save': ['Save', 'ذخیره'],
  'common.saveDraft': ['Save draft', 'ذخیره پیش‌نویس'],
  'common.saveAndPost': ['Save and post', 'ذخیره و ثبت قطعی'],
  'common.confirm': ['Confirm', 'تأیید'],
  'common.cancel': ['Cancel', 'لغو'],
  'common.printDoc': ['Print', 'چاپ'],
  'treasury.newReceipt': ['New receipt', 'دریافت جدید'],
  'treasury.newPayment': ['New payment', 'پرداخت جدید'],
  'expenses.newExpense': ['New expense', 'هزینه جدید'],
  'expenses.category': ['Category', 'دسته هزینه'],
  'expenses.paymentMethod': ['Paid from', 'محل پرداخت'],
  'invoices.supplier': ['Supplier', 'تأمین‌کننده'],
};

const norm = (s: string) => s.replace(/\s+/g, ' ').replace(/\*/g, '').trim();
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
const CONTROL = 'input:not([type="hidden"]), select, textarea, [role="combobox"]';

function isVisible(el: Element) {
  const r = (el as HTMLElement).getBoundingClientRect();
  return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden';
}

export function openDialogs(): HTMLElement[] {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).filter(isVisible);
}

function pageScope(): ParentNode {
  return document.querySelector('main [data-ai-page], main[data-ai-page]') ?? document.querySelector('main') ?? document;
}

/** Where a step acts: the page content, the topmost dialog ("*"), or a dialog by tag. */
export function dialogScope(key?: string, alts?: string[]): ParentNode | null {
  if (!key) return pageScope();
  if (key === '*') {
    const all = openDialogs();
    return all[all.length - 1] ?? null;
  }
  for (const k of [key, ...(alts ?? [])]) {
    const tagged = document.querySelector(`[data-ai-dialog="${CSS.escape(k)}"]`);
    if (tagged) return tagged;
    const names = LABELS[k] ?? [];
    for (const d of Array.from(document.querySelectorAll('[role="dialog"]'))) {
      const title = d.querySelector('.dialog-title, h2')?.textContent ?? '';
      if (names.some((n) => norm(title) === n)) return d;
    }
  }
  return null;
}

/** Finds the control for a field: tag first, then the <label> text. */
function fieldControl(scope: ParentNode, key: string): HTMLElement | null {
  const wrap = scope.querySelector(`[data-ai-field="${CSS.escape(key)}"]`);
  const pick = (w: Element) => w.querySelector<HTMLElement>(CONTROL);
  if (wrap) return wrap.matches('input,select,textarea,[role="combobox"]') ? (wrap as HTMLElement) : pick(wrap);
  const names = LABELS[key] ?? [];
  for (const lbl of Array.from(scope.querySelectorAll('label'))) {
    if (names.includes(norm(lbl.textContent ?? ''))) {
      const id = lbl.getAttribute('for');
      const el = id ? document.getElementById(id) : lbl.querySelector('input,select,textarea');
      if (el) return el as HTMLElement;
      const parent = lbl.parentElement;
      if (parent) return pick(parent);
    }
  }
  return null;
}

/** Visible tagged control; real buttons win over links (a link may reload the page). */
function actionControl(scope: ParentNode, keys: string[]): HTMLElement | null {
  for (const key of keys) {
    const tagged = Array.from(scope.querySelectorAll<HTMLElement>(`[data-ai-action="${CSS.escape(key)}"]`)).filter(isVisible);
    const best = tagged.find((e) => e.tagName === 'BUTTON') ?? tagged[0];
    if (best) return best;
    const names = LABELS[key] ?? [];
    const named = Array.from(scope.querySelectorAll<HTMLElement>('button, [role="button"]')).find((b) => isVisible(b) && names.includes(norm(b.textContent ?? '')));
    if (named) return named;
  }
  return null;
}

/** A cell of a line grid: the control in row `row` (1-based), cell `col` (0-based). */
function cellControl(row: number, col: number): HTMLElement | null {
  const rows = gridRows();
  const cell = rows[row - 1]?.cells[col];
  return cell ? cell.querySelector<HTMLElement>(CONTROL) : null;
}

function gridRows(): HTMLTableRowElement[] {
  const scope = pageScope() as ParentNode;
  const grid = scope.querySelector('.entry-grid table tbody, table.table tbody');
  return grid ? Array.from(grid.querySelectorAll<HTMLTableRowElement>(':scope > tr')) : [];
}

export class HumanUIEngine {
  private pathAtSubmit = '';
  private submitDialog: HTMLElement | null = null;

  constructor(readonly cursor: Cursor) {}

  /** Locates a step's target in the live DOM (tags → accessibility). */
  private finder(step: UIStep, kind: 'field' | 'action' | 'tab' | 'cell'): () => HTMLElement | null {
    return () => {
      if (kind === 'cell') return cellControl(step.row!, step.col!);
      const scope = dialogScope(step.dialog, step.alts);
      if (!scope) return null;
      if (kind === 'field') {
        // a ternary label renders one of several tags; the variant decides which
        for (const k of [step.target!, ...(step.alts ?? [])]) {
          const f = fieldControl(scope, k);
          if (f) return f;
        }
        return null;
      }
      if (kind === 'tab') return scope.querySelector<HTMLElement>(`[role="tab"][data-ai-tab="${CSS.escape(step.target!)}"], [role="tab"][data-key="${CSS.escape(step.target!)}"]`);
      return actionControl(scope, [step.target!, ...(step.alts ?? [])].filter(Boolean));
    };
  }

  /** Resolves a step's target to a real element: tags, accessibility, then the vision fallback. */
  private async resolve(step: UIStep, kind: 'field' | 'action' | 'tab' | 'cell'): Promise<HTMLElement | null> {
    const el = await waitFor(this.finder(step, kind), 4000);
    if (el) return el;
    return locateByVision(step as never, snapshot());
  }

  /** Moves the pointer onto the control (looking it up again as the page moves), then clicks it. */
  private async pointAndClick(step: UIStep, el: HTMLElement, kind: 'field' | 'action' | 'tab' | 'cell', doClick = true): Promise<HTMLElement> {
    const find = this.finder(step, kind);
    const target = (await this.cursor.to(() => (el.isConnected ? el : find()), step.label)) as HTMLElement;
    if (doClick) await this.cursor.click();
    return target;
  }

  /** The single entry point. Resolves to null on success or a plain-language error. */
  async execute(step: UIStep): Promise<string | null> {
    this.cursor.show();
    switch (step.op) {
      case 'navigate': return this.doNavigate(step);
      case 'back': history.back(); await sleep(250); return null;
      case 'forward': history.forward(); await sleep(250); return null;
      case 'scroll': {
        const amount = Number(step.value ?? '400') || 400;
        window.scrollBy({ top: amount, behavior: 'smooth' });
        await sleep(350);
        return null;
      }
      case 'click': case 'double_click': case 'hover': return this.doClick(step);
      case 'tab': {
        const el = await this.resolve(step, 'tab');
        if (!el) return `tab ${step.target} not found`;
        await this.pointAndClick(step, el, 'tab');
        click(el);
        return (await waitFor(() => el.getAttribute('aria-selected') === 'true', 2000)) ? null : `tab ${step.target} not selected`;
      }
      case 'fill': case 'select': case 'combo': case 'date': case 'check': return this.doField(step);
      case 'open_row': return this.doOpenRow(step);
      case 'ensure_rows': return this.doEnsureRows(step);
      case 'submit': return this.doSubmit(step);
      case 'wait_closed': return this.doWaitClosed(step);
      case 'wait_leave': return this.doWaitLeave(step);
      case 'verify_text': return this.doVerifyText(step);
    }
    return `unknown step ${(step as UIStep).op}`;
  }

  // ---- navigation: click the menu link when it exists, otherwise the app's own router ----
  private async doNavigate(s: UIStep): Promise<string | null> {
    const route = s.value!;
    if (location.pathname !== route) {
      const link = document.querySelector<HTMLElement>(`a[data-ai-nav="${CSS.escape(route)}"], a[href="${CSS.escape(route)}"]`);
      if (link && isVisible(link)) {
        await this.pointAndClick(s, link, 'action');
        click(link);
      } else {
        // no link for this route on screen (e.g. a record URL): the pointer rests on the page
        // content while the application's router opens it — still the app, not its API
        await this.cursor.move(window.innerWidth / 2, 96);
        if (!(await routerNavigate(route))) return `could not open ${route}`;
      }
    }
    if (!(await waitFor(() => location.pathname === route, 5000))) return `could not open ${route}`;
    if (s.page) {
      const ok = await waitFor(() => document.querySelector(`[data-ai-page="${CSS.escape(s.page!)}"]`) || location.pathname === route, 5000);
      if (!ok) return `page ${s.page} did not load`;
    }
    await sleep(120);
    return null;
  }

  // ---- click / double-click / hover with the expected effect observed ----
  private async doClick(s: UIStep): Promise<string | null> {
    const dialogsBefore = openDialogs().length;
    const pathBefore = location.pathname;
    const el = await this.resolve(s, 'action');
    if (!el) return `button ${s.target} not found`;
    if ((el as HTMLButtonElement).disabled) return `button ${s.target} is disabled (permission?)`;
    const hover = s.op === 'hover';
    const t = await this.pointAndClick(s, el, 'action', !hover);
    if (hover) {
      t.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
      t.dispatchEvent(new MouseEvent('mouseenter', { bubbles: false }));
      return null;
    }
    click(t);
    if (s.op === 'double_click') {
      await sleep(80);
      await this.cursor.click();
      click(t);
      t.dispatchEvent(new MouseEvent('dblclick', { bubbles: true, cancelable: true, view: window }));
    }
    return this.expectEffect(s, dialogsBefore, pathBefore);
  }

  private async expectEffect(s: UIStep, dialogsBefore: number, pathBefore: string): Promise<string | null> {
    const e = s.expect ?? '';
    if (e === 'none') { await sleep(400); return null; }
    if (e === 'dialog') {
      return (await waitFor(() => openDialogs().length > dialogsBefore, 5000)) ? null : `form ${s.target} did not open`;
    }
    if (e.startsWith('route:')) {
      const prefix = e.slice(6);
      return (await waitFor(() => location.pathname.startsWith(prefix), 5000)) ? null : `the page ${prefix} did not open`;
    }
    // legacy: the click opens the dialog named like the action
    const keys = [s.target, ...(s.alts ?? [])].filter(Boolean) as string[];
    const opened = await waitFor(() => keys.some((t) => dialogScope(t)) || (location.pathname !== pathBefore && keys.length === 0), 4000);
    return opened ? null : `form ${s.target} did not open`;
  }

  // ---- typing / choosing: adapts to what the control actually is ----
  private async doField(s: UIStep): Promise<string | null> {
    const kind = s.row ? 'cell' : 'field';
    const el = await this.resolve(s, kind);
    if (!el) return s.optional ? null : `${s.op === 'combo' ? 'picker' : 'field'} ${s.target} not found`;
    await this.pointAndClick(s, el, kind);
    const value = s.value ?? '';
    // choose the driver from the control that is really there, not only from the plan
    if (el instanceof HTMLSelectElement) {
      if (!(await waitFor(() => Array.from(el.options).some((o) => o.value === value), 4000))) return `option ${value} is not available in ${s.target}`;
      return selectValue(el, value) ? null : `could not choose ${value} in ${s.target}`;
    }
    if (el instanceof HTMLInputElement && el.type === 'checkbox') {
      return setChecked(el, value === 'true') ? null : `checkbox ${s.target} did not change`;
    }
    const input = el as HTMLInputElement;
    if (s.op === 'combo' || input.getAttribute('role') === 'combobox') {
      return (await comboPick(input, s.search ?? '', value)) ? null : `could not find "${s.search ?? ''}" in ${s.target}`;
    }
    click(input); // a real click into the field, then typing
    if (s.op === 'date') return (await setDate(input, value)) ? null : `date ${value} was not accepted`;
    await type(input, value, this.cursor.profile().perChar);
    blur(input);
    await sleep(40);
    const got = digitsToAscii(input.value).replace(/[,٬\s]/g, '');
    const want = value.replace(/[,\s]/g, '');
    return got === want || input.value === value ? null : `field ${s.target} shows "${input.value}" instead of "${value}"`;
  }

  // ---- open a list row (a record) ----
  private async doOpenRow(s: UIStep): Promise<string | null> {
    const id = s.value!;
    const dialogsBefore = openDialogs().length;
    const pathBefore = location.pathname;
    const row = await waitFor(() => (pageScope() as ParentNode).querySelector<HTMLElement>(`tr[data-ai-row-id="${CSS.escape(id)}"], [data-ai-row-id="${CSS.escape(id)}"]`), 6000);
    if (!row) return `record ${id} is not on this list (it may be on another page or filtered out)`;
    const cell = row.querySelector<HTMLElement>('td:not(:first-child)') ?? row;
    await this.cursor.to(cell, s.label);
    await this.cursor.click();
    click(cell);
    return this.expectEffect({ ...s, target: 'row ' + id }, dialogsBefore, pathBefore);
  }

  // ---- make sure a line grid has N rows by pressing its own "add line" button ----
  private async doEnsureRows(s: UIStep): Promise<string | null> {
    const want = Number(s.value ?? '1');
    for (let guard = 0; gridRows().length < want && guard < want + 3; guard++) {
      const add = actionControl(pageScope(), [s.target!]);
      if (!add) return `button ${s.target} not found`;
      const before = gridRows().length;
      await this.pointAndClick(s, add, 'action');
      click(add);
      if (!(await waitFor(() => gridRows().length > before, 3000))) return 'the new line did not appear';
    }
    return null;
  }

  // ---- submit ----
  private async doSubmit(s: UIStep): Promise<string | null> {
    let el = await this.resolve({ ...s, op: 'click' }, 'action');
    if (!el && s.dialog === '*') {
      // the dialog's primary button when the app's tag is missing
      const d = dialogScope('*') as HTMLElement | null;
      el = d?.querySelector<HTMLElement>('.dialog-foot .btn-accent, .dialog-foot button:last-child') ?? null;
    }
    if (!el) return `button ${s.target} not found`;
    if ((el as HTMLButtonElement).disabled) return `button ${s.target} is disabled (permission, or the form is incomplete)`;
    this.pathAtSubmit = location.pathname;
    this.submitDialog = s.dialog === '*' ? (dialogScope('*') as HTMLElement | null) : null;
    await this.pointAndClick(s, el, 'action');
    click(el);
    return null;
  }

  private validationMessage(scope: ParentNode | null): string | undefined {
    return (scope as Element | null)?.querySelector('.field-error, .alert, [role="alert"]')?.textContent?.trim() || undefined;
  }

  private async doWaitClosed(s: UIStep): Promise<string | null> {
    const dlg = this.submitDialog ?? (s.dialog && s.dialog !== '*' ? (dialogScope(s.dialog, s.alts) as HTMLElement | null) : null);
    const closed = await waitFor(() => (dlg ? !dlg.isConnected || !isVisible(dlg) : openDialogs().length === 0) || null, 10000);
    if (closed) return null;
    const msg = this.validationMessage(dlg ?? (dialogScope('*') as ParentNode | null));
    return msg ? `the form reported: ${msg}` : 'the form did not close after saving';
  }

  /** Reads the result from the screen itself: the saved record must be visible on the page. */
  private async doVerifyText(s: UIStep): Promise<string | null> {
    const want = norm(s.value ?? '').toLowerCase();
    const seen = () => {
      const text = norm(((pageScope() as Element).textContent ?? '')).toLowerCase();
      return text.includes(want) ? true : null;
    };
    // look at the row that shows it, as a person would
    const row = await waitFor(() => Array.from((pageScope() as ParentNode).querySelectorAll<HTMLElement>('tr, li, [role="row"]')).find((r) => norm(r.textContent ?? '').toLowerCase().includes(want)) ?? null, 8000);
    if (row) await this.cursor.to(row, s.label).catch(() => undefined);
    return (await waitFor(seen, 8000)) ? null : `“${s.value}” is not visible on the page after saving`;
  }

  /**
   * After a preview the form belongs to the user. Resolves once they close it — after
   * pressing its own save button (saved) or not — or after maxMs. Never clicks anything;
   * the server decides from the application whether a record was really saved.
   */
  async watchUserSave(skipped: UIStep[], maxMs = 30 * 60_000): Promise<{ closed: boolean; saved: boolean }> {
    const submit = skipped.find((s) => s.op === 'submit');
    const leave = skipped.some((s) => s.op === 'wait_leave');
    const from = location.pathname;
    const scoped = submit?.dialog ? (dialogScope(submit.dialog, submit.alts) as HTMLElement | null) : null;
    const dlg = leave ? null : (scoped && scoped !== (document as unknown) ? scoped : openDialogs().at(-1) ?? null);
    let saved = false;
    const onClick = (e: Event) => {
      const el = e.target instanceof Element ? e.target.closest('button, [role="button"], input[type="submit"]') : null;
      if (!el) return;
      const btn = submit ? this.finder(submit, 'action')() : null;
      if ((btn && (btn === el || btn.contains(el))) || (el as HTMLButtonElement).type === 'submit' || el.matches('.dialog-foot .btn-accent')) saved = true;
    };
    const onSubmit = () => { saved = true; };
    document.addEventListener('click', onClick, true);
    document.addEventListener('submit', onSubmit, true);
    try {
      const gone = () => (leave ? location.pathname !== from : dlg ? !dlg.isConnected || !isVisible(dlg) : openDialogs().length === 0) || null;
      const closed = !!(await waitFor(gone, maxMs));
      return { closed, saved };
    } finally {
      document.removeEventListener('click', onClick, true);
      document.removeEventListener('submit', onSubmit, true);
    }
  }

  private async doWaitLeave(_s: UIStep): Promise<string | null> {
    const from = this.pathAtSubmit || location.pathname;
    if (await waitFor(() => (location.pathname !== from ? true : null), 10000)) return null;
    const msg = this.validationMessage(pageScope());
    return msg ? `the form reported: ${msg}` : 'the page did not leave the form after saving';
  }
}

/**
 * Executes one human-UI action: resolve the real element → scroll → pointer → real
 * interaction → observe. This is the only function features and plans call.
 */
export function executeHumanUIAction(engine: HumanUIEngine, step: UIStep): Promise<string | null> {
  return engine.execute(step);
}
