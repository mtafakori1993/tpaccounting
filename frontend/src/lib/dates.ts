// Calendar-aware date helpers. Dates travel as ISO strings (YYYY-MM-DD);
// they are displayed and typed in the company calendar (Jalali or Gregorian).
import { jalaliMonthLength, toGregorian, toJalali } from './jalali';

export type Calendar = 'jalali' | 'gregorian';

const pad = (n: number) => String(n).padStart(2, '0');

export function isoFromParts(y: number, m: number, d: number) {
  return `${y}-${pad(m)}-${pad(d)}`;
}

export function parseIso(iso: string) {
  const [y, m, d] = iso.slice(0, 10).split('-').map(Number);
  return { y, m, d };
}

export function todayIso() {
  const t = new Date();
  return isoFromParts(t.getFullYear(), t.getMonth() + 1, t.getDate());
}

export function addDaysIso(iso: string, days: number) {
  const { y, m, d } = parseIso(iso);
  const dt = new Date(Date.UTC(y, m - 1, d + days));
  return isoFromParts(dt.getUTCFullYear(), dt.getUTCMonth() + 1, dt.getUTCDate());
}

/** Parts of an ISO date in the given calendar. */
export function calendarParts(iso: string, cal: Calendar) {
  const { y, m, d } = parseIso(iso);
  if (cal === 'jalali') {
    const j = toJalali(y, m, d);
    return { y: j.jy, m: j.jm, d: j.jd };
  }
  return { y, m, d };
}

export function isoFromCalendar(y: number, m: number, d: number, cal: Calendar) {
  if (cal === 'jalali') {
    const g = toGregorian(y, m, d);
    return isoFromParts(g.gy, g.gm, g.gd);
  }
  return isoFromParts(y, m, d);
}

export function monthLength(y: number, m: number, cal: Calendar) {
  if (cal === 'jalali') return jalaliMonthLength(y, m);
  return new Date(Date.UTC(y, m, 0)).getUTCDate();
}

/** Formats an ISO date as YYYY/MM/DD in the calendar (ASCII digits). */
export function formatCalendarDate(iso: string | null | undefined, cal: Calendar) {
  if (!iso) return '';
  const p = calendarParts(iso, cal);
  return `${p.y}/${pad(p.m)}/${pad(p.d)}`;
}

/** Parses user input like 1405/7/14 or 2026-10-06 into ISO, or null if invalid. */
export function parseCalendarDate(text: string, cal: Calendar): string | null {
  const s = normalizeDigits(text).trim().replace(/[-.\s]/g, '/');
  const m = s.match(/^(\d{2,4})\/(\d{1,2})\/(\d{1,2})$/);
  if (!m) return null;
  let y = Number(m[1]);
  const mo = Number(m[2]);
  const d = Number(m[3]);
  if (y < 100) y += cal === 'jalali' ? 1400 : 2000;
  if (mo < 1 || mo > 12 || d < 1 || d > monthLength(y, mo, cal)) return null;
  return isoFromCalendar(y, mo, d, cal);
}

/** Start and end of the calendar month and year containing iso. */
export function monthRange(iso: string, cal: Calendar) {
  const p = calendarParts(iso, cal);
  return { from: isoFromCalendar(p.y, p.m, 1, cal), to: isoFromCalendar(p.y, p.m, monthLength(p.y, p.m, cal), cal) };
}

export function yearRange(iso: string, cal: Calendar) {
  const p = calendarParts(iso, cal);
  return { from: isoFromCalendar(p.y, 1, 1, cal), to: isoFromCalendar(p.y, 12, monthLength(p.y, 12, cal), cal) };
}

const persianDigits = '۰۱۲۳۴۵۶۷۸۹';
const arabicDigits = '٠١٢٣٤٥٦٧٨٩';

/** Converts Persian/Arabic digits and separators to ASCII. */
export function normalizeDigits(s: string) {
  return s
    .replace(/[۰-۹]/g, (c) => String(persianDigits.indexOf(c)))
    .replace(/[٠-٩]/g, (c) => String(arabicDigits.indexOf(c)))
    .replace(/[٫]/g, '.')
    .replace(/[٬،]/g, ',');
}

export function toPersianDigits(s: string) {
  return s.replace(/\d/g, (c) => persianDigits[Number(c)]);
}

const jalaliMonthsFa = ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور', 'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند'];
const jalaliMonthsEn = ['Farvardin', 'Ordibehesht', 'Khordad', 'Tir', 'Mordad', 'Shahrivar', 'Mehr', 'Aban', 'Azar', 'Dey', 'Bahman', 'Esfand'];

/** Month names of the calendar in the UI language. */
export function monthNames(cal: Calendar | string, lang: string) {
  if (cal === 'jalali') return lang === 'fa' ? jalaliMonthsFa : jalaliMonthsEn;
  return Array.from({ length: 12 }, (_, i) => new Intl.DateTimeFormat(lang === 'fa' ? 'fa-IR' : 'en-US', { month: 'long' }).format(new Date(2024, i, 1)));
}
