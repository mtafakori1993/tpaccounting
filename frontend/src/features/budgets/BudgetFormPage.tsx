import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from 'react-router-dom';
import { Plus, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { Budget, BudgetPeriod } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button, IconButton } from '@/components/ui/Button';
import { Field, Input, MoneyInput, NumericInput, Select, TextArea } from '@/components/ui/Field';
import { Alert, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { AccountPicker, CostCenterSelect } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { add, toBig } from '@/lib/decimal';
import { percentsOf } from '@/lib/budget';
import { useFiscalYears, usePeriodName } from './common';

interface LineState {
  account_id: number | null;
  cost_center_id: number | null;
  allocation_method: 'manual' | 'equal' | 'percentage' | 'historical';
  annual_amount: string;
  amounts: string[];
  percents: string[];
  notes: string;
}

const blank = (n: number): LineState => ({ account_id: null, cost_center_id: null, allocation_method: 'equal', annual_amount: '', amounts: Array(n).fill(''), percents: Array(n).fill(''), notes: '' });

export function BudgetFormPage() {
  const { id } = useParams();
  const q = useQuery({ queryKey: ['budget', Number(id)], queryFn: () => api.get<Budget>(`/budgets/${id}`), enabled: !!id });
  if (id && !q.data) return <LoadingBlock />;
  return <BudgetForm budget={q.data} />;
}

function BudgetForm({ budget }: { budget?: Budget }) {
  const { t } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const { me } = useAuth();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const years = useFiscalYears();
  const [head, setHead] = useState({
    fiscal_year_id: budget?.fiscal_year_id ?? me?.fiscal_year?.id ?? (null as number | null), code: budget?.code ?? '', name: budget?.name ?? '',
    name_en: budget?.name_en ?? '', description: budget?.description ?? '', cost_center_id: budget?.cost_center_id ?? (null as number | null),
  });
  const fy = useQuery({
    queryKey: ['fiscal-year', head.fiscal_year_id],
    queryFn: () => api.get<{ periods: BudgetPeriod[] }>(`/company/fiscal-years/${head.fiscal_year_id}`),
    enabled: !!head.fiscal_year_id,
  });
  const periods = useMemo(() => fy.data?.periods ?? budget?.periods ?? [], [fy.data, budget]);
  const n = periods.length || 12;
  const periodName = usePeriodName();
  const [lines, setLines] = useState<LineState[]>(() => budget?.lines?.map((l) => ({
    account_id: l.account_id, cost_center_id: l.cost_center_id, allocation_method: l.allocation_method as LineState['allocation_method'],
    annual_amount: l.annual_amount, amounts: l.amounts, percents: percentsOf(l.amounts, l.annual_amount), notes: l.notes,
  })) ?? [blank(12)]);
  useEffect(() => {
    // Keep per-period arrays as long as the fiscal year has periods.
    setLines((ls) => ls.map((l) => ({ ...l, amounts: Array.from({ length: n }, (_, i) => l.amounts[i] ?? ''), percents: Array.from({ length: n }, (_, i) => l.percents[i] ?? '') })));
  }, [n]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const locked = !!budget && budget.status !== 'draft';
  const setLine = (i: number, patch: Partial<LineState>) => setLines(lines.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  const annualOf = (l: LineState) => (l.allocation_method === 'manual' ? add(...l.amounts.map((a) => a || '0')) : l.annual_amount || '0');
  const total = add(...lines.map(annualOf));
  const save = async () => {
    const body = {
      ...head, fiscal_year_id: head.fiscal_year_id ?? 0,
      lines: lines.filter((l) => l.account_id).map((l) => ({
        account_id: l.account_id, cost_center_id: l.cost_center_id, allocation_method: l.allocation_method, annual_amount: annualOf(l), notes: l.notes,
        amounts: l.allocation_method === 'manual' ? l.amounts.map((a) => a || '0') : undefined,
        percents: l.allocation_method === 'percentage' ? l.percents.map((p) => p || '0') : undefined,
      })),
    };
    const b = await run(() => (budget ? api.put<Budget>(`/budgets/${budget.id}`, body) : api.post<Budget>('/budgets', body)), t('common.saved'));
    if (b) {
      void qc.invalidateQueries({ queryKey: ['budgets'] });
      void qc.invalidateQueries({ queryKey: ['budget', b.id] });
      navigate(`/budgets/${b.id}`);
    }
  };
  const fieldErr = (i: number, k: string) => errors[`lines.${i}.${k}`];
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('budgets.title'), to: '/budgets' }]} title={budget ? `${t('budgets.editBudget')} — ${budget.name}` : t('budgets.newBudget')} subtitle={t('budgets.formSubtitle')}
        actions={!locked && <Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button>} />
      {locked && <Alert kind="warning">{t('budgets.readOnlyHelp')}</Alert>}
      {error && <Alert>{error}</Alert>}
      {fy.error && <Alert>{err(fy.error)}</Alert>}
      <Panel>
        <div className="form-grid">
          <Field label={t('budgets.fiscalYear')} required error={errors.fiscal_year_id}>
            <Select value={head.fiscal_year_id ?? ''} disabled={!!budget && budget.version > 1} onChange={(e) => setHead({ ...head, fiscal_year_id: e.target.value ? Number(e.target.value) : null })}>
              <option value="">{t('common.select')}</option>
              {years.data?.map((y) => <option key={y.id} value={y.id}>{f.digits(y.name)}</option>)}
            </Select>
          </Field>
          <Field label={t('budgets.code')} required error={errors.code}><Input value={head.code} disabled={!!budget && budget.version > 1} onChange={(e) => setHead({ ...head, code: e.target.value })} /></Field>
          <Field label={t('budgets.name')} required error={errors.name}><Input value={head.name} onChange={(e) => setHead({ ...head, name: e.target.value })} /></Field>
          <Field label={t('common.nameEn')}><Input value={head.name_en} onChange={(e) => setHead({ ...head, name_en: e.target.value })} dir="ltr" /></Field>
          <Field label={t('budgets.owner')} error={errors.cost_center_id}><CostCenterSelect value={head.cost_center_id} onChange={(x) => setHead({ ...head, cost_center_id: x })} /></Field>
          <Field label={t('common.description')} className="span-all"><TextArea rows={2} value={head.description} onChange={(e) => setHead({ ...head, description: e.target.value })} /></Field>
        </div>
      </Panel>
      <Panel bodyless title={t('budgets.lines')} actions={<span className="num">{t('budgets.total')}: {f.money(total)}</span>}>
        {errors.lines && <div style={{ padding: 16 }}><Alert>{t('budgets.emptyLines')}</Alert></div>}
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ minWidth: 260 }}>{t('budgets.account')}</th>
                <th style={{ minWidth: 160 }}>{t('budgets.costCenter')}</th>
                <th style={{ minWidth: 140 }}>{t('budgets.method')}</th>
                <th style={{ minWidth: 150 }} className="align-end">{t('budgets.annual')}</th>
                {Array.from({ length: n }, (_, i) => <th key={i} className="align-end" style={{ minWidth: 120 }}>{periods[i] ? periodName(periods[i], true) : f.int(i + 1)}</th>)}
                <th />
              </tr>
            </thead>
            <tbody>
              {lines.map((l, i) => {
                const pctTotal = add(...l.percents.map((p) => p || '0'));
                return (
                  <tr key={i}>
                    <td>
                      <AccountPicker includeGroups value={l.account_id} onChange={(x) => setLine(i, { account_id: x })} aria-invalid={!!fieldErr(i, 'account_id')} />
                      {fieldErr(i, 'account_id') && <span className="field-error">{t(`validation.${fieldErr(i, 'account_id') as 'invalid'}`)}</span>}
                    </td>
                    <td><CostCenterSelect value={l.cost_center_id} onChange={(x) => setLine(i, { cost_center_id: x })} /></td>
                    <td>
                      <Select value={l.allocation_method} disabled={locked} onChange={(e) => setLine(i, { allocation_method: e.target.value as LineState['allocation_method'] })}>
                        {(['equal', 'percentage', 'historical', 'manual'] as const).map((m) => <option key={m} value={m}>{t(`budgets.methods.${m}`)}</option>)}
                      </Select>
                    </td>
                    <td className="align-end">
                      {l.allocation_method === 'manual'
                        ? <span className="num">{f.money(annualOf(l))}</span>
                        : <MoneyInput className="grid-input" value={l.annual_amount} disabled={locked} onChange={(x) => setLine(i, { annual_amount: x })} aria-label={t('budgets.annual')} />}
                      {l.allocation_method === 'percentage' && !toBig(pctTotal).eq(100) && <span className="field-error">{t('budgets.percentTotal', { total: f.digits(pctTotal) })}</span>}
                    </td>
                    {Array.from({ length: n }, (_, p) => (
                      <td key={p} className="align-end">
                        {l.allocation_method === 'manual' && <MoneyInput className="grid-input" value={l.amounts[p] ?? ''} disabled={locked} onChange={(x) => setLine(i, { amounts: l.amounts.map((a, k) => (k === p ? x : a)) })} aria-label={periodName(periods[p], true)} />}
                        {l.allocation_method === 'percentage' && <NumericInput className="grid-input" fraction={4} value={l.percents[p] ?? ''} disabled={locked} onChange={(x) => setLine(i, { percents: l.percents.map((a, k) => (k === p ? x : a)) })} aria-label={`${periodName(periods[p], true)} %`} />}
                        {(l.allocation_method === 'equal' || l.allocation_method === 'historical') && <span className="muted num">{budget?.lines?.[i] && budget.lines[i].allocation_method === l.allocation_method ? f.money(budget.lines[i].amounts[p] ?? 0) : '…'}</span>}
                      </td>
                    ))}
                    <td>{!locked && <IconButton label={t('common.remove')} icon={<Trash2 size={16} />} onClick={() => setLines(lines.filter((_, j) => j !== i))} />}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {!locked && <div style={{ padding: 16 }}><Button icon={<Plus size={16} />} onClick={() => setLines([...lines, blank(n)])}>{t('budgets.addLine')}</Button></div>}
      </Panel>
    </div>
  );
}
