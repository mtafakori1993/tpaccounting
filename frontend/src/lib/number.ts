// The application's single number & money formatting system.
//
// Formatting is presentation only: values travel to and from the API as plain
// decimal strings ("1250000", "-12.5") and every calculation uses big.js
// (lib/decimal.ts). These functions turn such values into display text and
// turn anything a user types or pastes back into a plain decimal string. They
// work on strings end to end, so amounts never lose precision through
// JavaScript floating point.
//
// Components should not call these directly: use useFormat() (i18n/format.ts)
// for text, <Money>/<FormattedNumber> for display and <NumericInput>/
// <MoneyInput>/<PercentageInput> for entry. Identifiers made of digits
// (invoice/check/account numbers, codes, IBANs) are never grouped: show them
// with f.digits() only.
import Big from 'big.js';

export type NumberLang = 'fa' | 'en';

interface Symbols {
  group: string;
  decimal: string;
  minus: string;
  persianDigits: boolean;
}

/** Persian uses Persian digits with the Arabic thousands (٬) and decimal (٫) separators, as Intl's fa-IR does. */
export const SYMBOLS: Record<NumberLang, Symbols> = {
  fa: { group: '٬', decimal: '٫', minus: '-', persianDigits: true },
  en: { group: ',', decimal: '.', minus: '-', persianDigits: false },
};

const PERSIAN = '۰۱۲۳۴۵۶۷۸۹';
const ARABIC = '٠١٢٣٤٥٦٧٨٩';

export function toLatinDigits(s: string) {
  return s.replace(/[۰-۹]/g, (c) => String(PERSIAN.indexOf(c))).replace(/[٠-٩]/g, (c) => String(ARABIC.indexOf(c)));
}

export function toLocaleDigits(s: string, lang: NumberLang) {
  return SYMBOLS[lang].persianDigits ? s.replace(/\d/g, (c) => PERSIAN[Number(c)]) : s;
}

const CANONICAL = /^-?\d+(\.\d+)?$/;

/** True for a plain decimal string as used by the API. */
export const isCanonical = (s: string) => CANONICAL.test(s);

export interface ParseOptions {
  /** maximum fraction digits; with 0 every separator is read as grouping */
  maxFraction?: number;
  allowNegative?: boolean;
}

/**
 * Parses typed or pasted numeric text into a plain decimal string.
 *
 * Accepts Latin, Persian and Arabic digits; "," "٬" "'" "’" and spaces as
 * thousands separators; "." "٫" as decimal separators; and European
 * "1.250.000,50". When both "," and "." occur, the last one is the decimal
 * separator. A single "," is grouping (1,250 = 1250); repeated "." is grouping
 * (1.250.000). Returns '' for empty input and null when the text is not a number.
 */
export function parseFormattedNumber(input: string | number | null | undefined, opts: ParseOptions = {}): string | null {
  if (input === null || input === undefined) return '';
  if (typeof input === 'number') {
    if (!Number.isFinite(input)) return null;
    input = String(input);
  }
  const { maxFraction = 20, allowNegative = true } = opts;
  let s = toLatinDigits(input)
    .replace(/[‎‏‪-‮⁦-⁩]/g, '') // bidi marks from copied text
    .replace(/[\s  '’٬]/g, '')
    .replace(/٫/g, '.')
    .replace(/[−–]/g, '-');
  if (s === '') return '';
  let negative = false;
  if (s.startsWith('-')) {
    negative = true;
    s = s.slice(1);
  } else if (s.endsWith('-')) {
    // RTL copy-paste can move the sign to the end.
    negative = true;
    s = s.slice(0, -1);
  } else if (/^\(.*\)$/.test(s)) {
    negative = true; // accounting negative (1,250)
    s = s.slice(1, -1);
  }
  if (!/^[\d.,]*$/.test(s) || !/\d/.test(s)) return null;
  const commas = (s.match(/,/g) ?? []).length;
  const dots = (s.match(/\./g) ?? []).length;
  let intPart = s;
  let frac = '';
  if (commas && dots) {
    const dec = s.lastIndexOf('.') > s.lastIndexOf(',') ? '.' : ',';
    const grp = dec === '.' ? ',' : '.';
    if ((s.match(new RegExp(`\\${dec}`, 'g')) ?? []).length > 1) return null;
    [intPart, frac] = s.replace(new RegExp(`\\${grp}`, 'g'), '').split(dec);
  } else if (dots > 1) {
    intPart = s.replace(/\./g, '');
  } else if (dots === 1) {
    [intPart, frac] = s.split('.');
    if (maxFraction === 0) {
      // "1.250" in a whole-number field is grouping; "12.5" is not a whole number.
      if (frac.length === 3) {
        intPart += frac;
        frac = '';
      } else return null;
    }
  } else if (commas) {
    intPart = s.replace(/,/g, '');
  }
  if (intPart === '' && frac === '') return null;
  if (frac.length > maxFraction) return null;
  intPart = intPart.replace(/^0+(?=\d)/, '') || '0';
  const out = frac ? `${intPart}.${frac}` : intPart;
  if (negative && !allowNegative) return null;
  return negative && /[1-9]/.test(out) ? `-${out}` : out;
}

/** Inserts the group separator every three digits of an integer string. */
export function groupDigits(intDigits: string, sep: string) {
  return intDigits.replace(/\B(?=(\d{3})+(?!\d))/g, sep);
}

export interface FormatOptions {
  lang?: NumberLang;
  /** fraction digits shown at least (padded with zeros) */
  minFraction?: number;
  /** fraction digits shown at most (rounded half away from zero) */
  maxFraction?: number;
  /** thousands grouping (default true) */
  group?: boolean;
}

function toBigSafe(v: string | number | Big): Big | null {
  if (v instanceof Big) return v;
  const s = typeof v === 'number' ? (Number.isFinite(v) ? String(v) : null) : parseFormattedNumber(v);
  if (s === null || s === '') return null;
  try {
    return new Big(s);
  } catch {
    return null;
  }
}

/**
 * Formats a number for display. Idempotent: already formatted text (including
 * Persian digits and separators) is parsed first, so formatting twice never
 * corrupts a value. Empty/invalid input returns ''.
 */
export function formatNumber(value: string | number | Big | null | undefined, opts: FormatOptions = {}): string {
  if (value === null || value === undefined || value === '') return '';
  const { lang = 'en', minFraction = 0, maxFraction = 3, group = true } = opts;
  const b = toBigSafe(value);
  if (!b) return typeof value === 'string' ? value : '';
  const fixed = b.round(maxFraction, Big.roundHalfUp).toFixed(maxFraction);
  let [i, f = ''] = fixed.replace('-', '').split('.');
  f = f.replace(/0+$/, '');
  while (f.length < minFraction) f += '0';
  const negative = fixed.startsWith('-') && /[1-9]/.test(fixed);
  const sym = SYMBOLS[lang];
  const body = (group ? groupDigits(i, sym.group) : i) + (f ? sym.decimal + f : '');
  return toLocaleDigits((negative ? sym.minus : '') + body, lang);
}

export const formatInteger = (v: string | number | Big | null | undefined, lang: NumberLang = 'en') =>
  formatNumber(v, { lang, maxFraction: 0 });

export const formatDecimal = (v: string | number | Big | null | undefined, places: number, lang: NumberLang = 'en') =>
  formatNumber(v, { lang, minFraction: places, maxFraction: places });

/** Currency labels by ISO code; unknown codes are shown as the code itself. */
const CURRENCY: Record<string, Record<NumberLang, string>> = {
  IRR: { fa: 'ریال', en: 'IRR' },
  IRT: { fa: 'تومان', en: 'Toman' },
  USD: { fa: 'دلار', en: 'USD' },
  EUR: { fa: 'یورو', en: 'EUR' },
};

export const currencyLabel = (code: string | undefined, lang: NumberLang) => (code ? CURRENCY[code]?.[lang] ?? code : '');

export interface MoneyOptions {
  lang?: NumberLang;
  /** the company's amount decimals */
  decimals?: number;
  /** ISO currency code to append (omit for table cells) */
  currency?: string;
}

/** 1250000000 → "1,250,000,000" (or "1,250,000,000 IRR" with a currency). */
export function formatMoney(value: string | number | Big | null | undefined, opts: MoneyOptions = {}): string {
  const { lang = 'en', decimals = 0, currency } = opts;
  const s = formatNumber(value ?? 0, { lang, minFraction: decimals, maxFraction: decimals });
  return currency ? `${s} ${currencyLabel(currency, lang)}` : s;
}

/** A value that is already a percentage: 12.5 → "12.5%" ("۱۲٫۵٪" in Persian). */
export function formatPercentage(value: string | number | Big | null | undefined, opts: { lang?: NumberLang; maxFraction?: number } = {}): string {
  if (value === null || value === undefined || value === '') return '';
  const { lang = 'en', maxFraction = 1 } = opts;
  const n = formatNumber(value, { lang, maxFraction });
  if (n === '') return '';
  return lang === 'fa' ? `${n}٪` : `${n}%`;
}

// ---------- Editing support for numeric inputs ----------

const SIGNIFICANT = /[0-9۰-۹٠-٩.٫\-]/;

/** Position in text right after the n-th significant character. */
function positionAfter(text: string, n: number) {
  if (n <= 0) {
    // Keep the caret after a leading minus sign that has no digits yet.
    return 0;
  }
  let seen = 0;
  for (let i = 0; i < text.length; i++) {
    if (SIGNIFICANT.test(text[i])) {
      seen++;
      if (seen === n) return i + 1;
    }
  }
  return text.length;
}

export interface EditOptions {
  lang: NumberLang;
  maxFraction: number;
  allowNegative?: boolean;
}

export interface EditResult {
  /** canonical value for the API ('' when empty, or a partial like "-" / "12.") */
  raw: string;
  display: string;
  caret: number;
}

/**
 * Re-formats an input's text after an edit while keeping the caret next to
 * the same digit. Returns null when the edit must be rejected (e.g. a letter
 * or too many decimals), in which case the input keeps its previous text.
 * Partial values that are valid while typing ("-", "12.", "0.0") are kept.
 */
export function applyNumericEdit(text: string, caret: number, opts: EditOptions): EditResult | null {
  const sym = SYMBOLS[opts.lang];
  const latin = toLatinDigits(text).replace(/٫/g, '.');
  // While typing, the locale group separator and spaces are noise; "," in Persian
  // input or "." are decimal points only if the field allows fractions.
  let cleaned = latin.replace(/[\s '’٬]/g, '');
  if (sym.group !== ',') cleaned = cleaned.replace(/,/g, opts.maxFraction > 0 ? '.' : '');
  else cleaned = cleaned.replace(/,/g, '');
  if (cleaned === '') return { raw: '', display: '', caret: 0 };
  const allowNegative = opts.allowNegative ?? false;
  if (cleaned === '-') return allowNegative ? { raw: '-', display: sym.minus, caret: 1 } : null;
  const m = cleaned.match(/^(-?)(\d*)(?:\.(\d*))?$/);
  if (!m) return null;
  const [, minus, intRaw, fracRaw] = m;
  if (minus && !allowNegative) return null;
  if (fracRaw !== undefined && opts.maxFraction === 0) return null;
  if (fracRaw !== undefined && fracRaw.length > opts.maxFraction) return null;
  const intDigits = intRaw.replace(/^0+(?=\d)/, '');
  const hasPoint = fracRaw !== undefined;
  const intOut = intDigits === '' ? (hasPoint ? '0' : '') : intDigits;
  const raw = `${minus}${intOut}${hasPoint ? `.${fracRaw}` : ''}`;
  const display = toLocaleDigits(`${minus ? sym.minus : ''}${groupDigits(intOut, sym.group)}${hasPoint ? sym.decimal + fracRaw : ''}`, opts.lang);
  // Count the meaningful characters before the caret in what the user typed,
  // then adjust for leading zeros removed / a "0" added before a bare point.
  const commaIsPoint = sym.group !== ',' && opts.maxFraction > 0;
  let sig = 0;
  for (let i = 0; i < caret && i < text.length; i++) if (SIGNIFICANT.test(text[i]) || (commaIsPoint && text[i] === ',')) sig++;
  const lead = minus ? 1 : 0;
  if (sig > lead) {
    const intBefore = Math.min(sig - lead, intRaw.length);
    sig -= Math.min(intRaw.length - intDigits.length, intBefore);
    if (intDigits === '' && hasPoint) sig += 1;
  }
  return { raw, display, caret: positionAfter(display, sig) };
}

/**
 * Backspace/Delete next to a group separator would only remove the separator
 * (which formatting puts straight back). This returns the text and caret with
 * the adjacent digit removed instead, or null when the key needs no special
 * handling.
 */
export function deleteAcrossSeparator(text: string, start: number, end: number, key: 'Backspace' | 'Delete', lang: NumberLang): { text: string; caret: number } | null {
  if (start !== end) return null;
  const sep = SYMBOLS[lang].group;
  if (key === 'Backspace' && start > 1 && text[start - 1] === sep) {
    return { text: text.slice(0, start - 2) + text.slice(start), caret: start - 2 };
  }
  if (key === 'Delete' && start < text.length - 1 && text[start] === sep) {
    return { text: text.slice(0, start) + text.slice(start + 2), caret: start };
  }
  return null;
}

/** Display text for a canonical value inside an input (grouped, locale digits, no rounding). */
export function inputDisplay(raw: string, lang: NumberLang): string {
  if (raw === '' || raw === null || raw === undefined) return '';
  const r = applyNumericEdit(raw, raw.length, { lang, maxFraction: 20, allowNegative: true });
  return r ? r.display : raw;
}
