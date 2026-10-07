import { Link } from 'react-router-dom';
import { useI18n, type TKey } from '@/i18n/I18nProvider';
import { PageHeader, Panel } from '@/components/ui/Misc';

type ReportKey = 'journal' | 'ledger' | 'trial-balance' | 'party-statement' | 'profit-loss' | 'balance-sheet' | 'cash-position' | 'receivables' | 'payables' | 'sales' | 'purchases' | 'expenses' | 'cost-centers';

export const reportGroups: { title: TKey; reports: ReportKey[] }[] = [
  { title: 'reports.accounting', reports: ['journal', 'ledger', 'trial-balance', 'party-statement'] },
  { title: 'reports.financial', reports: ['profit-loss', 'balance-sheet', 'cash-position'] },
  { title: 'reports.operational', reports: ['receivables', 'payables', 'sales', 'purchases', 'expenses', 'cost-centers'] },
];

export function ReportsHome() {
  const { t } = useI18n();
  return (
    <div className="page">
      <PageHeader title={t('reports.title')} />
      <div className="report-groups">
        {reportGroups.map((g) => (
          <Panel key={g.title} title={t(g.title)} bodyless>
            {g.reports.map((r) => (
              <Link key={r} to={`/reports/${r}`} className="report-link">
                <div className="name">{t(`reports.names.${r}`)}</div>
                <div className="desc">{t(`reports.descriptions.${r}`)}</div>
              </Link>
            ))}
          </Panel>
        ))}
      </div>
    </div>
  );
}
