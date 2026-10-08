import { describe, expect, it } from 'vitest';
import { digitsToAscii, gregorianToJalali } from '../src/agent/drivers';

describe('drivers', () => {
  it('converts Gregorian to Jalali like the app', () => {
    expect(gregorianToJalali(2026, 10, 8)).toEqual([1405, 7, 16]);
    expect(gregorianToJalali(2024, 3, 20)).toEqual([1403, 1, 1]);
    expect(gregorianToJalali(2025, 3, 20)).toEqual([1403, 12, 30]);
  });
  it('normalises Persian and Arabic digits', () => {
    expect(digitsToAscii('۱۴۰۵/۰۷/۱۶')).toBe('1405/07/16');
    expect(digitsToAscii('٤٥')).toBe('45');
  });
});
