import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { monthRange, todayIso } from '@/lib/dates';
import { Field } from './ui/Field';
import { DateField } from './ui/DateField';
import { Button } from './ui/Button';

/** From/to date filter with one-click presets. */
export function DateRange({ from, to, onChange, presets = true }: { from: string; to: string; onChange: (from: string, to: string) => void; presets?: boolean }) {
  const { t } = useI18n();
  const f = useFormat();
  const { me } = useAuth();
  const fy = me?.fiscal_year;
  return (
    <>
      <Field label={t('common.from')}>
        <DateField value={from} onChange={(v) => onChange(v, to)} />
      </Field>
      <Field label={t('common.to')}>
        <DateField value={to} onChange={(v) => onChange(from, v)} />
      </Field>
      {presets && (
        <div className="row" style={{ alignSelf: 'flex-end', paddingBottom: 2 }}>
          <Button size="sm" variant="quiet" onClick={() => { const r = monthRange(todayIso(), f.calendar); onChange(r.from, r.to); }}>{t('common.thisMonth')}</Button>
          {fy && <Button size="sm" variant="quiet" onClick={() => onChange(fy.start_date.slice(0, 10), fy.end_date.slice(0, 10))}>{t('common.thisYear')}</Button>}
        </div>
      )}
    </>
  );
}

export function defaultYearRange(fy: { start_date: string; end_date: string } | null | undefined) {
  return fy ? { from: fy.start_date.slice(0, 10), to: fy.end_date.slice(0, 10) } : { from: '', to: '' };
}
