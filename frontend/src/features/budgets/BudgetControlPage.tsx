import { useState, type ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { Budget, BudgetEvaluation, BudgetEventDetail, BudgetRule, Role } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, PercentageInput, Select } from '@/components/ui/Field';
import { EmptyState, ErrorState } from '@/components/ui/Feedback';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { sourceRoute } from '@/features/journal/sourceLinks';
import { Alert, Amount, Badge, PageHeader, Panel, Tabs } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { DateField } from '@/components/ui/DateField';
import { ExportMenu } from '@/components/ExportMenu';
import { Meter } from '@/components/charts/BarChart';
import { AccountPicker, CostCenterSelect } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { todayIso } from '@/lib/dates';

const policyTone = { none: undefined, warning: 'info', soft: 'warning', hard: 'danger' } as const;
const outcomeTone = { warned: 'info', confirmed: 'warning', overridden: 'danger', blocked: 'danger' } as const;

export function BudgetControlPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const [editing, setEditing] = useState<BudgetRule | 'new' | null>(null);
  const [event, setEvent] = useState<BudgetEventDetail | null>(null);
  const [outcome, setOutcome] = useState<'all' | 'overridden' | 'blocked' | 'confirmed' | 'warned'>('all');
  const [deleting, setDeleting] = useState<BudgetRule | null>(null);
  const del = useSubmit();
  const q = useQuery({ queryKey: ['budget-control'], queryFn: () => api.get<{ rules: BudgetRule[]; events: BudgetEventDetail[] }>('/budgets/control') });
  const events = q.data?.events ?? [];
  const shown = outcome === 'all' ? events : events.filter((e) => e.outcome === outcome);
  const countOf = (o: string) => events.filter((e) => e.outcome === o).length;
  const reload = () => void qc.invalidateQueries({ queryKey: ['budget-control'] });
  const any = (v: string | null | undefined, all: string) => v ?? <span className="muted">{all}</span>;
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('budgets.title'), to: '/budgets' }]} title={t('budgets.controlTitle')} subtitle={t('budgets.controlSubtitle')}
        actions={can('budgets.edit') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('budgets.control.newRule')}</Button>} />
      {q.error && !q.data && <Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel>}
      <KpiGrid loading={q.isLoading} count={4}>
        <Kpi label={t('budgets.control.rules')} value={f.count(q.data?.rules.filter((r) => r.is_active).length ?? 0)} sub={t('budgets.control.defaultRule')} />
        <Kpi label={t('budgets.decisions.overridden')} value={f.count(countOf('overridden'))} tone={countOf('overridden') ? 'danger' : 'neutral'} />
        <Kpi label={t('budgets.decisions.blocked')} value={f.count(countOf('blocked'))} tone={countOf('blocked') ? 'warning' : 'neutral'} />
        <Kpi label={t('budgets.decisions.confirmed')} value={f.count(countOf('confirmed'))} sub={`${t('budgets.decisions.warned')}: ${f.count(countOf('warned'))}`} />
      </KpiGrid>
      <Panel bodyless title={t('budgets.control.rules')}>
        <div className="filter-bar"><span className="muted">{t('budgets.control.specificity')}</span></div>
        <DataTable rows={q.data?.rules} loading={q.isLoading} rowKey={(r) => r.id} onRowClick={can('budgets.edit') ? setEditing : undefined} caption={t('budgets.control.rules')}
          empty={<EmptyState compact title={t('budgets.noRules')} body={t('budgets.noRulesBody')} action={can('budgets.edit') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('budgets.control.newRule')}</Button>} />}
          rowClassName={(r) => (r.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'name', header: t('common.name'), cell: (r) => r.name || '—' },
            { key: 'policy', header: t('budgets.control.policy'), cell: (r) => <Badge tone={policyTone[r.policy]}>{t(`budgets.control.policies.${r.policy}`)}</Badge> },
            { key: 'budget', header: t('budgets.title'), cell: (r) => any(r.budget_name, t('budgets.control.allBudgets')) },
            { key: 'account', header: t('budgets.account'), cell: (r) => any(r.account_code && `${f.digits(r.account_code)} ${r.account_name}`, t('budgets.control.allAccounts')) },
            { key: 'cc', header: t('budgets.costCenter'), cell: (r) => any(r.cost_center_name, t('budgets.control.allCostCenters')) },
            { key: 'role', header: t('budgets.control.role'), cell: (r) => any(r.role_name, t('budgets.control.allRoles')) },
            { key: 'th', header: t('budgets.control.threshold'), align: 'end', cell: (r) => <span className="num">{f.percent(r.threshold_percent)}</span> },
            { key: 'basis', header: t('budgets.control.basis'), cell: (r) => t(`budgets.control.bases.${r.basis}`) },
            { key: 'x', header: '', cell: (r) => can('budgets.edit') && <Button size="sm" variant="quiet" onClick={(e) => { e.stopPropagation(); setDeleting(r); }}>{t('common.delete')}</Button> },
          ]} />
      </Panel>
      <Simulation />
      <Panel bodyless title={t('budgets.control.events')} actions={<ExportMenu name="budget-events" permission="budgets.export" rows={shown} columns={[
        { header: t('budgets.event.timestamp'), value: (e) => f.dateTime(e.created_at) },
        { header: t('budgets.event.budget'), value: (e) => e.budget_name ?? '' },
        { header: t('budgets.event.account'), value: (e) => `${e.account_code ?? ''} ${e.account_name ?? ''}` },
        { header: t('budgets.event.costCenter'), value: (e) => e.cost_center_name ?? '' },
        { header: t('budgets.event.transaction'), value: (e) => `${e.source_type ? t(`journal.sources.${e.source_type as 'manual'}`) : ''} ${e.entry_reference}`.trim() },
        { header: t('budgets.event.policy'), value: (e) => t(`budgets.control.policies.${e.policy as 'hard'}`) },
        { header: t('budgets.event.originalBudget'), value: (e) => e.budget_amount, numeric: true },
        { header: t('budgets.event.limit'), value: (e) => e.limit_amount ?? '', numeric: true },
        { header: t('budgets.event.actual'), value: (e) => e.actual_amount, numeric: true },
        { header: t('budgets.event.requested'), value: (e) => e.amount, numeric: true },
        { header: t('budgets.event.exceeded'), value: (e) => e.exceeded_amount ?? '', numeric: true },
        { header: t('budgets.event.user'), value: (e) => e.user_name ?? '' },
        { header: t('budgets.event.role'), value: (e) => e.role_code },
        { header: t('budgets.event.reason'), value: (e) => e.reason },
        { header: t('budgets.event.decision'), value: (e) => t(`budgets.decisions.${e.outcome as 'warned'}`) },
      ]} />}>
        <Tabs value={outcome} onChange={setOutcome} label={t('budgets.control.outcome')} tabs={[
          { key: 'all', label: t('common.all'), count: f.count(events.length) },
          { key: 'overridden', label: t('budgets.decisions.overridden'), count: countOf('overridden') ? f.count(countOf('overridden')) : undefined, tone: 'danger' },
          { key: 'blocked', label: t('budgets.decisions.blocked'), count: countOf('blocked') ? f.count(countOf('blocked')) : undefined, tone: 'warning' },
          { key: 'confirmed', label: t('budgets.decisions.confirmed') },
          { key: 'warned', label: t('budgets.decisions.warned') },
        ]} />
        <DataTable rows={shown} loading={q.isLoading} rowKey={(e) => e.id} onRowClick={setEvent} caption={t('budgets.control.events')}
          empty={<EmptyState compact title={t('budgets.noEvents')} body={t('budgets.noEventsBody')} />}
          columns={[
            { key: 'd', header: t('budgets.event.timestamp'), cell: (e) => <span className="num">{f.dateTime(e.created_at)}</span>, width: 150 },
            { key: 'o', header: t('budgets.event.decision'), cell: (e) => <Badge tone={outcomeTone[e.outcome as 'warned']}>{t(`budgets.decisions.${e.outcome as 'warned'}`)}</Badge> },
            { key: 'b', header: t('budgets.event.budget'), cell: (e) => e.budget_name ?? '' },
            { key: 'a', header: t('budgets.event.account'), cell: (e) => `${f.digits(e.account_code ?? '')} ${e.account_name ?? ''}${e.cost_center_name ? ` — ${e.cost_center_name}` : ''}` },
            { key: 'amt', header: t('budgets.event.requested'), align: 'end', cell: (e) => <Amount value={e.amount} /> },
            { key: 'ex', header: t('budgets.event.exceeded'), align: 'end', cell: (e) => <span className="danger-text"><Amount value={e.exceeded_amount ?? '0'} blankZero /></span> },
            { key: 'u', header: t('budgets.event.user'), cell: (e) => e.user_name ?? '' },
            { key: 'r', header: t('budgets.event.reason'), cell: (e) => <span className="truncate" title={e.reason}>{e.reason}</span> },
          ]} />
      </Panel>
      {event && <EventDialog event={event} onClose={() => setEvent(null)} />}
      {editing && <RuleDialog rule={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); reload(); }} />}
      {deleting && (
        <ConfirmDialog title={t('common.delete')} body={t('common.confirmDelete')} confirmLabel={t('common.yesDelete')} negative pending={del.pending} error={del.error}
          onClose={() => setDeleting(null)} onConfirm={async () => { if (await del.run(() => api.del(`/budgets/control/rules/${deleting.id}`).then(() => true), t('common.deleted'))) { setDeleting(null); reload(); } }} />
      )}
    </div>
  );
}

function RuleDialog({ rule, onClose, onSaved }: { rule?: BudgetRule; onClose: () => void; onSaved: () => void }) {
  const { t, lang } = useI18n();
  const { can } = useAuth();
  const budgets = useQuery({ queryKey: ['budgets', 'active'], queryFn: () => api.get<Budget[]>('/budgets', { status: 'active' }) });
  const roles = useQuery({ queryKey: ['roles'], queryFn: () => api.get<Role[]>('/roles'), enabled: can('roles.view') });
  const [v, setV] = useState({
    name: rule?.name ?? '', policy: rule?.policy ?? 'warning', threshold_percent: rule?.threshold_percent ?? '100', basis: rule?.basis ?? 'ytd',
    budget_id: rule?.budget_id ?? (null as number | null), account_id: rule?.account_id ?? (null as number | null), cost_center_id: rule?.cost_center_id ?? (null as number | null),
    role_id: rule?.role_id ?? (null as number | null), is_active: rule?.is_active ?? true,
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const ok = await run(() => (rule ? api.put(`/budgets/control/rules/${rule.id}`, v) : api.post('/budgets/control/rules', v)).then(() => true), t('common.saved'));
    if (ok) onSaved();
  };
  return (
    <Dialog size="wide" title={rule ? t('budgets.control.editRule') : t('budgets.control.newRule')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        <Alert kind="info">{t(`budgets.control.policyHelp.${v.policy}`)}</Alert>
        <div className="form-grid">
          <Field label={t('common.name')}><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} autoFocus /></Field>
          <Field label={t('budgets.control.policy')} error={errors.policy}>
            <Select value={v.policy} onChange={(e) => setV({ ...v, policy: e.target.value as BudgetRule['policy'] })}>
              {(['none', 'warning', 'soft', 'hard'] as const).map((p) => <option key={p} value={p}>{t(`budgets.control.policies.${p}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('budgets.control.threshold')} error={errors.threshold_percent}><PercentageInput value={v.threshold_percent} onChange={(x) => setV({ ...v, threshold_percent: x })} /></Field>
          <Field label={t('budgets.control.basis')} error={errors.basis}>
            <Select value={v.basis} onChange={(e) => setV({ ...v, basis: e.target.value as BudgetRule['basis'] })}>
              {(['period', 'ytd', 'annual'] as const).map((b) => <option key={b} value={b}>{t(`budgets.control.bases.${b}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('budgets.title')} error={errors.budget_id}>
            <Select value={v.budget_id ?? ''} onChange={(e) => setV({ ...v, budget_id: e.target.value ? Number(e.target.value) : null })}>
              <option value="">{t('budgets.control.allBudgets')}</option>
              {budgets.data?.map((b) => <option key={b.id} value={b.id}>{lang === 'en' && b.name_en ? b.name_en : b.name}</option>)}
            </Select>
          </Field>
          <Field label={t('budgets.account')} error={errors.account_id} className="span-2">
            <AccountPicker includeGroups value={v.account_id} onChange={(x) => setV({ ...v, account_id: x })} filter={(a) => a.type === 'expense' || a.type === 'cost_of_sales'} />
          </Field>
          <Field label={t('budgets.costCenter')} error={errors.cost_center_id}>
            <CostCenterSelect emptyLabel={t('budgets.control.allCostCenters')} value={v.cost_center_id} onChange={(x) => setV({ ...v, cost_center_id: x })} />
          </Field>
          {roles.data && (
            <Field label={t('budgets.control.role')} error={errors.role_id}>
              <Select value={v.role_id ?? ''} onChange={(e) => setV({ ...v, role_id: e.target.value ? Number(e.target.value) : null })}>
                <option value="">{t('budgets.control.allRoles')}</option>
                {roles.data.map((r) => <option key={r.id} value={r.id}>{lang === 'en' && r.name_en ? r.name_en : r.name}</option>)}
              </Select>
            </Field>
          )}
          {rule && <div style={{ alignSelf: 'end' }}><Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => setV({ ...v, is_active: x })} /></div>}
        </div>
      </div>
    </Dialog>
  );
}

function Simulation() {
  const { t } = useI18n();
  const f = useFormat();
  const [v, setV] = useState({ account_id: null as number | null, cost_center_id: null as number | null, amount: '', date: todayIso() });
  const [result, setResult] = useState<BudgetEvaluation[] | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const check = async () => {
    const r = await run(() => api.post<BudgetEvaluation[]>('/budgets/control/check', { ...v, account_id: v.account_id ?? 0, amount: v.amount || '0' }));
    if (r) setResult(r);
  };
  return (
    <Panel title={t('budgets.control.simulate')}>
      <div className="stack">
        <p className="muted">{t('budgets.control.simulateHelp')}</p>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('budgets.account')} required error={errors.account_id} className="span-2">
            <AccountPicker value={v.account_id} onChange={(x) => setV({ ...v, account_id: x })} filter={(a) => a.type === 'expense' || a.type === 'cost_of_sales'} />
          </Field>
          <Field label={t('budgets.costCenter')}><CostCenterSelect value={v.cost_center_id} onChange={(x) => setV({ ...v, cost_center_id: x })} /></Field>
          <Field label={t('common.amount')} required error={errors.amount}><MoneyInput value={v.amount} onChange={(x) => setV({ ...v, amount: x })} /></Field>
          <Field label={t('common.date')}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
          <div style={{ alignSelf: 'end' }}><Button variant="accent" pending={pending} onClick={check}>{t('budgets.control.check')}</Button></div>
        </div>
        {result && result.length === 0 && <Alert kind="info">{t('budgets.control.noBudgetLine')}</Alert>}
        {result && result.length > 0 && (
          <DataTable rows={result} rowKey={(r) => r.line_id}
            columns={[
              { key: 'b', header: t('budgets.title'), cell: (r) => r.budget_name },
              { key: 'p', header: t('budgets.control.policy'), cell: (r) => <Badge tone={policyTone[r.policy as 'hard']}>{t(`budgets.control.policies.${r.policy as 'hard'}`)}</Badge> },
              { key: 'basis', header: t('budgets.control.basis'), cell: (r) => t(`budgets.control.bases.${r.basis as 'ytd'}`) },
              { key: 'bud', header: t('budgets.budget'), align: 'end', cell: (r) => <Amount value={r.budget} /> },
              { key: 'act', header: t('budgets.actual'), align: 'end', cell: (r) => <Amount value={r.actual} /> },
              { key: 'rem', header: t('budgets.remaining'), align: 'end', cell: (r) => <Amount value={r.remaining} signed /> },
              { key: 'u', header: t('budgets.utilization'), cell: (r) => <Meter percent={r.utilization} format={(x) => f.percent(x)} />, width: 160 },
              { key: 'x', header: '', cell: (r) => (r.exceeded ? <Badge tone="danger">{t('budgets.control.wouldExceed')}</Badge> : <Badge tone="success">{t('budgets.control.withinBudget')}</Badge>) },
            ]} />
        )}
      </div>
    </Panel>
  );
}

/** Everything recorded about one budget-control decision. */
function EventDialog({ event: e, onClose }: { event: BudgetEventDetail; onClose: () => void }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const src = e.source_type ? sourceRoute(e.source_type, e.source_id) : null;
  const row = (label: string, value: ReactNode) => <><dt>{label}</dt><dd>{value || '—'}</dd></>;
  return (
    <Dialog size="wide" title={t('budgets.eventTitle')} description={<Badge tone={outcomeTone[e.outcome as 'warned']}>{t(`budgets.decisions.${e.outcome as 'warned'}`)}</Badge>} onClose={onClose}
      footer={<Button onClick={onClose}>{t('common.close')}</Button>}>
      <div className="grid-2">
        <dl className="dl-grid">
          {row(t('budgets.event.budget'), e.budget_id ? <Link to={`/budgets/${e.budget_id}`}>{e.budget_name}</Link> : e.budget_name)}
          {row(t('budgets.event.account'), e.account_code && `${f.digits(e.account_code)} ${e.account_name ?? ''}`)}
          {row(t('budgets.event.costCenter'), e.cost_center_name)}
          {row(t('budgets.event.transaction'), e.journal_entry_id ? (
            <>{src ? <Link to={src}>{tryT(`journal.sources.${e.source_type}`) ?? e.source_type}</Link> : tryT(`journal.sources.${e.source_type}`)} {e.entry_reference && <span className="muted">{f.digits(e.entry_reference)}</span>} · <Link to={`/journal/${e.journal_entry_id}`}>{t('budgets.journalEntry')} {f.digits(String(e.journal_number ?? ''))}</Link></>
          ) : <span className="danger-text">{t('budgets.event.notPosted')}{e.source_type ? ` — ${tryT(`journal.sources.${e.source_type}`) ?? e.source_type}` : ''}</span>)}
          {row(t('common.description'), e.entry_description)}
          {row(t('budgets.event.user'), e.user_name)}
          {row(t('budgets.event.role'), e.role_code)}
          {row(t('budgets.event.timestamp'), <span className="num">{f.dateTime(e.created_at)}</span>)}
        </dl>
        <dl className="dl-grid">
          {row(t('budgets.event.policy'), <Badge tone={policyTone[e.policy as 'hard']}>{t(`budgets.control.policies.${e.policy as 'hard'}`)}</Badge>)}
          {row(t('budgets.event.basis'), e.basis && t(`budgets.control.bases.${e.basis as 'ytd'}`))}
          {row(t('budgets.event.threshold'), e.threshold_percent && f.percent(e.threshold_percent))}
          {row(t('budgets.event.originalBudget'), <Amount value={e.budget_amount} />)}
          {row(t('budgets.event.limit'), e.limit_amount && <Amount value={e.limit_amount} />)}
          {row(t('budgets.event.requested'), <Amount value={e.amount} />)}
          {row(t('budgets.event.actual'), <Amount value={e.actual_amount} />)}
          {row(t('budgets.event.exceeded'), e.exceeded_amount && <strong className="danger-text"><Amount value={e.exceeded_amount} /></strong>)}
        </dl>
      </div>
      {e.reason && <div style={{ marginTop: 16 }}><Alert kind={e.outcome === 'overridden' ? 'warning' : 'info'}><strong>{t('budgets.event.reason')}:</strong> {e.reason}</Alert></div>}
    </Dialog>
  );
}
