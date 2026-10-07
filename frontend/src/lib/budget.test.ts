import { describe, expect, it } from 'vitest';
import { percentsOf } from './budget';
import { add } from './decimal';
import { en } from '@/i18n/en';
import { fa } from '@/i18n/fa';

describe('percentsOf', () => {
  it('always adds up to exactly 100', () => {
    const p = percentsOf(['33333333', '33333333', '33333334'], '100000000');
    expect(add(...p)).toBe('100');
  });
  it('is empty without an annual amount', () => {
    expect(percentsOf(['0', '0'], '0')).toEqual(['', '']);
  });
});

/** Every key path of a nested dictionary. */
function paths(o: object, prefix = ''): string[] {
  return Object.entries(o).flatMap(([k, v]) => (typeof v === 'object' && v !== null ? paths(v, `${prefix}${k}.`) : [`${prefix}${k}`]));
}

describe('dictionaries', () => {
  it('Persian and English have the same keys for the new modules', () => {
    for (const mod of ['checks', 'payroll', 'budgets'] as const) {
      expect(paths(fa[mod]).sort()).toEqual(paths(en[mod]).sort());
    }
    expect(paths(fa).length).toBe(paths(en).length);
  });
  it('translates every new permission action and module', () => {
    for (const a of ['calculate', 'approve', 'pay', 'close', 'submit', 'activate', 'override'] as const) {
      expect(fa.settings.actions[a]).toBeTruthy();
    }
    for (const m of ['checks', 'payroll', 'budgets'] as const) expect(fa.settings.modules[m]).toBeTruthy();
  });
});
