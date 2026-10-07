import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '@/api/client';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Alert, Amount, BalanceAmount } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { useAccountName } from '@/components/pickers/Pickers';

export interface LedgerRow {
  entry_id: number;
  date: string;
  number: number;
  source_type: string;
  source_id: number | null;
  reference: string;
  account_id: number;
  account_code: string;
  account_name: string;
  account_name_en: string;
  party_id: number | null;
  party_name: string | null;
  cost_center_name: string | null;
  description: string;
  debit: string;
  credit: string;
  balance: string;
}
interface Ledger { opening: string; total_debit: string; total_credit: string; closing: string; rows: LedgerRow[]; truncated: boolean }

/** Ledger lines with opening, running and closing balance. Used for accounts, parties and cost centers. */
export function LedgerView({ params, exportName, showAccount = true, showParty = true, toolbar, title }: {
  params: { account_id?: number | null; party_id?: number | null; cost_center_id?: number | null; from?: string; to?: string; q?: string };
  exportName: string;
  showAccount?: boolean;
  showParty?: boolean;
  toolbar?: React.ReactNode;
  /** document title for PDF/print, e.g. the party or account name */
  title?: string;
}) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const navigate = useNavigate();
  const name = useAccountName();
  const enabled = !!(params.account_id || params.party_id || params.cost_center_id);
  const q = useQuery({ queryKey: ['ledger', params], queryFn: () => api.get<Ledger>('/reports/ledger', params), enabled });
  const d = q.data;
  const src = (r: LedgerRow) => tryT(`journal.sources.${r.source_type}`) ?? r.source_type;

  return (
    <>
      <div className="row-between" style={{ padding: '12px 20px', borderBottom: '1px solid var(--color-border)' }}>
        <div className="row" style={{ gap: 32 }}>
          {[
            [t('reports.opening'), <BalanceAmount value={d?.opening} />],
            [t('common.debit'), <Amount value={d?.total_debit ?? '0'} />],
            [t('common.credit'), <Amount value={d?.total_credit ?? '0'} />],
            [t('reports.closing'), <BalanceAmount value={d?.closing} />],
          ].map(([label, value], i) => (
            <div key={i} className="summary-item">
              <div className="label">{label}</div>
              <div className="value" style={{ fontSize: 18 }}>{value}</div>
            </div>
          ))}
        </div>
        <div className="row no-print">
          {toolbar}
          <ExportMenu
            name={exportName}
            rows={d?.rows}
            pdf={() => ({
              title: title ?? document.querySelector('.page-title')?.textContent?.trim() ?? exportName,
              subtitle: [params.from && `${t('common.from')} ${f.date(params.from)}`, params.to && `${t('common.to')} ${f.date(params.to)}`].filter(Boolean).join('  '),
              orientation: showAccount && showParty ? 'landscape' : 'portrait',
              tables: [{
                columns: [
                  { label: t('common.date'), align: 'start' },
                  { label: t('common.number'), align: 'start' },
                  ...(showAccount ? [{ label: t('journal.account'), align: 'start' as const }] : []),
                  ...(showParty ? [{ label: t('journal.party'), align: 'start' as const }] : []),
                  { label: t('common.description'), align: 'start' },
                  { label: t('common.debit'), align: 'end' },
                  { label: t('common.credit'), align: 'end' },
                  { label: t('reports.runningBalance'), align: 'end' },
                ],
                rows: [
                  { style: 'muted' as const, cells: ['', '', ...(showAccount ? [''] : []), ...(showParty ? [''] : []), t('reports.opening'), '', '', f.balance(d?.opening)] },
                  ...(d?.rows ?? []).map((r) => ({
                    cells: [f.date(r.date), f.int(r.number), ...(showAccount ? [`${f.digits(r.account_code)} ${name({ name: r.account_name, name_en: r.account_name_en })}`] : []),
                      ...(showParty ? [r.party_name ?? ''] : []), r.description, Number(r.debit) ? f.money(r.debit) : '', Number(r.credit) ? f.money(r.credit) : '', f.balance(r.balance)],
                  })),
                ],
                footer: ['', '', ...(showAccount ? [''] : []), ...(showParty ? [''] : []), t('common.total'), f.money(d?.total_debit), f.money(d?.total_credit), f.balance(d?.closing)],
              }],
            })}
            columns={[
              { header: t('common.date'), value: (r) => f.date(r.date) },
              { header: t('common.number'), value: (r) => r.number, numeric: true },
              { header: t('journal.account'), value: (r) => `${r.account_code} ${name({ name: r.account_name, name_en: r.account_name_en })}` },
              { header: t('journal.party'), value: (r) => r.party_name ?? '' },
              { header: t('common.description'), value: (r) => r.description },
              { header: t('common.debit'), value: (r) => r.debit, numeric: true },
              { header: t('common.credit'), value: (r) => r.credit, numeric: true },
              { header: t('common.balance'), value: (r) => r.balance, numeric: true },
            ]}
          />
        </div>
      </div>
      {q.error && <div style={{ padding: 16 }}><Alert>{err(q.error)}</Alert></div>}
      {d?.truncated && <div style={{ padding: 16 }}><Alert kind="warning">{t('common.truncated', { count: f.count(d.rows.length) })}</Alert></div>}
      <DataTable
        rows={enabled ? d?.rows : []}
        loading={q.isLoading && enabled}
        rowKey={(r, i) => `${r.entry_id}-${i}`}
        onRowClick={(r) => navigate(`/journal/${r.entry_id}`)}
        showFooter
        columns={[
          { key: 'date', header: t('common.date'), cell: (r) => <span className="num">{f.date(r.date)}</span>, width: 110 },
          { key: 'number', header: t('common.number'), cell: (r) => <span className="num">{f.int(r.number)}</span>, width: 70 },
          { key: 'source', header: t('journal.source'), cell: src, width: 130 },
          ...(showAccount ? [{ key: 'account', header: t('journal.account'), cell: (r: LedgerRow) => <><span className="num muted">{f.digits(r.account_code)}</span> {name({ name: r.account_name, name_en: r.account_name_en })}</> }] : []),
          ...(showParty ? [{ key: 'party', header: t('journal.party'), cell: (r: LedgerRow) => r.party_name ?? '' }] : []),
          { key: 'desc', header: t('common.description'), cell: (r) => r.description, footer: t('common.total') },
          { key: 'debit', header: t('common.debit'), align: 'end', cell: (r) => <Amount value={r.debit} blankZero />, footer: <Amount value={d?.total_debit} /> },
          { key: 'credit', header: t('common.credit'), align: 'end', cell: (r) => <Amount value={r.credit} blankZero />, footer: <Amount value={d?.total_credit} /> },
          { key: 'balance', header: t('reports.runningBalance'), align: 'end', cell: (r) => <BalanceAmount value={r.balance} />, footer: <BalanceAmount value={d?.closing} /> },
        ]}
      />
    </>
  );
}
