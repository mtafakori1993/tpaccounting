// Exact decimal arithmetic for amounts (strings in, strings out). The backend
// is authoritative; these mirror its rules so previews match exactly.
import Big from 'big.js';

Big.RM = Big.roundHalfUp; // half away from zero, same as the server

export type Amount = string;

export function toBig(v: string | number | null | undefined): Big {
  if (v === null || v === undefined || v === '') return new Big(0);
  try {
    return new Big(typeof v === 'number' ? v : v.replace(/,/g, ''));
  } catch {
    return new Big(0);
  }
}

export const isValidNumber = (v: string) => /^-?\d+(\.\d+)?$/.test(v.trim());
export const add = (...vs: (string | number)[]) => vs.reduce<Big>((s, v) => s.plus(toBig(v)), new Big(0)).toString();
export const sub = (a: string | number, b: string | number) => toBig(a).minus(toBig(b)).toString();
export const isZero = (v: string | number) => toBig(v).eq(0);
export const isNeg = (v: string | number) => toBig(v).lt(0);
export const cmp = (a: string | number, b: string | number) => toBig(a).cmp(toBig(b));
export const abs = (v: string | number) => toBig(v).abs().toString();
export const neg = (v: string | number) => toBig(v).times(-1).toString();

export interface LineCalc {
  gross: string;
  discount: string;
  net: string;
  tax: string;
  total: string;
}

/** Invoice line: tax is charged on the discounted amount. */
export function calcLine(qty: string, price: string, discount: string, rate: string, places: number): LineCalc {
  const gross = toBig(qty).times(toBig(price)).round(places);
  let disc = toBig(discount).round(places);
  if (disc.gt(gross)) disc = gross;
  const net = gross.minus(disc);
  const tax = net.times(toBig(rate)).div(100).round(places);
  return { gross: gross.toString(), discount: disc.toString(), net: net.toString(), tax: tax.toString(), total: net.plus(tax).toString() };
}

export function sumLines(lines: LineCalc[]) {
  const t = { gross: new Big(0), discount: new Big(0), net: new Big(0), tax: new Big(0), total: new Big(0) };
  for (const l of lines) {
    t.gross = t.gross.plus(l.gross);
    t.discount = t.discount.plus(l.discount);
    t.net = t.net.plus(l.net);
    t.tax = t.tax.plus(l.tax);
    t.total = t.total.plus(l.total);
  }
  return { gross: t.gross.toString(), discount: t.discount.toString(), net: t.net.toString(), tax: t.tax.toString(), total: t.total.toString() };
}

/** Parses a discount typed as an amount ("50000") or percentage ("10%") of qty × price. */
export function parseDiscount(input: string, qty: string, price: string, places: number): string {
  const s = input.trim().replace(/,/g, '');
  if (s.endsWith('%')) {
    const pct = s.slice(0, -1).trim();
    if (!isValidNumber(pct)) return '0';
    return toBig(qty).times(toBig(price)).times(toBig(pct)).div(100).round(places).toString();
  }
  return isValidNumber(s) ? s : '0';
}
