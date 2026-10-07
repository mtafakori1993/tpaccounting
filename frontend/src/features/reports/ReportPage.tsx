import { useQuery } from '@tanstack/react-query';
import { Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import type { ReactNode } from 'react';
import { api, type Query } from '@/api/client';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Checkbox, Field, Input, Select } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Amount, EmptyState, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import type { ExportColumn } from '@/components/export';
import { AccountPicker, CostCenterSelect, PartyPicker, useAccountName } from '@/components/pickers/Pickers';
import { todayIso } from '@/lib/dates';
import type { PdfDocument, PdfRow } from '@/lib/pdf';
import { LedgerView, type LedgerRow } from './LedgerView';

const known = ['journal', 'ledger', 'trial-balance', 'party-statement', 'profit-loss', 'balance-sheet', 'cash-position', 'receivables', 'payables', 'sales', 'purchases', 'expenses', 'cost-centers'] as const;
type Report = (typeof known)[number];

/** URL-backed report parameters with fiscal-year defaults. */
function useParamsState() {
  const [sp, setSp] = useSearchParams();
  const { me } = useAuth();
  const fy = me?.fiscal_year;
  const get = (k: string, def = '') => sp.get(k) ?? def;
  const set = (patch: Record<string, string | number | boolean | null | undefined>) => {
    const n = new URLSearchParams(sp);
    for (const [k, v] of Object.entries(patch)) {
      if (v === null || v === undefined || v === '' || v === false) n.delete(k);
      else n.set(k, String(v));
    }
    setSp(n, { replace: true });
  };
  const from = get('from', fy?.start_date.slice(0, 10) ?? '');
  const to = get('to', todayIso() < (fy?.end_date.slice(0, 10) ?? '9999') ? todayIso() : fy?.end_date.slice(0, 10) ?? '');
  return { get, set, from, to };
}

/** "From … To …" line printed under report titles. */
function usePeriod(from: string, to: string) {
  const { t } = useI18n();
  const f = useFormat();
  return [from && `${t('common.from')} ${f.date(from)}`, to && `${t('common.to')} ${f.date(to)}`].filter(Boolean).join('   ');
}

/** Rows of a grouped financial statement for print: section header, lines, section total. */
function statementRows(sections: { title: string; lines: Line[]; total: string }[], f: ReturnType<typeof useFormat>, name: (a: Line) => string, totalLabel: string): PdfRow[] {
  return sections.flatMap((s) => [
    { style: 'group' as const, cells: [s.title, '', ''] },
    ...s.lines.map((l) => ({ cells: [f.digits(l.code), name(l), f.money(l.amount)] })),
    { style: 'subtotal' as const, cells: ['', `${totalLabel} ${s.title}`, f.money(s.total)] },
  ]);
}

function useReport<T>(path: string, params: Query, enabled = true) {
  return useQuery({ queryKey: ['report', path, params], queryFn: () => api.get<T>(path, params), enabled });
}

export function ReportPage() {
  const { report } = useParams();
  const { t } = useI18n();
  if (!known.includes(report as Report)) return <Navigate to="/reports" replace />;
  const r = report as Report;
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('reports.title'), to: '/reports' }]} title={t(`reports.names.${r}`)} subtitle={t(`reports.descriptions.${r}`)} />
      {r === 'journal' && <JournalReport />}
      {r === 'ledger' && <LedgerReport />}
      {r === 'party-statement' && <PartyStatement />}
      {r === 'trial-balance' && <TrialBalance />}
      {r === 'profit-loss' && <ProfitLoss />}
      {r === 'balance-sheet' && <BalanceSheet />}
      {r === 'cash-position' && <CashPosition />}
      {(r === 'receivables' || r === 'payables') && <Aging kind={r} />}
      {(r === 'sales' || r === 'purchases') && <Trade side={r} />}
      {r === 'expenses' && <Expenses />}
      {r === 'cost-centers' && <CostCenters />}
    </div>
  );
}

function ReportFrame<T>({ filters, name, rows, columns, children, error, loading, subtitle, pdf }: {
  filters: ReactNode; name: string; rows?: T[]; columns: ExportColumn<T>[]; children: ReactNode; error?: unknown; loading?: boolean;
  subtitle?: string; pdf?: () => Partial<PdfDocument>;
}) {
  const err = useErrorMessage();
  const { report } = useParams();
  const { t } = useI18n();
  const title = t(`reports.names.${report as Report}`);
  return (
    <Panel bodyless>
      <div className="toolbar">
        {filters}
        <span className="grow" />
        <ExportMenu name={name} rows={rows} columns={columns} title={title} subtitle={subtitle} pdf={pdf && (() => ({ subtitle, ...pdf(), title }))} />
      </div>
      {error ? <div style={{ padding: 16 }}><Alert>{err(error)}</Alert></div> : null}
      {loading ? <LoadingBlock /> : children}
    </Panel>
  );
}

function LevelSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { t } = useI18n();
  return (
    <Field label={t('reports.level')}>
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        {(['1', '2', '3', '4'] as const).map((l) => <option key={l} value={l}>{t(`levels.${l}`)}</option>)}
      </Select>
    </Field>
  );
}

function JournalReport() {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const p = useParamsState();
  const navigate = useNavigate();
  const name = useAccountName();
  const q = useReport<{ rows: LedgerRow[]; total_debit: string; total_credit: string; truncated: boolean }>('/reports/journal', { from: p.from, to: p.to, q: p.get('q') });
  const period = usePeriod(p.from, p.to);
  const d = q.data;
  return (
    <ReportFrame
      name="journal"
      subtitle={period}
      error={q.error}
      rows={d?.rows}
      columns={[
        { header: t('common.date'), value: (r) => f.date(r.date) },
        { header: t('common.number'), value: (r) => r.number, numeric: true },
        { header: t('common.code'), value: (r) => r.account_code },
        { header: t('journal.account'), value: (r) => name({ name: r.account_name, name_en: r.account_name_en }) },
        { header: t('journal.party'), value: (r) => r.party_name ?? '' },
        { header: t('common.description'), value: (r) => r.description },
        { header: t('common.debit'), value: (r) => r.debit, numeric: true },
        { header: t('common.credit'), value: (r) => r.credit, numeric: true },
      ]}
      filters={<>
        <Field label={t('common.search')} className="search"><Input value={p.get('q')} onChange={(e) => p.set({ q: e.target.value })} /></Field>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
      </>}
    >
      {d?.truncated && <div style={{ padding: 16 }}><Alert kind="warning">{t('common.truncated', { count: f.count(d.rows.length) })}</Alert></div>}
      <DataTable
        rows={d?.rows}
        loading={q.isLoading}
        rowKey={(r, i) => `${r.entry_id}-${i}`}
        onRowClick={(r) => navigate(`/journal/${r.entry_id}`)}
        showFooter
        columns={[
          { key: 'date', header: t('common.date'), cell: (r) => <span className="num">{f.date(r.date)}</span>, width: 110 },
          { key: 'n', header: t('common.number'), cell: (r) => <span className="num">{f.int(r.number)}</span>, width: 70 },
          { key: 'src', header: t('journal.source'), cell: (r) => tryT(`journal.sources.${r.source_type}`) ?? r.source_type },
          { key: 'acc', header: t('journal.account'), cell: (r) => <><span className="num muted">{f.digits(r.account_code)}</span> {name({ name: r.account_name, name_en: r.account_name_en })}</> },
          { key: 'party', header: t('journal.party'), cell: (r) => r.party_name ?? '' },
          { key: 'desc', header: t('common.description'), cell: (r) => r.description, footer: t('common.total') },
          { key: 'dr', header: t('common.debit'), align: 'end', cell: (r) => <Amount value={r.debit} blankZero />, footer: <Amount value={d?.total_debit} /> },
          { key: 'cr', header: t('common.credit'), align: 'end', cell: (r) => <Amount value={r.credit} blankZero />, footer: <Amount value={d?.total_credit} /> },
        ]}
      />
    </ReportFrame>
  );
}

function LedgerReport() {
  const { t } = useI18n();
  const p = useParamsState();
  const accountId = Number(p.get('account_id')) || null;
  const partyId = Number(p.get('party_id')) || null;
  const ccId = Number(p.get('cost_center_id')) || null;
  return (
    <Panel bodyless>
      <div className="toolbar">
        <Field label={`${t('journal.account')} (${t('reports.withSub')})`} className="search"><AccountPicker includeGroups value={accountId} onChange={(id) => p.set({ account_id: id })} /></Field>
        <Field label={t('journal.party')}><PartyPicker value={partyId} onChange={(id) => p.set({ party_id: id })} /></Field>
        <Field label={t('journal.costCenter')}><CostCenterSelect value={ccId} onChange={(id) => p.set({ cost_center_id: id })} /></Field>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
      </div>
      {!accountId && !partyId && !ccId ? (
        <EmptyState title={t('reports.pickAccount')} />
      ) : (
        <LedgerView params={{ account_id: accountId, party_id: partyId, cost_center_id: ccId, from: p.from, to: p.to }} exportName="ledger" />
      )}
    </Panel>
  );
}

function PartyStatement() {
  const { t } = useI18n();
  const p = useParamsState();
  const partyId = Number(p.get('party_id')) || null;
  return (
    <Panel bodyless>
      <div className="toolbar">
        <Field label={t('journal.party')} className="search"><PartyPicker value={partyId} onChange={(id) => p.set({ party_id: id })} /></Field>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
      </div>
      {!partyId ? <EmptyState title={t('reports.pickParty')} /> : <LedgerView params={{ party_id: partyId, from: p.from, to: p.to }} exportName="party-statement" showParty={false} />}
    </Panel>
  );
}

interface TBRow { account_id: number; code: string; name: string; name_en: string; type: string; level: number; opening_debit: string; opening_credit: string; period_debit: string; period_credit: string; closing_debit: string; closing_credit: string }

function TrialBalance() {
  const { t } = useI18n();
  const f = useFormat();
  const p = useParamsState();
  const navigate = useNavigate();
  const name = useAccountName();
  const level = p.get('level', '3');
  const usePeriodText = usePeriod(p.from, p.to);
  const q = useReport<{ rows: TBRow[]; totals: TBRow }>('/reports/trial-balance', { from: p.from, to: p.to, level, show_zero: p.get('show_zero') === 'true', exclude_closing: p.get('exclude_closing') === 'true' });
  const d = q.data;
  const amt = (k: keyof TBRow) => ({ key: k, header: '', align: 'end' as const, cell: (r: TBRow) => <Amount value={r[k] as string} blankZero />, footer: <Amount value={d?.totals[k] as string} /> });
  const cols = [
    { key: 'code', header: t('common.code'), cell: (r: TBRow) => <span className="num">{f.digits(r.code)}</span>, width: 90 },
    { key: 'name', header: t('journal.account'), cell: (r: TBRow) => name(r), footer: t('common.total') },
    { ...amt('opening_debit'), header: `${t('reports.opening')} — ${t('common.debit')}` },
    { ...amt('opening_credit'), header: `${t('reports.opening')} — ${t('common.credit')}` },
    { ...amt('period_debit'), header: `${t('reports.period')} — ${t('common.debit')}` },
    { ...amt('period_credit'), header: `${t('reports.period')} — ${t('common.credit')}` },
    { ...amt('closing_debit'), header: `${t('reports.closing')} — ${t('common.debit')}` },
    { ...amt('closing_credit'), header: `${t('reports.closing')} — ${t('common.credit')}` },
  ];
  return (
    <ReportFrame
      name="trial-balance"
      subtitle={usePeriodText}
      error={q.error}
      rows={d?.rows}
      columns={[
        { header: t('common.code'), value: (r) => r.code },
        { header: t('journal.account'), value: (r) => name(r) },
        ...(['opening_debit', 'opening_credit', 'period_debit', 'period_credit', 'closing_debit', 'closing_credit'] as const).map((k, i) => ({ header: String(cols[i + 2].header), value: (r: TBRow) => r[k], numeric: true })),
      ]}
      filters={<>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
        <LevelSelect value={level} onChange={(v) => p.set({ level: v })} />
        <Checkbox label={t('reports.showZero')} checked={p.get('show_zero') === 'true'} onChange={(v) => p.set({ show_zero: v })} />
        <Checkbox label={t('reports.excludeClosing')} checked={p.get('exclude_closing') === 'true'} onChange={(v) => p.set({ exclude_closing: v })} />
      </>}
    >
      <DataTable rows={d?.rows} loading={q.isLoading} rowKey={(r) => r.account_id} showFooter columns={cols}
        onRowClick={(r) => navigate(`/reports/ledger?account_id=${r.account_id}&from=${p.from}&to=${p.to}`)} />
    </ReportFrame>
  );
}

interface Line { account_id: number; code: string; name: string; name_en: string; level: number; amount: string }

function StatementSection({ title, lines, total, from, to }: { title: string; lines: Line[]; total: string; from?: string; to?: string }) {
  const f = useFormat();
  const name = useAccountName();
  const navigate = useNavigate();
  const { t } = useI18n();
  return (
    <>
      <tr className="group-row"><td colSpan={3}>{title}</td></tr>
      {lines.map((l) => (
        <tr key={l.account_id} className="clickable" onClick={() => navigate(`/reports/ledger?account_id=${l.account_id}${from ? `&from=${from}` : ''}${to ? `&to=${to}` : ''}`)}>
          <td className="indent-1 num muted" style={{ width: 90 }}>{f.digits(l.code)}</td>
          <td>{name(l)}</td>
          <td className="align-end"><Amount value={l.amount} signed /></td>
        </tr>
      ))}
      <tr className="subtotal-row"><td /><td>{t('common.total')} {title}</td><td className="align-end"><Amount value={total} signed /></td></tr>
    </>
  );
}

function ProfitLoss() {
  const { t } = useI18n();
  const p = useParamsState();
  const level = p.get('level', '3');
  const cc = Number(p.get('cost_center_id')) || null;
  const q = useReport<{ revenue: Line[]; cost_of_sales: Line[]; expenses: Line[]; total_revenue: string; total_cost_of_sales: string; gross_profit: string; total_expenses: string; net_profit: string }>(
    '/reports/profit-loss', { from: p.from, to: p.to, level, cost_center_id: cc },
  );
  const d = q.data;
  const f = useFormat();
  const accName = useAccountName();
  const period = usePeriod(p.from, p.to);
  const flat = d ? [
    ...d.revenue.map((l) => ({ ...l, section: t('reports.revenue') })),
    ...d.cost_of_sales.map((l) => ({ ...l, section: t('reports.costOfSales') })),
    ...d.expenses.map((l) => ({ ...l, section: t('reports.expenses') })),
    { account_id: 0, code: '', name: t('reports.netProfit'), name_en: '', level: 0, amount: d.net_profit, section: '' },
  ] : undefined;
  const neg = d && d.net_profit.startsWith('-');
  return (
    <ReportFrame name="profit-loss" subtitle={period} error={q.error} loading={q.isLoading} rows={flat}
      pdf={d ? () => ({
        tables: [{
          columns: [{ label: t('common.code'), align: 'start' }, { label: t('journal.account'), align: 'start' }, { label: t('common.amount'), align: 'end' }],
          rows: [
            ...statementRows([{ title: t('reports.revenue'), lines: d.revenue, total: d.total_revenue }, { title: t('reports.costOfSales'), lines: d.cost_of_sales, total: d.total_cost_of_sales }], f, accName, t('common.total')),
            { style: 'subtotal', cells: ['', t('reports.grossProfit'), f.money(d.gross_profit)] },
            ...statementRows([{ title: t('reports.expenses'), lines: d.expenses, total: d.total_expenses }], f, accName, t('common.total')),
          ],
          footer: ['', d.net_profit.startsWith('-') ? t('reports.netLoss') : t('reports.netProfit'), f.money(d.net_profit)],
        }],
      }) : undefined}
      columns={[{ header: t('common.type'), value: (r) => r.section }, { header: t('common.code'), value: (r) => r.code }, { header: t('journal.account'), value: (r) => r.name }, { header: t('common.amount'), value: (r) => r.amount, numeric: true }]}
      filters={<>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
        <LevelSelect value={level} onChange={(v) => p.set({ level: v })} />
        <Field label={t('journal.costCenter')}><CostCenterSelect value={cc} onChange={(id) => p.set({ cost_center_id: id })} /></Field>
      </>}>
      {d && (
        <div className="table-wrap">
          <table className="table">
            <tbody>
              <StatementSection title={t('reports.revenue')} lines={d.revenue} total={d.total_revenue} from={p.from} to={p.to} />
              <StatementSection title={t('reports.costOfSales')} lines={d.cost_of_sales} total={d.total_cost_of_sales} from={p.from} to={p.to} />
              <tr className="subtotal-row"><td /><td>{t('reports.grossProfit')}</td><td className="align-end"><Amount value={d.gross_profit} signed /></td></tr>
              <StatementSection title={t('reports.expenses')} lines={d.expenses} total={d.total_expenses} from={p.from} to={p.to} />
            </tbody>
            <tfoot>
              <tr><td /><td style={{ fontSize: 16 }}>{neg ? t('reports.netLoss') : t('reports.netProfit')}</td><td className="align-end" style={{ fontSize: 18 }}><Amount value={d.net_profit} signed /></td></tr>
            </tfoot>
          </table>
        </div>
      )}
    </ReportFrame>
  );
}

function BalanceSheet() {
  const { t } = useI18n();
  const p = useParamsState();
  const level = p.get('level', '3');
  const asOf = p.get('as_of', todayIso());
  const q = useReport<{ assets: Line[]; liabilities: Line[]; equity: Line[]; total_assets: string; total_liabilities: string; total_equity: string; current_earnings: string; total_liabilities_and_equity: string; balanced: boolean }>(
    '/reports/balance-sheet', { as_of: asOf, level },
  );
  const d = q.data;
  const f = useFormat();
  const accName = useAccountName();
  const flat = d ? [
    ...d.assets.map((l) => ({ ...l, section: t('reports.assets') })),
    ...d.liabilities.map((l) => ({ ...l, section: t('reports.liabilities') })),
    ...d.equity.map((l) => ({ ...l, section: t('reports.equity') })),
    { account_id: 0, code: '', name: t('reports.currentEarnings'), name_en: '', level: 0, amount: d.current_earnings, section: t('reports.equity') },
  ] : undefined;
  return (
    <ReportFrame name="balance-sheet" subtitle={`${t('reports.asOf')} ${f.date(asOf)}`} error={q.error} loading={q.isLoading} rows={flat}
      pdf={d ? () => ({
        tables: [{
          columns: [{ label: t('common.code'), align: 'start' }, { label: t('journal.account'), align: 'start' }, { label: t('common.amount'), align: 'end' }],
          rows: [
            ...statementRows([{ title: t('reports.assets'), lines: d.assets, total: d.total_assets }, { title: t('reports.liabilities'), lines: d.liabilities, total: d.total_liabilities }], f, accName, t('common.total')),
            { style: 'group', cells: [t('reports.equity'), '', ''] },
            ...d.equity.map((l) => ({ cells: [f.digits(l.code), accName(l), f.money(l.amount)] })),
            { cells: ['', t('reports.currentEarnings'), f.money(d.current_earnings)] },
            { style: 'subtotal', cells: ['', `${t('common.total')} ${t('reports.equity')}`, f.money(d.total_equity)] },
          ],
          footer: ['', t('reports.totalLiabilitiesEquity'), f.money(d.total_liabilities_and_equity)],
        }],
      }) : undefined}
      columns={[{ header: t('common.type'), value: (r) => r.section }, { header: t('common.code'), value: (r) => r.code }, { header: t('journal.account'), value: (r) => r.name }, { header: t('common.amount'), value: (r) => r.amount, numeric: true }]}
      filters={<>
        <Field label={t('reports.asOf')}><DateField value={asOf} onChange={(v) => p.set({ as_of: v })} /></Field>
        <LevelSelect value={level} onChange={(v) => p.set({ level: v })} />
      </>}>
      {d && (
        <div className="stack" style={{ gap: 0 }}>
          {!d.balanced && <div style={{ padding: 16 }}><Alert kind="warning">{t('reports.notBalanced')}</Alert></div>}
          <div className="table-wrap">
            <table className="table">
              <tbody>
                <StatementSection title={t('reports.assets')} lines={d.assets} total={d.total_assets} to={asOf} />
                <StatementSection title={t('reports.liabilities')} lines={d.liabilities} total={d.total_liabilities} to={asOf} />
                <StatementSection title={t('reports.equity')} lines={[...d.equity]} total={d.total_equity} to={asOf} />
                <tr><td className="indent-1" /><td>{t('reports.currentEarnings')}</td><td className="align-end"><Amount value={d.current_earnings} signed /></td></tr>
              </tbody>
              <tfoot>
                <tr><td /><td>{t('reports.totalLiabilitiesEquity')}</td><td className="align-end"><Amount value={d.total_liabilities_and_equity} /></td></tr>
              </tfoot>
            </table>
          </div>
        </div>
      )}
    </ReportFrame>
  );
}

interface CashRow { id: number; kind: string; name: string; bank_name: string; opening: string; in: string; out: string; closing: string }

function CashPosition() {
  const { t } = useI18n();
  const p = useParamsState();
  const q = useReport<{ rows: CashRow[]; total: CashRow; total_cash: string; total_bank: string }>('/reports/cash-position', { from: p.from, to: p.to });
  const d = q.data;
  return (
    <ReportFrame name="cash-position" error={q.error} rows={d?.rows}
      columns={[
        { header: t('common.name'), value: (r) => r.name },
        { header: t('common.type'), value: (r) => (r.kind === 'cash' ? t('treasury.cash') : t('treasury.bank')) },
        { header: t('reports.opening'), value: (r) => r.opening, numeric: true },
        { header: t('reports.in'), value: (r) => r.in, numeric: true },
        { header: t('reports.out'), value: (r) => r.out, numeric: true },
        { header: t('reports.closing'), value: (r) => r.closing, numeric: true },
      ]}
      filters={<DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />}>
      {d && (
        <div className="summary-strip">
          <div className="summary-item"><div className="label">{t('dashboard.cashBalance')}</div><div className="value"><Amount value={d.total_cash} signed /></div></div>
          <div className="summary-item"><div className="label">{t('dashboard.bankBalance')}</div><div className="value"><Amount value={d.total_bank} signed /></div></div>
        </div>
      )}
      <DataTable rows={d?.rows} loading={q.isLoading} rowKey={(r) => r.id} showFooter columns={[
        { key: 'name', header: t('common.name'), cell: (r) => r.name },
        { key: 'kind', header: t('common.type'), cell: (r) => (r.kind === 'cash' ? t('treasury.cash') : t('treasury.bank')), footer: t('common.total') },
        { key: 'o', header: t('reports.opening'), align: 'end', cell: (r) => <Amount value={r.opening} signed />, footer: <Amount value={d?.total.opening} signed /> },
        { key: 'i', header: t('reports.in'), align: 'end', cell: (r) => <Amount value={r.in} />, footer: <Amount value={d?.total.in} /> },
        { key: 'u', header: t('reports.out'), align: 'end', cell: (r) => <Amount value={r.out} />, footer: <Amount value={d?.total.out} /> },
        { key: 'c', header: t('reports.closing'), align: 'end', cell: (r) => <Amount value={r.closing} signed />, footer: <Amount value={d?.total.closing} signed /> },
      ]} />
    </ReportFrame>
  );
}

interface AgingRow { party_id: number; party_code: string; party_name: string; phone: string; balance: string; current: string; days_1_30: string; days_31_60: string; days_61_90: string; days_over_90: string }

function Aging({ kind }: { kind: 'receivables' | 'payables' }) {
  const { t } = useI18n();
  const f = useFormat();
  const p = useParamsState();
  const navigate = useNavigate();
  const asOf = p.get('as_of', todayIso());
  const q = useReport<{ rows: AgingRow[]; total: AgingRow }>(`/reports/${kind}`, { as_of: asOf });
  const d = q.data;
  const buckets = [['current', 'reports.current'], ['days_1_30', 'reports.d30'], ['days_31_60', 'reports.d60'], ['days_61_90', 'reports.d90'], ['days_over_90', 'reports.over90']] as const;
  return (
    <ReportFrame name={kind} error={q.error} rows={d?.rows}
      columns={[
        { header: t('common.code'), value: (r) => r.party_code },
        { header: t('common.name'), value: (r) => r.party_name },
        { header: t('common.balance'), value: (r) => r.balance, numeric: true },
        ...buckets.map(([k, l]) => ({ header: t(l), value: (r: AgingRow) => r[k], numeric: true })),
      ]}
      filters={<Field label={t('reports.asOf')}><DateField value={asOf} onChange={(v) => p.set({ as_of: v })} /></Field>}>
      <DataTable rows={d?.rows} loading={q.isLoading} rowKey={(r) => r.party_id} showFooter onRowClick={(r) => navigate(`/parties/${r.party_id}`)} columns={[
        { key: 'name', header: t('common.name'), cell: (r) => r.party_name },
        { key: 'phone', header: t('parties.mobile'), cell: (r) => <span className="num" dir="ltr">{f.digits(r.phone)}</span>, footer: t('common.total') },
        { key: 'bal', header: t('common.balance'), align: 'end', cell: (r) => <Amount value={r.balance} />, footer: <Amount value={d?.total.balance} /> },
        ...buckets.map(([k, l]) => ({ key: k, header: t(l), align: 'end' as const, cell: (r: AgingRow) => <span className={k === 'days_over_90' && r[k] !== '0' ? 'danger-text' : undefined}><Amount value={r[k]} blankZero /></span>, footer: <Amount value={d?.total[k]} /> })),
      ]} />
    </ReportFrame>
  );
}

interface TradeRow { key: string; id: number | null; label: string; code: string; count: number; quantity: string; gross: string; discount: string; net: string; tax: string; total: string }

function monthLabel(key: string, f: ReturnType<typeof useFormat>) {
  return f.digits(key.replace('-', '/'));
}

function Trade({ side }: { side: 'sales' | 'purchases' }) {
  const { t } = useI18n();
  const f = useFormat();
  const p = useParamsState();
  const navigate = useNavigate();
  const group = p.get('group_by', 'party');
  const q = useReport<{ rows: TradeRow[]; total: TradeRow }>(`/reports/${side}`, { from: p.from, to: p.to, group_by: group });
  const d = q.data;
  const label = (r: TradeRow) => (group === 'month' ? monthLabel(r.label, f) : r.label);
  return (
    <ReportFrame name={`${side}-${group}`} error={q.error} rows={d?.rows}
      columns={[
        { header: t('reports.groupBy'), value: label },
        { header: t('reports.count'), value: (r) => r.count, numeric: true },
        ...(group === 'product' ? [{ header: t('reports.quantity'), value: (r: TradeRow) => r.quantity, numeric: true }] : []),
        { header: t('reports.gross'), value: (r) => r.gross, numeric: true },
        { header: t('invoices.discount'), value: (r) => r.discount, numeric: true },
        { header: t('invoices.net'), value: (r) => r.net, numeric: true },
        { header: t('invoices.tax'), value: (r) => r.tax, numeric: true },
        { header: t('invoices.grandTotal'), value: (r) => r.total, numeric: true },
      ]}
      filters={<>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
        <Field label={t('reports.groupBy')}>
          <Select value={group} onChange={(e) => p.set({ group_by: e.target.value })}>
            <option value="party">{t('reports.byParty')}</option>
            <option value="product">{t('reports.byProduct')}</option>
            <option value="month">{t('reports.byMonth')}</option>
          </Select>
        </Field>
      </>}>
      <DataTable rows={d?.rows} loading={q.isLoading} rowKey={(r) => r.key} showFooter
        onRowClick={group === 'party' ? (r) => r.id && navigate(`/parties/${r.id}`) : undefined}
        columns={[
          { key: 'label', header: group === 'party' ? t('reports.byParty') : group === 'product' ? t('reports.byProduct') : t('reports.byMonth'), cell: label, footer: t('common.total') },
          { key: 'count', header: t('reports.count'), align: 'end', cell: (r) => <span className="num">{f.count(r.count)}</span>, footer: <span className="num">{f.count(d?.total.count ?? 0)}</span> },
          ...(group === 'product' ? [{ key: 'qty', header: t('reports.quantity'), align: 'end' as const, cell: (r: TradeRow) => <span className="num">{f.qty(r.quantity)}</span> }] : []),
          { key: 'gross', header: t('reports.gross'), align: 'end', cell: (r) => <Amount value={r.gross} />, footer: <Amount value={d?.total.gross} /> },
          { key: 'disc', header: t('invoices.discount'), align: 'end', cell: (r) => <Amount value={r.discount} blankZero />, footer: <Amount value={d?.total.discount} /> },
          { key: 'net', header: t('invoices.net'), align: 'end', cell: (r) => <Amount value={r.net} />, footer: <Amount value={d?.total.net} /> },
          { key: 'tax', header: t('invoices.tax'), align: 'end', cell: (r) => <Amount value={r.tax} blankZero />, footer: <Amount value={d?.total.tax} /> },
          { key: 'total', header: t('invoices.grandTotal'), align: 'end', cell: (r) => <Amount value={r.total} />, footer: <Amount value={d?.total.total} /> },
        ]} />
    </ReportFrame>
  );
}

function Expenses() {
  const { t } = useI18n();
  const f = useFormat();
  const p = useParamsState();
  const navigate = useNavigate();
  const group = p.get('group_by', 'account');
  const cc = Number(p.get('cost_center_id')) || null;
  const q = useReport<{ rows: { id: number | null; code: string; label: string; amount: string }[]; total: string }>('/reports/expenses', { from: p.from, to: p.to, group_by: group, cost_center_id: cc });
  const d = q.data;
  const label = (r: { code: string; label: string }) => (group === 'month' ? monthLabel(r.label, f) : r.label || t('reports.unassigned'));
  return (
    <ReportFrame name={`expenses-${group}`} error={q.error} rows={d?.rows}
      columns={[{ header: t('common.code'), value: (r) => (group === 'month' ? '' : r.code) }, { header: t('common.name'), value: label }, { header: t('common.amount'), value: (r) => r.amount, numeric: true }]}
      filters={<>
        <DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />
        <Field label={t('reports.groupBy')}>
          <Select value={group} onChange={(e) => p.set({ group_by: e.target.value })}>
            <option value="account">{t('reports.byAccount')}</option>
            <option value="cost_center">{t('reports.byCostCenter')}</option>
            <option value="month">{t('reports.byMonth')}</option>
          </Select>
        </Field>
        <Field label={t('journal.costCenter')}><CostCenterSelect value={cc} onChange={(id) => p.set({ cost_center_id: id })} /></Field>
      </>}>
      <DataTable rows={d?.rows} loading={q.isLoading} rowKey={(r, i) => `${r.code}-${i}`} showFooter
        onRowClick={group === 'account' ? (r) => r.id && navigate(`/reports/ledger?account_id=${r.id}&from=${p.from}&to=${p.to}`) : undefined}
        columns={[
          ...(group !== 'month' ? [{ key: 'code', header: t('common.code'), cell: (r: { code: string }) => <span className="num">{f.digits(r.code)}</span>, width: 100 }] : []),
          { key: 'label', header: t('common.name'), cell: label, footer: t('common.total') },
          { key: 'amount', header: t('common.amount'), align: 'end', cell: (r) => <Amount value={r.amount} />, footer: <Amount value={d?.total} /> },
        ]} />
    </ReportFrame>
  );
}

interface CCRow { cost_center_id: number | null; code: string | null; name: string | null; revenue: string; cost_of_sales: string; expenses: string; net: string }

function CostCenters() {
  const { t } = useI18n();
  const p = useParamsState();
  const navigate = useNavigate();
  const q = useReport<{ rows: CCRow[]; total: CCRow }>('/reports/cost-centers', { from: p.from, to: p.to });
  const d = q.data;
  const name = (r: CCRow) => r.name ?? t('reports.unassigned');
  return (
    <ReportFrame name="cost-centers" error={q.error} rows={d?.rows}
      columns={[
        { header: t('common.name'), value: name },
        { header: t('reports.revenue'), value: (r) => r.revenue, numeric: true },
        { header: t('reports.costOfSales'), value: (r) => r.cost_of_sales, numeric: true },
        { header: t('reports.expenses'), value: (r) => r.expenses, numeric: true },
        { header: t('reports.netProfit'), value: (r) => r.net, numeric: true },
      ]}
      filters={<DateRange from={p.from} to={p.to} onChange={(from, to) => p.set({ from, to })} />}>
      <DataTable rows={d?.rows} loading={q.isLoading} rowKey={(r) => r.cost_center_id ?? 0} showFooter
        onRowClick={(r) => r.cost_center_id && navigate(`/reports/ledger?cost_center_id=${r.cost_center_id}&from=${p.from}&to=${p.to}`)}
        columns={[
          { key: 'name', header: t('common.name'), cell: name, footer: t('common.total') },
          { key: 'rev', header: t('reports.revenue'), align: 'end', cell: (r) => <Amount value={r.revenue} />, footer: <Amount value={d?.total.revenue} /> },
          { key: 'cos', header: t('reports.costOfSales'), align: 'end', cell: (r) => <Amount value={r.cost_of_sales} />, footer: <Amount value={d?.total.cost_of_sales} /> },
          { key: 'exp', header: t('reports.expenses'), align: 'end', cell: (r) => <Amount value={r.expenses} />, footer: <Amount value={d?.total.expenses} /> },
          { key: 'net', header: t('reports.netProfit'), align: 'end', cell: (r) => <Amount value={r.net} signed />, footer: <Amount value={d?.total.net} signed /> },
        ]} />
    </ReportFrame>
  );
}

