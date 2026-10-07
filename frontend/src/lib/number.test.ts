import { describe, expect, it } from 'vitest';
import {
  applyNumericEdit, deleteAcrossSeparator, formatDecimal, formatInteger, formatMoney, formatNumber, formatPercentage, inputDisplay,
  parseFormattedNumber,
} from './number';

describe('formatNumber groups thousands', () => {
  it.each([
    ['1000', '1,000'], ['10000', '10,000'], ['100000', '100,000'], ['1000000', '1,000,000'], ['1250000000', '1,250,000,000'],
    ['1234', '1,234'], ['12345', '12,345'], ['123456', '123,456'], ['1234567', '1,234,567'], ['1234567890', '1,234,567,890'], ['999', '999'],
  ])('%s → %s', (v, want) => expect(formatNumber(v)).toBe(want));
  it('is exact beyond floating point', () => {
    expect(formatNumber('123456789012345678901')).toBe('123,456,789,012,345,678,901');
    expect(formatMoney('9007199254740993')).toBe('9,007,199,254,740,993');
  });
  it('handles negatives, zero and empty', () => {
    expect(formatNumber('-1000')).toBe('-1,000');
    expect(formatNumber(-1250000)).toBe('-1,250,000');
    expect(formatNumber('0')).toBe('0');
    expect(formatNumber('-0')).toBe('0');
    expect(formatNumber('')).toBe('');
    expect(formatNumber(null)).toBe('');
  });
  it('uses Persian digits and separators', () => {
    expect(formatNumber('1250000.5', { lang: 'fa' })).toBe('۱٬۲۵۰٬۰۰۰٫۵');
    expect(formatMoney('-1250000', { lang: 'fa' })).toBe('-۱٬۲۵۰٬۰۰۰');
  });
  it('is idempotent', () => {
    const once = formatNumber('1250000');
    expect(formatNumber(once)).toBe('1,250,000');
    expect(formatNumber(formatNumber('1250000', { lang: 'fa' }), { lang: 'fa' })).toBe('۱٬۲۵۰٬۰۰۰');
  });
  it('rounds decimals half away from zero', () => {
    expect(formatDecimal('1250000.505', 2)).toBe('1,250,000.51');
    expect(formatDecimal('1250000.5', 2)).toBe('1,250,000.50');
    expect(formatInteger('1250000.5')).toBe('1,250,001');
  });
});

describe('formatMoney and formatPercentage', () => {
  it('appends the currency', () => {
    expect(formatMoney('1250000000', { currency: 'IRR' })).toBe('1,250,000,000 IRR');
    expect(formatMoney('1250000000', { currency: 'IRR', lang: 'fa' })).toBe('۱٬۲۵۰٬۰۰۰٬۰۰۰ ریال');
    expect(formatMoney('12.5', { decimals: 2 })).toBe('12.50');
  });
  it('formats percentages', () => {
    expect(formatPercentage(15)).toBe('15%');
    expect(formatPercentage('12.5')).toBe('12.5%');
    expect(formatPercentage('72.54')).toBe('72.5%');
    expect(formatPercentage('12.5', { lang: 'fa' })).toBe('۱۲٫۵٪');
    expect(formatPercentage(null)).toBe('');
  });
});

describe('parseFormattedNumber', () => {
  it.each([
    ['1250000', '1250000'], ['1,250,000', '1250000'], ['1.250.000', '1250000'], ['1 250 000', '1250000'], ['۱۲۵۰۰۰۰', '1250000'],
    ['۱٬۲۵۰٬۰۰۰', '1250000'], ['1,250.50', '1250.50'], ['1.250,50', '1250.50'], ['۱۲۵۰٫۵', '1250.5'], ['-1,000', '-1000'], ['1,000-', '-1000'],
    ['(1,250)', '-1250'], ['0', '0'], ['007', '7'], [' 1 250 ', '1250'],
  ])('%s → %s', (v, want) => expect(parseFormattedNumber(v)).toBe(want));
  it('keeps empty distinct from zero', () => {
    expect(parseFormattedNumber('')).toBe('');
    expect(parseFormattedNumber('  ')).toBe('');
  });
  it('rejects text that is not a number', () => {
    expect(parseFormattedNumber('12a')).toBeNull();
    expect(parseFormattedNumber('1.2.3,4,5')).toBeNull();
    expect(parseFormattedNumber('-5', { allowNegative: false })).toBeNull();
  });
  it('respects the fraction limit of whole-number fields', () => {
    expect(parseFormattedNumber('1.250', { maxFraction: 0 })).toBe('1250');
    expect(parseFormattedNumber('12.5', { maxFraction: 0 })).toBeNull();
    expect(parseFormattedNumber('12.555', { maxFraction: 2 })).toBeNull();
  });
});

describe('applyNumericEdit keeps the caret on the same digit', () => {
  const en = { lang: 'en' as const, maxFraction: 0 };
  it('formats while typing', () => {
    expect(applyNumericEdit('1250000000', 10, en)).toEqual({ raw: '1250000000', display: '1,250,000,000', caret: 13 });
  });
  it('inserting in the middle', () => {
    // "1,250|,000" + "9" → "1,2509,000" → 12,509,000 with the caret after the 9
    const r = applyNumericEdit('1,2509,000', 6, en)!;
    expect(r.display).toBe('12,509,000');
    expect(r.display.slice(0, r.caret)).toBe('12,509');
  });
  it('deleting a digit in the middle', () => {
    // "1,250,000" with the 5 removed by backspace (caret was after 5)
    const r = applyNumericEdit('1,20,000', 3, en)!;
    expect(r.raw).toBe('120000');
    expect(r.display).toBe('120,000');
    expect(r.display.slice(0, r.caret)).toBe('12');
  });
  it('backspace/delete next to a separator remove the digit, not the comma', () => {
    expect(deleteAcrossSeparator('1,250', 2, 2, 'Backspace', 'en')).toEqual({ text: '250', caret: 0 });
    expect(deleteAcrossSeparator('1,250', 1, 1, 'Delete', 'en')).toEqual({ text: '1250'.slice(0, 1) + '50', caret: 1 });
    expect(deleteAcrossSeparator('1,250', 3, 3, 'Backspace', 'en')).toBeNull();
    expect(deleteAcrossSeparator('1,250', 0, 5, 'Backspace', 'en')).toBeNull(); // selection: browser default
  });
  it('clearing yields empty, not zero', () => {
    expect(applyNumericEdit('', 0, en)).toEqual({ raw: '', display: '', caret: 0 });
  });
  it('accepts pasted formats', () => {
    expect(applyNumericEdit('۱٬۲۵۰٬۰۰۰', 9, en)!.raw).toBe('1250000');
    expect(applyNumericEdit('1 250 000', 9, en)!.raw).toBe('1250000');
  });
  it('rejects letters and extra decimals', () => {
    expect(applyNumericEdit('12a', 3, en)).toBeNull();
    expect(applyNumericEdit('1.5', 3, en)).toBeNull();
    expect(applyNumericEdit('1.555', 5, { lang: 'en', maxFraction: 2 })).toBeNull();
  });
  it('supports decimals and negatives while typing', () => {
    const dec = { lang: 'en' as const, maxFraction: 2, allowNegative: true };
    expect(applyNumericEdit('-', 1, dec)).toEqual({ raw: '-', display: '-', caret: 1 });
    expect(applyNumericEdit('1250.', 5, dec)).toEqual({ raw: '1250.', display: '1,250.', caret: 6 });
    expect(applyNumericEdit('-1250.5', 7, dec)!.display).toBe('-1,250.5');
    expect(applyNumericEdit('.5', 2, dec)).toEqual({ raw: '0.5', display: '0.5', caret: 3 });
  });
  it('removes leading zeros without losing the caret', () => {
    const r = applyNumericEdit('0005', 4, en)!;
    expect(r).toEqual({ raw: '5', display: '5', caret: 1 });
  });
  it('Persian: Persian digits, ٬ grouping and ٫ or , as the decimal point', () => {
    const fa = { lang: 'fa' as const, maxFraction: 2 };
    expect(applyNumericEdit('1250000', 7, fa)).toEqual({ raw: '1250000', display: '۱٬۲۵۰٬۰۰۰', caret: 9 });
    expect(applyNumericEdit('۱۲۵۰,۵', 6, fa)!.raw).toBe('1250.5');
    expect(applyNumericEdit('۱٬۲۵۰٫۵', 7, fa)!.display).toBe('۱٬۲۵۰٫۵');
  });
  it('shows stored values grouped', () => {
    expect(inputDisplay('1250000.5', 'en')).toBe('1,250,000.5');
    expect(inputDisplay('', 'fa')).toBe('');
  });
});
