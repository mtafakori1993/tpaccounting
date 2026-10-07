import { describe, expect, it } from 'vitest';
import { calcLine, parseDiscount, sumLines } from './decimal';
import { formatCalendarDate, parseCalendarDate } from './dates';
import { toGregorian, toJalali } from './jalali';

describe('invoice math mirrors the server', () => {
  it('charges tax on the discounted amount', () => {
    expect(calcLine('4', '1500000', '100000', '10', 0)).toEqual({ gross: '6000000', discount: '100000', net: '5900000', tax: '590000', total: '6490000' });
  });
  it('rounds half away from zero', () => {
    expect(calcLine('1', '15', '0', '10', 0).tax).toBe('2');
  });
  it('sums lines', () => {
    const t = sumLines([calcLine('4', '1500000', '100000', '10', 0), calcLine('2', '500000', '0', '10', 0)]);
    expect(t.total).toBe('7590000');
  });
  it('parses percentage discounts', () => {
    expect(parseDiscount('10%', '3', '1000', 0)).toBe('300');
    expect(parseDiscount('250', '3', '1000', 0)).toBe('250');
  });
});

describe('jalali calendar', () => {
  it('converts both ways', () => {
    expect(toJalali(2026, 10, 6)).toEqual({ jy: 1405, jm: 7, jd: 14 });
    expect(toGregorian(1403, 12, 30)).toEqual({ gy: 2025, gm: 3, gd: 20 });
  });
  it('parses typed dates with Persian digits', () => {
    expect(parseCalendarDate('۱۴۰۵/۷/۱۴', 'jalali')).toBe('2026-10-06');
    expect(parseCalendarDate('1404/12/30', 'jalali')).toBeNull(); // 1404 is not leap
    expect(formatCalendarDate('2026-10-06', 'jalali')).toBe('1405/07/14');
    expect(parseCalendarDate('2026-10-06', 'gregorian')).toBe('2026-10-06');
  });
});
