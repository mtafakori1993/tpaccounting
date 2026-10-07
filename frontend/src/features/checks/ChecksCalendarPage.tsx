import { useMemo, useState } from 'react';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { api } from '@/api/client';
import type { Check, CheckPage } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Field, MoneyInput, Select } from '@/components/ui/Field';
import { PageHeader, Panel, Tabs } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { EmptyState, ErrorState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { ServerExport } from '@/components/ServerExport';
import { PartyPicker } from '@/components/pickers/Pickers';
import { usePersistentState } from '@/lib/hooks';
import { calendarParts, isoFromCalendar, monthLength, parseIso, todayIso } from '@/lib/dates';
import { issuedStatuses, receivedStatuses, useBanks, useCheckColumns } from './common';

export function ChecksCalendarPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const navigate = useNavigate();
  const banks = useBanks();
  const cal = f.calendar;
  const today = todayIso();
  const [ym, setYm] = useState(() => { const p = calendarParts(today, cal); return { y: p.y, m: p.m }; });
  const [mode, setMode] = useState<'month' | 'list'>('month');
  const [flt, setFlt] = usePersistentState('filters.check-calendar', { check_type: '', status: '', bank_id: '', party_id: '', min_amount: '', max_amount: '', live: 'true' });
  const first = isoFromCalendar(ym.y, ym.m, 1, cal);
  const days = monthLength(ym.y, ym.m, cal);
  const last = isoFromCalendar(ym.y, ym.m, days, cal);
  const q = useQuery({
    queryKey: ['checks', 'calendar', first, flt],
    queryFn: () => api.get<CheckPage>('/checks', { ...flt, due_from: first, due_to: last, page_size: 500, sort: 'due_date' }),
    placeholderData: keepPreviousData,
  });
  const totals = useMemo(() => {
    let inflow = 0n, outflow = 0n, nIn = 0, nOut = 0;
    // Amounts are decimal strings; sum whole units exactly for the headline (fractions are shown by the table).
    for (const c of q.data?.items ?? []) {
      const v = BigInt(c.amount.split('.')[0]);
      if (c.check_type === 'received') { inflow += v; nIn++; } else { outflow += v; nOut++; }
    }
    return { inflow: inflow.toString(), outflow: outflow.toString(), nIn, nOut };
  }, [q.data]);
  const active = Object.entries(flt).filter(([k, v]) => v && !(k === 'live' && v === 'true')).length;
  const advancedActive = ['bank_id', 'party_id', 'min_amount', 'max_amount'].filter((k) => flt[k as keyof typeof flt]).length;
  const byDay = useMemo(() => {
    const m = new Map<string, Check[]>();
    for (const c of q.data?.items ?? []) m.set(c.due_date.slice(0, 10), [...(m.get(c.due_date.slice(0, 10)) ?? []), c]);
    return m;
  }, [q.data]);
  // Weeks start on Saturday in the Jalali calendar and on Monday otherwise.
  const weekStart = cal === 'jalali' ? 6 : 1;
  const { y, m, d } = parseIso(first);
  const offset = (new Date(Date.UTC(y, m - 1, d)).getUTCDay() - weekStart + 7) % 7;
  const dows = Array.from({ length: 7 }, (_, i) => new Intl.DateTimeFormat(lang === 'fa' ? 'fa-IR' : 'en-US', { weekday: 'short', timeZone: 'UTC' })
    .format(new Date(Date.UTC(2024, 0, 6 + ((weekStart + i) % 7)))));
  const monthName = new Intl.DateTimeFormat(lang === 'fa' ? 'fa-IR-u-ca-persian' : cal === 'jalali' ? 'en-US-u-ca-persian' : 'en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' })
    .format(new Date(`${first}T12:00:00Z`));
  const move = (delta: number) => setYm(({ y, m }) => { let nm = m + delta, ny = y; if (nm < 1) { nm = 12; ny--; } if (nm > 12) { nm = 1; ny++; } return { y: ny, m: nm }; });
  const statuses = flt.check_type === 'issued' ? issuedStatuses : flt.check_type === 'received' ? receivedStatuses : Array.from(new Set([...receivedStatuses, ...issuedStatuses]));
  const columns = useCheckColumns();
  const set = (k: keyof typeof flt, v: string) => setFlt({ ...flt, [k]: v });
  const open = (c: Check) => navigate(`/checks/${c.id}`);
  return (
    <div className="page">
      <PageHeader title={t('checks.calendarTitle')} subtitle={t('checks.calendarSubtitle')}
        actions={<ServerExport path="/checks/export" query={{ ...flt, due_from: first, due_to: last }} name="check-calendar" permission="checks.export" />} />
      <KpiGrid loading={!q.data} count={3}>
        <Kpi label={t('checks.inflow')} value={f.money(totals.inflow)} sub={t('checks.dashboard.count', { count: f.count(totals.nIn) })} tone="success" />
        <Kpi label={t('checks.outflow')} value={f.money(totals.outflow)} sub={t('checks.dashboard.count', { count: f.count(totals.nOut) })} tone="info" />
        <Kpi label={t('checks.net')} value={f.money((BigInt(totals.inflow) - BigInt(totals.outflow)).toString())} sub={monthName} tone="primary" />
      </KpiGrid>
      <Panel bodyless>
        <div className="row-between" style={{ padding: '8px 20px', borderBottom: '1px solid var(--color-border)' }}>
          <div className="row">
            <button type="button" className="btn btn-quiet btn-icon" aria-label={t('checks.previousMonth')} onClick={() => move(-1)}><ChevronLeft size={18} className="icon-directional" /></button>
            <strong style={{ minWidth: 150, textAlign: 'center' }} aria-live="polite">{monthName}</strong>
            <button type="button" className="btn btn-quiet btn-icon" aria-label={t('checks.nextMonth')} onClick={() => move(1)}><ChevronRight size={18} className="icon-directional" /></button>
            <button type="button" className="btn btn-quiet btn-sm" onClick={() => { const p = calendarParts(today, cal); setYm({ y: p.y, m: p.m }); }}>{t('common.today')}</button>
          </div>
          <div className="chart-legend" aria-hidden>
            <span className="key"><span className="swatch" style={{ background: 'var(--color-success-bg)', border: '1px solid var(--color-success)' }} />{t('checks.types.received')}</span>
            <span className="key"><span className="swatch" style={{ background: 'var(--color-info-bg)', border: '1px solid var(--color-info)' }} />{t('checks.types.issued')}</span>
            <span className="key"><span className="swatch" style={{ background: 'var(--color-danger-bg)', border: '1px solid var(--color-danger)' }} />{t('checks.tabs.overdue')}</span>
          </div>
        </div>
        <Tabs value={mode} onChange={setMode} tabs={[{ key: 'month', label: t('checks.calendarMonth') }, { key: 'list', label: t('checks.calendarList'), count: q.data ? f.count(q.data.total) : undefined }]} />
        <FilterBar
          activeCount={active}
          advancedActive={advancedActive}
          onReset={() => setFlt({ check_type: '', status: '', bank_id: '', party_id: '', min_amount: '', max_amount: '', live: 'true' })}
          basic={<>
            <Field label={t('checks.type')}>
              <Select value={flt.check_type} onChange={(e) => setFlt({ ...flt, check_type: e.target.value, status: '' })}>
                <option value="">{t('checks.allTypes')}</option>
                <option value="received">{t('checks.types.received')}</option>
                <option value="issued">{t('checks.types.issued')}</option>
              </Select>
            </Field>
            <Field label={t('common.status')}>
              <Select value={flt.status} onChange={(e) => set('status', e.target.value)}>
                <option value="">{t('common.all')}</option>
                {statuses.map((s) => <option key={s} value={s}>{t(`checks.statuses.${s as 'draft'}`)}</option>)}
              </Select>
            </Field>
            <Field label={t('checks.outstanding')}>
              <Select value={flt.live} onChange={(e) => set('live', e.target.value)}>
                <option value="">{t('common.all')}</option>
                <option value="true">{t('checks.liveOnly')}</option>
              </Select>
            </Field>
          </>}
          advanced={<>
            <Field label={t('checks.bank')}>
              <Select value={flt.bank_id} onChange={(e) => set('bank_id', e.target.value)}>
                <option value="">{t('common.all')}</option>
                {banks.data?.map((b) => <option key={b.id} value={b.id}>{lang === 'en' ? b.name_en : b.name}</option>)}
              </Select>
            </Field>
            <Field label={t('checks.party')}><PartyPicker value={Number(flt.party_id) || null} onChange={(x) => set('party_id', x ? String(x) : '')} /></Field>
            <Field label={t('checks.minAmount')}><MoneyInput value={flt.min_amount} onChange={(x) => set('min_amount', x)} /></Field>
            <Field label={t('checks.maxAmount')}><MoneyInput value={flt.max_amount} onChange={(x) => set('max_amount', x)} /></Field>
          </>}
        />
        {q.error && !q.data ? <ErrorState error={q.error} onRetry={() => void q.refetch()} /> : mode === 'month' ? (
          <div className="month-scroll">
            <div className="month-grid" role="grid" aria-label={monthName}>
              {dows.map((w) => <div key={w} className="dow" role="columnheader">{w}</div>)}
              {Array.from({ length: offset }, (_, i) => <div key={`e${i}`} className="month-cell muted-cell" />)}
              {Array.from({ length: days }, (_, i) => {
                const iso = isoFromCalendar(ym.y, ym.m, i + 1, cal);
                const list = byDay.get(iso) ?? [];
                return (
                  <div key={iso} className={`month-cell ${iso === today ? 'today' : ''}`} role="gridcell" aria-label={f.date(iso)}>
                    <span className="day num">{f.int(i + 1)}</span>
                    {list.slice(0, 4).map((c) => (
                      <button key={c.id} type="button" className={`chip ${c.overdue ? 'late' : c.check_type === 'received' ? 'in' : 'out'}`}
                        title={`${c.party_name} — ${f.money(c.amount)}`} onClick={() => open(c)}>
                        {c.check_type === 'received' ? '↓' : '↑'} <span className="num">{f.money(c.amount)}</span> {c.party_name}
                      </button>
                    ))}
                    {list.length > 4 && <button type="button" className="chip" onClick={() => setMode('list')}>{t('ui.andMore', { count: f.count(list.length - 4) })}</button>}
                  </div>
                );
              })}
            </div>
          </div>
        ) : (
          <DataTable rows={q.data?.items} loading={q.isFetching} rowKey={(c) => c.id} onRowClick={open} columns={columns} columnsKey="checks.calendar"
            rowClassName={(c) => (c.overdue ? 'danger-row' : undefined)} empty={<EmptyState title={t('checks.none')} body={t('checks.emptyFilteredBody')} filtered={active > 0} />} />
        )}
      </Panel>
    </div>
  );
}
