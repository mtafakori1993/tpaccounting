import { useMemo } from 'react';
import { useI18n } from './I18nProvider';
import { useAuth } from '@/auth/AuthProvider';
import { formatCalendarDate, type Calendar } from '@/lib/dates';
import { currencyLabel, formatInteger, formatMoney, formatNumber, formatPercentage, toLocaleDigits } from '@/lib/number';
import { toBig } from '@/lib/decimal';

/** Formatting bound to the current language and company settings. */
export function useFormat() {
  const { lang, t } = useI18n();
  const { me } = useAuth();
  const calendar: Calendar = me?.company.calendar ?? 'jalali';
  const decimals = me?.company.amount_decimals ?? 0;

  const currency = me?.company.base_currency ?? 'IRR';

  return useMemo(() => {
    const locale = lang === 'fa' ? 'fa-IR' : 'en-US';
    const digits = (s: string) => toLocaleDigits(s, lang);
    return {
      calendar,
      decimals,
      locale,
      currency,
      /** the company currency's label: "ریال" / "IRR" */
      currencyLabel: currencyLabel(currency, lang),
      /** localises the digits of identifiers (invoice/check numbers, codes) without grouping them */
      digits,
      /** 1,250,000 — amounts in the company's decimals */
      money: (v: string | number | null | undefined) => formatMoney(v ?? 0, { lang, decimals }),
      /** 1,250,000 IRR — amounts with the company currency (KPIs, totals) */
      moneyWithCurrency: (v: string | number | null | undefined) => formatMoney(v ?? 0, { lang, decimals, currency }),
      /** absolute value with a Dr/Cr marker, for balances */
      balance: (v: string | number | null | undefined) => {
        const n = toBig(v ?? 0);
        if (n.eq(0)) return formatMoney(0, { lang, decimals });
        return `${formatMoney(n.abs().toString(), { lang, decimals })} ${n.gt(0) ? t('common.dr') : t('common.cr')}`;
      },
      /** quantities: up to 3 decimals, grouped */
      qty: (v: string | number | null | undefined) => formatNumber(v ?? 0, { lang, maxFraction: 3 }),
      /** any quantity with a chosen precision, grouped */
      number: (v: string | number | null | undefined, maxFraction = 2) => formatNumber(v ?? 0, { lang, maxFraction }),
      /** counts: 12,345 */
      count: (v: number | string | null | undefined) => formatInteger(v ?? 0, lang),
      /** identifiers and years made of digits: localised digits, never grouped */
      int: (v: number | string) => digits(String(v)),
      /** 105.5 → 105.5% (the value is already a percentage) */
      percent: (v: number | string | null | undefined) => (v === null || v === undefined || v === '' ? '—' : formatPercentage(v, { lang })),
      date: (iso: string | null | undefined) => (iso ? digits(formatCalendarDate(iso, calendar)) : ''),
      dateTime: (iso: string | null | undefined) => {
        if (!iso) return '';
        const d = new Date(iso);
        const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
        const time = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
        return digits(`${formatCalendarDate(day, calendar)} ${time}`);
      },
    };
  }, [lang, calendar, decimals, currency, t]);
}

export type Formatter = ReturnType<typeof useFormat>;
