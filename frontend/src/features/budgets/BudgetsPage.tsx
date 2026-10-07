import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { PieChart, Plus, ShieldAlert } from 'lucide-react';
import { api } from '@/api/client';
import type { Budget } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { LinkButton } from '@/components/ui/Button';
import { Checkbox, Field, Select } from '@/components/ui/Field';
import { Amount, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { EmptyState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { DataTable } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { usePersistentState } from '@/lib/hooks';
import { useFiscalYears } from './common';

export const budgetStatuses = ['draft', 'submitted', 'approved', 'active', 'closed', 'archived'] as const;

export function BudgetsPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const years = useFiscalYears();
  const [flt, setFlt] = usePersistentState('filters.budgets', { fiscal_year_id: '', status: '', latest: true });
  const q = useQuery({ queryKey: ['budgets', flt], queryFn: () => api.get<Budget[]>('/budgets', flt) });
  return (
    <div className="page">
      <PageHeader title={t('budgets.title')} subtitle={t('budgets.listSubtitle')} actions={<>
        <LinkButton to="/budgets/dashboard" icon={<PieChart size={16} />}>{t('budgets.dashboardTitle')}</LinkButton>
        <LinkButton to="/budgets/control" icon={<ShieldAlert size={16} />}>{t('budgets.controlTitle')}</LinkButton>
        <ExportMenu name="budgets" permission="budgets.export" rows={q.data} columns={[
          { header: t('budgets.code'), value: (b) => b.code },
          { header: t('budgets.name'), value: (b) => b.name },
          { header: t('budgets.version'), value: (b) => b.version, numeric: true },
          { header: t('budgets.fiscalYear'), value: (b) => b.fiscal_year_name },
          { header: t('budgets.owner'), value: (b) => b.cost_center_name ?? '' },
          { header: t('budgets.expenseTotal'), value: (b) => b.expense_total, numeric: true },
          { header: t('budgets.incomeTotal'), value: (b) => b.income_total, numeric: true },
          { header: t('common.status'), value: (b) => t(`budgets.statuses.${b.status as 'draft'}`) },
        ]} />
        {can('budgets.create') && <LinkButton to="/budgets/new" variant="accent" icon={<Plus size={16} />}>{t('budgets.newBudget')}</LinkButton>}
      </>} />
      <Panel bodyless>
        <FilterBar activeCount={[flt.fiscal_year_id, flt.status, flt.latest ? '' : 'x'].filter(Boolean).length} onReset={() => setFlt({ fiscal_year_id: '', status: '', latest: true })} basic={<>
          <Field label={t('budgets.fiscalYear')}>
            <Select value={flt.fiscal_year_id} onChange={(e) => setFlt({ ...flt, fiscal_year_id: e.target.value })}>
              <option value="">{t('common.all')}</option>
              {years.data?.map((y) => <option key={y.id} value={y.id}>{f.digits(y.name)}</option>)}
            </Select>
          </Field>
          <Field label={t('common.status')}>
            <Select value={flt.status} onChange={(e) => setFlt({ ...flt, status: e.target.value })}>
              <option value="">{t('common.all')}</option>
              {budgetStatuses.map((s) => <option key={s} value={s}>{t(`budgets.statuses.${s}`)}</option>)}
            </Select>
          </Field>
          <div style={{ alignSelf: 'end', paddingBottom: 6 }}><Checkbox label={t('budgets.latestOnly')} checked={flt.latest} onChange={(x) => setFlt({ ...flt, latest: x })} /></div>
        </>} />
        <DataTable rows={q.data} loading={q.isFetching} error={q.error} onRetry={() => void q.refetch()} rowKey={(b) => b.id} onRowClick={(b) => navigate(`/budgets/${b.id}`)} caption={t('budgets.title')}
          empty={flt.status || flt.fiscal_year_id ? undefined : <EmptyState title={t('budgets.emptyBudgets')} body={t('budgets.emptyBudgetsBody')}
            action={can('budgets.create') && <LinkButton to="/budgets/new" variant="accent" icon={<Plus size={16} />}>{t('budgets.newBudget')}</LinkButton>} />}
          columns={[
            { key: 'code', header: t('budgets.code'), cell: (b) => <span className="num">{f.digits(b.code)}</span>, width: 100 },
            { key: 'name', header: t('budgets.name'), cell: (b) => b.name },
            { key: 'version', header: t('budgets.version'), cell: (b) => <span className="num">{t('budgets.versionN', { n: f.int(b.version) })}</span>, width: 90 },
            { key: 'fy', header: t('budgets.fiscalYear'), cell: (b) => f.digits(b.fiscal_year_name), width: 100 },
            { key: 'owner', header: t('budgets.owner'), cell: (b) => b.cost_center_name ?? '' },
            { key: 'exp', header: t('budgets.expenseTotal'), align: 'end', cell: (b) => <Amount value={b.expense_total} blankZero /> },
            { key: 'inc', header: t('budgets.incomeTotal'), align: 'end', cell: (b) => <Amount value={b.income_total} blankZero /> },
            { key: 'status', header: t('common.status'), cell: (b) => <StatusBadge status={b.status} />, width: 110 },
          ]} />
      </Panel>
    </div>
  );
}
