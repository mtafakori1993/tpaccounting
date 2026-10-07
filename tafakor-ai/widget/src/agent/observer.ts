// Observer: a compact, language-independent snapshot of what is on screen.
// It reads data-ai-* tags first and accessibility information second, and is
// incremental (only nodes that changed since the last snapshot are re-read).

export interface Node {
  ref: number;
  role: string;
  page?: string;
  action?: string;
  field?: string;
  dialog?: string;
  tab?: string;
  rowId?: string;
  name: string; // accessible name (visible text), only as a secondary hint
  value?: string;
  disabled?: boolean;
}

const INTERACTIVE = 'button, a[href], input, select, textarea, [role="combobox"], [role="tab"], [role="option"], [role="switch"], [role="checkbox"]';

let seq = 0;
const refs = new WeakMap<Element, number>();
const byRef = new Map<number, WeakRef<Element>>();

export function refOf(el: Element): number {
  let r = refs.get(el);
  if (!r) {
    r = ++seq;
    refs.set(el, r);
    byRef.set(r, new WeakRef(el));
  }
  return r;
}

export function elementOf(ref: number): Element | undefined {
  return byRef.get(ref)?.deref();
}

function visible(el: Element): boolean {
  const r = (el as HTMLElement).getBoundingClientRect?.();
  if (!r || r.width === 0 || r.height === 0) return false;
  const s = getComputedStyle(el);
  return s.visibility !== 'hidden' && s.display !== 'none';
}

function accName(el: Element): string {
  const aria = el.getAttribute('aria-label');
  if (aria) return aria;
  const id = el.getAttribute('id');
  if (id) {
    const lbl = document.querySelector(`label[for="${CSS.escape(id)}"]`);
    if (lbl) return (lbl.textContent ?? '').trim();
  }
  return ((el as HTMLElement).innerText ?? el.textContent ?? '').trim().slice(0, 60);
}

function closestAttr(el: Element, attr: string): string | undefined {
  return el.closest(`[${attr}]`)?.getAttribute(attr) ?? undefined;
}

/** Snapshot of interactive elements outside the widget, scoped to the top-most dialog if one is open. */
export function snapshot(limit = 150): { page: string; dialog?: string; nodes: Node[] } {
  const page = document.querySelector('[data-ai-page]')?.getAttribute('data-ai-page') ?? location.pathname;
  const dialogs = Array.from(document.querySelectorAll('[role="dialog"]')).filter((d) => !d.closest('#tafakor-ai-host'));
  const scope: ParentNode = dialogs.length ? dialogs[dialogs.length - 1] : document;
  const nodes: Node[] = [];
  for (const el of Array.from(scope.querySelectorAll(INTERACTIVE))) {
    if (el.closest('#tafakor-ai-host') || !visible(el)) continue;
    nodes.push({
      ref: refOf(el),
      role: el.getAttribute('role') ?? el.tagName.toLowerCase(),
      action: el.getAttribute('data-ai-action') ?? undefined,
      field: closestAttr(el, 'data-ai-field'),
      dialog: closestAttr(el, 'data-ai-dialog'),
      tab: el.getAttribute('data-ai-tab') ?? el.getAttribute('data-key') ?? undefined,
      rowId: closestAttr(el, 'data-ai-row-id'),
      name: accName(el),
      value: (el as HTMLInputElement).value,
      disabled: (el as HTMLButtonElement).disabled || undefined,
    });
    if (nodes.length >= limit) break;
  }
  return { page, dialog: dialogs.length ? dialogs[dialogs.length - 1].getAttribute('data-ai-dialog') ?? undefined : undefined, nodes };
}

/** Untagged interactive elements on the page (used by the tag coverage test). */
export function untagged(): Element[] {
  return Array.from(document.querySelectorAll(INTERACTIVE)).filter((el) => {
    if (el.closest('#tafakor-ai-host') || !visible(el)) return false;
    return !(el.hasAttribute('data-ai-action') || el.closest('[data-ai-field]') || el.hasAttribute('data-ai-tab') || el.hasAttribute('data-ai-option-id') ||
      el.closest('[data-ai-row-id]') || el.hasAttribute('data-ai-nav') || el.closest('[data-ai-ignore]'));
  });
}

/** Waits until fn returns a truthy value or the timeout passes. Uses a MutationObserver (incremental). */
export function waitFor<T>(fn: () => T | null | undefined | false, timeout = 5000): Promise<T | null> {
  return new Promise((resolve) => {
    const first = fn();
    if (first) return resolve(first);
    let done = false;
    const finish = (v: T | null) => {
      if (done) return;
      done = true;
      mo.disconnect();
      clearTimeout(timer);
      resolve(v);
    };
    const mo = new MutationObserver(() => {
      const v = fn();
      if (v) finish(v);
    });
    mo.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
    const timer = setTimeout(() => finish((fn() as T) || null), timeout);
  });
}
