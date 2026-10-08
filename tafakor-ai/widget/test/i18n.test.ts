// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest';
import { lang, num, t } from '../src/i18n';

describe('i18n', () => {
  it('follows the host document language', () => {
    document.documentElement.lang = 'en';
    expect(lang()).toBe('en');
    expect(t('askMode')).toBe('Ask me before registering');
    document.documentElement.lang = 'fa';
    expect(t('askMode')).toBe('قبل از ثبت از من بپرس');
  });
  it('formats amounts with grouping and Persian digits', () => {
    document.documentElement.lang = 'en';
    expect(num('10489500')).toBe('10,489,500');
    expect(num('1234.5678')).toBe('1,234.5678');
    document.documentElement.lang = 'fa';
    expect(num('2500000')).toBe('۲٬۵۰۰٬۰۰۰');
  });
});
