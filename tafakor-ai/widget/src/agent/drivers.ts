// Drivers perform real DOM actions on the app's React components. React keeps
// its own value tracker, so values are written with the native setter and then
// the events React listens to are dispatched. Events are isTrusted=false, so the
// app's native file pickers are never driven (uploads go through the widget).

import { waitFor } from './observer';

export async function navigate(route: string): Promise<boolean> {
  if (!route.startsWith('/') || route.startsWith('//')) return false;
  if (location.pathname !== route) {
    history.pushState({}, '', route);
    window.dispatchEvent(new PopStateEvent('popstate', { state: {} }));
  }
  return !!(await waitFor(() => location.pathname === route, 3000));
}

function nativeSet(el: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
  const proto = el instanceof HTMLSelectElement ? HTMLSelectElement.prototype : el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, 'value')?.set;
  setter ? setter.call(el, value) : (el.value = value);
}

export function click(el: Element) {
  const o = { bubbles: true, cancelable: true, view: window };
  el.dispatchEvent(new PointerEvent('pointerdown', o));
  el.dispatchEvent(new MouseEvent('mousedown', o));
  el.dispatchEvent(new PointerEvent('pointerup', o));
  el.dispatchEvent(new MouseEvent('mouseup', o));
  (el as HTMLElement).click();
}

/** Types text into a controlled input (React onChange fires per input event). */
export async function type(el: HTMLInputElement | HTMLTextAreaElement, text: string, perChar = 18) {
  el.focus();
  nativeSet(el, '');
  el.dispatchEvent(new Event('input', { bubbles: true }));
  let acc = '';
  for (const ch of text) {
    acc += ch;
    nativeSet(el, acc);
    el.dispatchEvent(new InputEvent('input', { bubbles: true, data: ch, inputType: 'insertText' }));
    if (perChar) await new Promise((r) => setTimeout(r, perChar));
  }
  el.dispatchEvent(new Event('change', { bubbles: true }));
}

export function blur(el: HTMLElement) {
  el.dispatchEvent(new FocusEvent('blur', { bubbles: false }));
  el.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
  el.blur();
}

export function selectValue(el: HTMLSelectElement, value: string): boolean {
  if (!Array.from(el.options).some((o) => o.value === value)) return false;
  el.focus();
  nativeSet(el, value);
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return el.value === value;
}

export function setChecked(el: HTMLInputElement, checked: boolean) {
  if (el.checked !== checked) click(el);
  return el.checked === checked;
}

/** ComboBox driver: focus, type the search text, wait for the option with the
 *  wanted id (data-ai-option-id) and pick it. Verifies the chosen label. */
export async function comboPick(input: HTMLInputElement, search: string, optionId: string): Promise<boolean> {
  input.focus();
  click(input);
  await type(input, search, 25);
  const listId = input.getAttribute('aria-controls');
  const opt = await waitFor(() => {
    const scope = (listId && document.getElementById(listId)) || document;
    return scope.querySelector<HTMLElement>(`[role="option"][data-ai-option-id="${CSS.escape(optionId)}"]`);
  }, 5000);
  if (!opt) return false;
  click(opt);
  await waitFor(() => input.getAttribute('aria-expanded') === 'false', 1500);
  return true;
}

// ---------- Jalali (for DateField in the Jalali calendar) ----------

function div(a: number, b: number) {
  return Math.trunc(a / b);
}

export function gregorianToJalali(gy: number, gm: number, gd: number): [number, number, number] {
  const gdm = [0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334];
  const gy2 = gm > 2 ? gy + 1 : gy;
  let days = 355666 + 365 * gy + div(gy2 + 3, 4) - div(gy2 + 99, 100) + div(gy2 + 399, 400) + gd + gdm[gm - 1];
  let jy = -1595 + 33 * div(days, 12053);
  days %= 12053;
  jy += 4 * div(days, 1461);
  days %= 1461;
  if (days > 365) {
    jy += div(days - 1, 365);
    days = (days - 1) % 365;
  }
  const jm = days < 186 ? 1 + div(days, 31) : 7 + div(days - 186, 30);
  const jd = 1 + (days < 186 ? days % 31 : (days - 186) % 30);
  return [jy, jm, jd];
}

const pad = (n: number) => String(n).padStart(2, '0');

export function digitsToAscii(s: string) {
  return s.replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d))).replace(/[٠-٩]/g, (d) => String('٠١٢٣٤٥٦٧٨٩'.indexOf(d)));
}

/** DateField driver: types the date in the field's calendar (detected from its placeholder) and commits on blur. */
export async function setDate(input: HTMLInputElement, iso: string): Promise<boolean> {
  const [y, m, d] = iso.split('-').map(Number);
  const jalali = (input.placeholder ?? '').includes('/');
  const text = jalali ? (() => { const [jy, jm, jd] = gregorianToJalali(y, m, d); return `${jy}/${pad(jm)}/${pad(jd)}`; })() : iso;
  await type(input, text, 10);
  blur(input);
  await new Promise((r) => setTimeout(r, 50));
  return digitsToAscii(input.value).replace(/\s/g, '') === text && input.getAttribute('aria-invalid') !== 'true';
}
