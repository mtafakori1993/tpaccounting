// Executor: runs a server-issued UI plan step by step with the virtual cursor.
// Target order: data-ai-* tags → accessibility (role + label) → vision fallback
// (server picks among numbered candidates) — never long blind coordinate sequences.
// Every important step is verified; on any mismatch it stops and reports.

import { Cursor } from './cursor';
import { blur, click, comboPick, navigate, selectValue, setChecked, setDate, type, digitsToAscii } from './drivers';
import { snapshot, waitFor } from './observer';
import { locateByVision } from './vision';

export interface UIStep {
  op: 'navigate' | 'click' | 'fill' | 'select' | 'combo' | 'date' | 'tab' | 'check' | 'submit' | 'wait_closed';
  page?: string;
  dialog?: string;
  target?: string;
  value?: string;
  search?: string;
  label: string;
}

export interface UIPlan {
  task_id: string;
  steps: UIStep[];
}

export interface UIResult {
  ok: boolean;
  failed_step: number;
  error: string;
}

// Secondary (accessibility) names for the tags used by plans, in both languages.
// Used only when a tag is missing (e.g. an older build of the app).
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
  'common.saveAndPost': ['Save and post', 'ذخیره و ثبت قطعی'],
  'treasury.newReceipt': ['New receipt', 'دریافت جدید'],
  'treasury.newPayment': ['New payment', 'پرداخت جدید'],
  'expenses.newExpense': ['New expense', 'هزینه جدید'],
  'expenses.category': ['Category', 'دسته هزینه'],
  'expenses.paymentMethod': ['Paid from', 'محل پرداخت'],
  'invoices.supplier': ['Supplier', 'تأمین‌کننده'],
};

function norm(s: string) {
  return s.replace(/\s+/g, ' ').replace(/\*/g, '').trim();
}

function dialogScope(key?: string): ParentNode | null {
  // without a dialog, act inside the page content only (never the shell or menus)
  if (!key) return document.querySelector('main [data-ai-page], main[data-ai-page]') ?? document.querySelector('main') ?? document;
  const tagged = document.querySelector(`[data-ai-dialog="${CSS.escape(key)}"]`);
  if (tagged) return tagged;
  const names = LABELS[key] ?? [];
  for (const d of Array.from(document.querySelectorAll('[role="dialog"]'))) {
    const title = d.querySelector('.dialog-title, h2')?.textContent ?? '';
    if (names.some((n) => norm(title) === n)) return d;
  }
  return null;
}

/** Finds the control for a field: tag first, then the <label> text. */
function fieldControl(scope: ParentNode, key: string): HTMLElement | null {
  const wrap = scope.querySelector(`[data-ai-field="${CSS.escape(key)}"]`);
  const pick = (w: Element) => w.querySelector<HTMLElement>('input:not([type="hidden"]), select, textarea, [role="combobox"]');
  if (wrap) return wrap.matches('input,select,textarea') ? (wrap as HTMLElement) : pick(wrap);
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

function isVisible(el: Element) {
  const r = (el as HTMLElement).getBoundingClientRect();
  return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== 'hidden';
}

/** Visible tagged control; real buttons win over links (a link may reload the page). */
function actionControl(scope: ParentNode, key: string): HTMLElement | null {
  const tagged = Array.from(scope.querySelectorAll<HTMLElement>(`[data-ai-action="${CSS.escape(key)}"]`)).filter(isVisible);
  const best = tagged.find((e) => e.tagName === 'BUTTON') ?? tagged[0];
  if (best) return best;
  const names = LABELS[key] ?? [];
  return Array.from(scope.querySelectorAll<HTMLElement>('button, [role="button"]')).find((b) => isVisible(b) && names.includes(norm(b.textContent ?? ''))) ?? null;
}

async function resolve(step: UIStep, kind: 'field' | 'action' | 'tab'): Promise<HTMLElement | null> {
  const find = () => {
    const scope = dialogScope(step.dialog);
    if (!scope) return null;
    if (kind === 'field') return fieldControl(scope, step.target!);
    if (kind === 'tab') return scope.querySelector<HTMLElement>(`[role="tab"][data-ai-tab="${CSS.escape(step.target!)}"], [role="tab"][data-key="${CSS.escape(step.target!)}"]`);
    return actionControl(scope, step.target!);
  };
  const el = await waitFor(find, 4000);
  if (el) return el;
  // Vision fallback: the server looks at a screenshot + numbered candidates.
  return locateByVision(step, snapshot());
}

export async function runPlan(plan: UIPlan, cursor: Cursor, onStatus: (label: string) => void, aborted: () => boolean): Promise<UIResult> {
  cursor.show();
  try {
    for (let i = 0; i < plan.steps.length; i++) {
      if (aborted()) return { ok: false, failed_step: i, error: 'cancelled' };
      const s = plan.steps[i];
      onStatus(s.label);
      const err = await runStep(s, cursor);
      if (err) return { ok: false, failed_step: i, error: err };
    }
    return { ok: true, failed_step: -1, error: '' };
  } catch (e: any) {
    return { ok: false, failed_step: -1, error: String(e?.message ?? e) };
  } finally {
    setTimeout(() => cursor.hide(), 800);
  }
}

async function runStep(s: UIStep, cursor: Cursor): Promise<string | null> {
  switch (s.op) {
    case 'navigate': {
      if (!(await navigate(s.value!))) return `could not open ${s.value}`;
      if (s.page) {
        const ok = await waitFor(() => document.querySelector(`[data-ai-page="${CSS.escape(s.page!)}"]`) || location.pathname === s.value, 5000);
        if (!ok) return `page ${s.page} did not load`;
      }
      return null;
    }
    case 'click': {
      const el = await resolve(s, 'action');
      if (!el) return `button ${s.target} not found`;
      if ((el as HTMLButtonElement).disabled) return `button ${s.target} is disabled (permission?)`;
      await cursor.to(el, s.label);
      await cursor.click();
      click(el);
      const opened = await waitFor(() => dialogScope(s.target), 4000); // plans open the dialog named like the action
      return opened ? null : `form ${s.target} did not open`;
    }
    case 'tab': {
      const el = await resolve(s, 'tab');
      if (!el) return `tab ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      click(el);
      return (await waitFor(() => el.getAttribute('aria-selected') === 'true', 2000)) ? null : `tab ${s.target} not selected`;
    }
    case 'fill': {
      const el = (await resolve(s, 'field')) as HTMLInputElement | null;
      if (!el) return `field ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      await type(el, s.value!);
      blur(el);
      await new Promise((r) => setTimeout(r, 40));
      const got = digitsToAscii(el.value).replace(/[,٬\s]/g, '');
      const want = s.value!.replace(/[,\s]/g, '');
      return got === want || el.value === s.value ? null : `field ${s.target} shows "${el.value}" instead of "${s.value}"`;
    }
    case 'date': {
      const el = (await resolve(s, 'field')) as HTMLInputElement | null;
      if (!el) return `date field ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      return (await setDate(el, s.value!)) ? null : `date ${s.value} was not accepted`;
    }
    case 'select': {
      const el = (await resolve(s, 'field')) as HTMLSelectElement | null;
      if (!el || el.tagName !== 'SELECT') return `list ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      if (!(await waitFor(() => Array.from(el.options).some((o) => o.value === s.value), 4000))) return `option ${s.value} is not available in ${s.target}`;
      return selectValue(el, s.value!) ? null : `could not choose ${s.value} in ${s.target}`;
    }
    case 'combo': {
      const el = (await resolve(s, 'field')) as HTMLInputElement | null;
      if (!el) return `picker ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      return (await comboPick(el, s.search ?? '', s.value!)) ? null : `could not find "${s.search}" in ${s.target}`;
    }
    case 'check': {
      const el = (await resolve(s, 'field')) as HTMLInputElement | null;
      if (!el || el.type !== 'checkbox') return `checkbox ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      return setChecked(el, s.value === 'true') ? null : `checkbox ${s.target} did not change`;
    }
    case 'submit': {
      const el = await resolve(s, 'action');
      if (!el) return `button ${s.target} not found`;
      await cursor.to(el, s.label);
      await cursor.click();
      click(el);
      return null;
    }
    case 'wait_closed': {
      const closed = await waitFor(() => !dialogScope(s.dialog), 10000);
      if (closed) return null;
      // The form stayed open: report the app's own validation message.
      const scope = dialogScope(s.dialog) as Element | null;
      const msg = scope?.querySelector('.field-error, .alert, [role="alert"]')?.textContent?.trim();
      return msg ? `the form reported: ${msg}` : 'the form did not close after saving';
    }
  }
  return `unknown step ${(s as UIStep).op}`;
}
