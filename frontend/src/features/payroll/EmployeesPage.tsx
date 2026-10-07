import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { Plus, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { Employee } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, NumericInput, PercentageInput, Select, TextArea } from '@/components/ui/Field';
import { EmptyState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { DateField } from '@/components/ui/DateField';
import { Alert, Amount, Badge, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { Attachments } from '@/components/Attachments';
import { CostCenterSelect } from '@/components/pickers/Pickers';
import { useList } from '@/lib/useList';
import { useSubmit } from '@/lib/useSubmit';
import { todayIso } from '@/lib/dates';
import { add } from '@/lib/decimal';
import { componentName, useSalaryComponents } from './common';

export function EmployeesPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const [editing, setEditing] = useState<number | 'new' | null>(null);
  const [sp, setSp] = useSearchParams();
  useEffect(() => {
    const e = Number(sp.get('edit'));
    if (e) { setEditing(e); setSp({}, { replace: true }); }
  }, [sp, setSp]);
  const list = useList<Employee, { q: string; department_id: string; active: string }>('employees', '/employees', { q: '', department_id: '', active: 'true' }, { key: 'code', desc: false });
  const rows = list.query.data?.items;
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('payroll.overviewTitle'), to: '/payroll' }]} title={t('payroll.employeesTitle')} subtitle={t('payroll.employeesSubtitle')} actions={<>
        <ExportMenu name="employees" permission="payroll.export" rows={rows} columns={[
          { header: t('payroll.code'), value: (e) => e.code },
          { header: t('payroll.fullName'), value: (e) => e.full_name },
          { header: t('payroll.nationalId'), value: (e) => e.national_id },
          { header: t('payroll.department'), value: (e) => e.department_name ?? '' },
          { header: t('payroll.position'), value: (e) => e.position },
          { header: t('payroll.hireDate'), value: (e) => f.date(e.hire_date) },
          { header: t('payroll.baseSalary'), value: (e) => e.base_salary, numeric: true },
          { header: t('payroll.iban'), value: (e) => e.iban },
        ]} />
        {can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('payroll.newEmployee')}</Button>}
      </>} />
      <Panel bodyless>
        <FilterBar activeCount={[list.filters.q, list.filters.department_id, list.filters.active !== 'true' ? 'x' : ''].filter(Boolean).length}
          onReset={list.resetFilters} basic={<>
          <Field label={t('common.search')} className="search"><Input type="search" value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('payroll.department')}>
            <CostCenterSelect kinds={['department']} emptyLabel={t('common.all')} value={Number(list.filters.department_id) || null} onChange={(x) => list.setFilter('department_id', x ? String(x) : '')} />
          </Field>
          <Field label={t('common.status')}>
            <Select value={list.filters.active} onChange={(e) => list.setFilter('active', e.target.value)}>
              <option value="">{t('common.all')}</option>
              <option value="true">{t('common.active')}</option>
              <option value="false">{t('common.inactive')}</option>
            </Select>
          </Field>
        </>} />
        <DataTable rows={rows} loading={list.query.isFetching} error={list.query.error} onRetry={() => void list.query.refetch()} rowKey={(e) => e.id} sort={list.sort} onSort={list.setSort} onRowClick={(e) => setEditing(e.id)}
          caption={t('payroll.employeesTitle')} columnsKey="payroll.employees"
          empty={list.filters.q || list.filters.department_id ? undefined : <EmptyState title={t('payroll.emptyEmployees')} body={t('payroll.emptyEmployeesBody')}
            action={can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('payroll.newEmployee')}</Button>} />}
          rowClassName={(e) => (e.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'code', header: t('payroll.code'), sortable: true, cell: (e) => <span className="num">{f.digits(e.code)}</span>, width: 100 },
            { key: 'name', header: t('payroll.fullName'), sortable: true, cell: (e) => e.full_name },
            { key: 'department', header: t('payroll.department'), sortable: true, cell: (e) => e.department_name ?? '' },
            { key: 'position', header: t('payroll.position'), optional: true, cell: (e) => e.position },
            { key: 'iban', header: t('payroll.iban'), optional: true, defaultHidden: true, cell: (e) => <span className="num" dir="ltr">{e.iban}</span> },
            { key: 'type', header: t('payroll.employmentType'), optional: true, cell: (e) => t(`payroll.employmentTypes.${e.employment_type as 'full_time'}`) },
            { key: 'hire_date', header: t('payroll.hireDate'), sortable: true, cell: (e) => <span className="num">{f.date(e.hire_date)}</span> },
            { key: 'base_salary', header: t('payroll.baseSalary'), sortable: true, align: 'end', cell: (e) => <Amount value={e.base_salary} /> },
            { key: 'status', header: t('common.status'), cell: (e) => (e.is_active ? <Badge tone="success">{t('common.active')}</Badge> : <Badge>{t('common.inactive')}</Badge>) },
          ]} />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
      {editing && <EmployeeDialog id={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void qc.invalidateQueries({ queryKey: ['employees'] }); }} />}
    </div>
  );
}

function EmployeeDialog({ id, onClose, onSaved }: { id?: number; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const q = useQuery({ queryKey: ['employee', id], queryFn: () => api.get<Employee>(`/employees/${id}`), enabled: !!id });
  if (id && !q.data) return <Dialog wide title={t('common.loading')} onClose={onClose}><LoadingBlock /></Dialog>;
  return <EmployeeForm employee={q.data} onClose={onClose} onSaved={onSaved} />;
}

function EmployeeForm({ employee: e, onClose, onSaved }: { employee?: Employee; onClose: () => void; onSaved: () => void }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const comps = useSalaryComponents();
  const [v, setV] = useState({
    code: e?.code ?? '', first_name: e?.first_name ?? '', last_name: e?.last_name ?? '', national_id: e?.national_id ?? '', personnel_number: e?.personnel_number ?? '',
    mobile: e?.mobile ?? '', email: e?.email ?? '', address: e?.address ?? '', bank_name: e?.bank_name ?? '', bank_account_number: e?.bank_account_number ?? '',
    iban: e?.iban ?? '', department_id: e?.department_id ?? (null as number | null), cost_center_id: e?.cost_center_id ?? (null as number | null),
    position: e?.position ?? '', employment_type: e?.employment_type ?? 'full_time', hire_date: e?.hire_date ?? todayIso(), termination_date: e?.termination_date ?? '',
    base_salary: e?.base_salary ?? '', children_count: String(e?.children_count ?? 0), marital_status: e?.marital_status ?? 'single',
    insurance_number: e?.insurance_number ?? '', insurance_exempt: e?.insurance_exempt ?? false, tax_exempt: e?.tax_exempt ?? false,
    is_active: e?.is_active ?? true, notes: e?.notes ?? '',
  });
  const [allocs, setAllocs] = useState(e?.allocations.map((a) => ({ cost_center_id: a.cost_center_id as number | null, percent: a.percent })) ?? []);
  const [overrides, setOverrides] = useState(e?.components.map((c) => ({ component_id: c.component_id as number | null, rate: c.rate })) ?? []);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const set = <K extends keyof typeof v>(k: K, x: (typeof v)[K]) => setV((s) => ({ ...s, [k]: x }));
  const editable = e ? can('payroll.edit') : can('payroll.create');
  const save = async () => {
    const body = {
      ...v, base_salary: v.base_salary || '0', children_count: Number(v.children_count) || 0, termination_date: v.termination_date || null,
      allocations: allocs.filter((a) => a.cost_center_id).map((a) => ({ cost_center_id: a.cost_center_id, percent: a.percent || '0' })),
      components: overrides.filter((o) => o.component_id).map((o) => ({ component_id: o.component_id, rate: o.rate || '0' })),
    };
    const ok = await run(() => (e ? api.put(`/employees/${e.id}`, body) : api.post('/employees', body)).then(() => true), t('common.saved'));
    if (ok) onSaved();
  };
  const allocTotal = allocs.reduce((s, a) => add(s, a.percent || '0'), '0');
  return (
    <Dialog wide title={e ? `${t('payroll.editEmployee')} — ${e.full_name}` : t('payroll.newEmployee')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button>{editable && <Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button>}</>}>
      <form className="stack" onSubmit={(ev) => { ev.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-section-title">{t('payroll.sections.identity')}</div>
        <div className="form-grid">
          <Field label={t('payroll.code')} required error={errors.code}><Input value={v.code} onChange={(x) => set('code', x.target.value)} autoFocus /></Field>
          <Field label={t('payroll.firstName')} required error={errors.first_name}><Input value={v.first_name} onChange={(x) => set('first_name', x.target.value)} /></Field>
          <Field label={t('payroll.lastName')} required error={errors.last_name}><Input value={v.last_name} onChange={(x) => set('last_name', x.target.value)} /></Field>
          <Field label={t('payroll.nationalId')} error={errors.national_id}><Input value={v.national_id} onChange={(x) => set('national_id', x.target.value)} inputMode="numeric" dir="ltr" /></Field>
          <Field label={t('payroll.personnelNumber')}><Input value={v.personnel_number} onChange={(x) => set('personnel_number', x.target.value)} /></Field>
          <Field label={t('payroll.mobile')}><Input value={v.mobile} onChange={(x) => set('mobile', x.target.value)} dir="ltr" /></Field>
          <Field label={t('payroll.email')} error={errors.email}><Input value={v.email} onChange={(x) => set('email', x.target.value)} dir="ltr" /></Field>
          <Field label={t('payroll.maritalStatus')}>
            <Select value={v.marital_status} onChange={(x) => set('marital_status', x.target.value)}>
              <option value="single">{t('payroll.marital.single')}</option>
              <option value="married">{t('payroll.marital.married')}</option>
            </Select>
          </Field>
          <Field label={t('payroll.childrenCount')} error={errors.children_count}><Input value={v.children_count} onChange={(x) => set('children_count', x.target.value)} inputMode="numeric" /></Field>
          <Field label={t('payroll.address')} className="span-all"><Input value={v.address} onChange={(x) => set('address', x.target.value)} /></Field>
        </div>
        <div className="form-section-title">{t('payroll.sections.employment')}</div>
        <div className="form-grid">
          <Field label={t('payroll.department')} error={errors.department_id}><CostCenterSelect kinds={['department']} value={v.department_id} onChange={(x) => set('department_id', x)} /></Field>
          <Field label={t('payroll.costCenter')} error={errors.cost_center_id}><CostCenterSelect value={v.cost_center_id} onChange={(x) => set('cost_center_id', x)} /></Field>
          <Field label={t('payroll.position')}><Input value={v.position} onChange={(x) => set('position', x.target.value)} /></Field>
          <Field label={t('payroll.employmentType')}>
            <Select value={v.employment_type} onChange={(x) => set('employment_type', x.target.value)}>
              {(['full_time', 'part_time', 'contract', 'hourly', 'intern'] as const).map((k) => <option key={k} value={k}>{t(`payroll.employmentTypes.${k}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('payroll.hireDate')} required error={errors.hire_date}><DateField value={v.hire_date} onChange={(x) => set('hire_date', x)} /></Field>
          <Field label={t('payroll.terminationDate')} error={errors.termination_date}><DateField value={v.termination_date} onChange={(x) => set('termination_date', x)} /></Field>
          <Field label={t('payroll.baseSalary')} required error={errors.base_salary}><MoneyInput value={v.base_salary} onChange={(x) => set('base_salary', x)} /></Field>
          {e && <div style={{ alignSelf: 'end' }}><Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} /></div>}
        </div>
        <div className="form-section-title">{t('payroll.sections.bank')}</div>
        <div className="form-grid">
          <Field label={t('payroll.bankName')}><Input value={v.bank_name} onChange={(x) => set('bank_name', x.target.value)} /></Field>
          <Field label={t('payroll.bankAccountNumber')}><Input value={v.bank_account_number} onChange={(x) => set('bank_account_number', x.target.value)} dir="ltr" /></Field>
          <Field label={t('payroll.iban')} error={errors.iban} className="span-2"><Input value={v.iban} onChange={(x) => set('iban', x.target.value)} dir="ltr" placeholder="IR…" /></Field>
        </div>
        <div className="form-section-title">{t('payroll.sections.insurance')}</div>
        <div className="form-grid">
          <Field label={t('payroll.insuranceNumber')}><Input value={v.insurance_number} onChange={(x) => set('insurance_number', x.target.value)} dir="ltr" /></Field>
          <div style={{ alignSelf: 'end' }}><Checkbox label={t('payroll.insuranceExempt')} checked={v.insurance_exempt} onChange={(x) => set('insurance_exempt', x)} /></div>
          <div style={{ alignSelf: 'end' }}><Checkbox label={t('payroll.taxExempt')} checked={v.tax_exempt} onChange={(x) => set('tax_exempt', x)} /></div>
        </div>
        <div className="form-section-title">{t('payroll.sections.allocation')}</div>
        <p className="muted">{t('payroll.allocationHelp')}</p>
        {errors.allocations && <Alert>{t('validation.allocation_total')}</Alert>}
        {allocs.map((a, i) => (
          <div key={i} className="row">
            <div className="grow"><CostCenterSelect value={a.cost_center_id} onChange={(x) => setAllocs(allocs.map((y, j) => (j === i ? { ...y, cost_center_id: x } : y)))} /></div>
            <PercentageInput value={a.percent} onChange={(x) => setAllocs(allocs.map((y, j) => (j === i ? { ...y, percent: x } : y)))} style={{ width: 110 }} aria-label={t('payroll.percent')} />
            <IconButton label={t('common.remove')} icon={<Trash2 size={16} />} onClick={() => setAllocs(allocs.filter((_, j) => j !== i))} />
          </div>
        ))}
        <div className="row-between">
          <Button size="sm" icon={<Plus size={14} />} onClick={() => setAllocs([...allocs, { cost_center_id: null, percent: '' }])}>{t('payroll.addAllocation')}</Button>
          {allocs.length > 0 && <span className={`num ${Number(allocTotal) === 100 ? 'success-text' : 'danger-text'}`}>{f.percent(allocTotal)}</span>}
        </div>
        <div className="form-section-title">{t('payroll.sections.overrides')}</div>
        <p className="muted">{t('payroll.overridesHelp')}</p>
        {overrides.map((o, i) => (
          <div key={i} className="row">
            <Select className="grow" value={o.component_id ?? ''} onChange={(x) => setOverrides(overrides.map((y, j) => (j === i ? { ...y, component_id: x.target.value ? Number(x.target.value) : null } : y)))}>
              <option value="">{t('common.select')}</option>
              {comps.data?.filter((c) => c.method !== 'manual' && c.method !== 'tax_brackets').map((c) => <option key={c.id} value={c.id}>{componentName(c, lang)}</option>)}
            </Select>
            <NumericInput fraction={4} value={o.rate} onChange={(x) => setOverrides(overrides.map((y, j) => (j === i ? { ...y, rate: x } : y)))} style={{ width: 160 }} aria-label={t('payroll.rate')} />
            <IconButton label={t('common.remove')} icon={<Trash2 size={16} />} onClick={() => setOverrides(overrides.filter((_, j) => j !== i))} />
          </div>
        ))}
        <div><Button size="sm" icon={<Plus size={14} />} onClick={() => setOverrides([...overrides, { component_id: null, rate: '' }])}>{t('payroll.addOverride')}</Button></div>
        <Field label={t('common.notes')}><TextArea rows={2} value={v.notes} onChange={(x) => set('notes', x.target.value)} /></Field>
        {e && <Attachments entity="employee" id={e.id} canEdit={can('payroll.edit')} />}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
