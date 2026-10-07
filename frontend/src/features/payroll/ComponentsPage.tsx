import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Scale, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { Bracket, PayrollRules, SalaryComponent } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton, LinkButton } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, NumericInput, PercentageInput, Select, TextArea } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Badge, EmptyState, PageHeader, Panel } from '@/components/ui/Misc';
import { ErrorState } from '@/components/ui/Feedback';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { AccountPicker } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { componentName, useSalaryComponents } from './common';

const methods = ['fixed', 'base_percent', 'daily_base', 'hourly_base', 'per_unit', 'per_child', 'percent_of_insurable', 'percent_of_gross', 'formula', 'tax_table', 'insurance_rule', 'tax_brackets', 'manual'] as const;
const categories = ['salary', 'allowance', 'overtime', 'bonus', 'insurance', 'tax', 'loan', 'advance', 'unpaid_leave', 'other'] as const;
const qtyFields = ['', 'working_days', 'working_hours', 'absence_days', 'unpaid_leave_days', 'paid_leave_days', 'overtime_hours', 'mission_days', 'holiday_work_hours'] as const;

export function ComponentsPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const comps = useSalaryComponents();
  const [editing, setEditing] = useState<SalaryComponent | 'new' | null>(null);
  const install = useSubmit();
  const reload = () => void qc.invalidateQueries({ queryKey: ['salary-components'] });
  const rateText = (c: SalaryComponent) => {
    if (c.method === 'manual') return '—';
    if (c.method === 'tax_table' || c.method === 'insurance_rule') return t('payroll.ruleSourced');
    if (c.method === 'formula') return c.formula;
    if (c.method === 'tax_brackets') return c.brackets.map((b) => f.percent(b.rate)).join(' / ');
    if (['fixed', 'per_unit', 'per_child'].includes(c.method)) return f.money(c.rate);
    return f.percent(c.rate);
  };
  return (
    <div className="page">
      <PageHeader title={t('payroll.componentsTitle')} subtitle={t('payroll.componentsSubtitle')} actions={<>
        <LinkButton to="/payroll/settings" icon={<Scale size={16} />}>{t('nav.payrollSettings')}</LinkButton>
        {can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('payroll.newComponent')}</Button>}
      </>} />
      {install.error && <div className="section-gap"><Alert>{install.error}</Alert></div>}
      {comps.error && <Panel bodyless><ErrorState error={comps.error} onRetry={() => void comps.refetch()} /></Panel>}
      {(['earning', 'deduction', 'employer'] as const).map((kind) => (
        <Panel key={kind} bodyless title={t(`payroll.kinds.${kind}`)}>
          <DataTable rows={comps.data?.filter((c) => c.kind === kind)} loading={comps.isLoading} rowKey={(c) => c.id} caption={t(`payroll.kinds.${kind}`)}
            onRowClick={can('payroll.edit') ? (c) => setEditing(c) : undefined} rowClassName={(c) => (c.is_active ? undefined : 'muted-row')}
            empty={<EmptyState title={t('payroll.noComponents')} action={kind === 'earning' && can('payroll.create') && (
              <Button pending={install.pending} onClick={() => install.run(() => api.post('/payroll/components/defaults').then(reload), t('common.saved'))}>{t('payroll.installDefaults')}</Button>
            )} />}
            columns={[
              { key: 'seq', header: t('payroll.seq'), cell: (c) => <span className="num">{f.int(c.seq)}</span>, width: 70 },
              { key: 'name', header: t('common.name'), cell: (c) => componentName(c, lang) },
              { key: 'cat', header: t('payroll.category'), cell: (c) => t(`payroll.categories.${c.category as 'other'}`) },
              { key: 'method', header: t('payroll.method'), cell: (c) => t(`payroll.methods.${c.method as 'fixed'}`) },
              { key: 'rate', header: t('payroll.rate'), align: 'end', cell: (c) => <span className={c.method === 'formula' ? 'muted' : 'num'} dir={c.method === 'formula' ? 'ltr' : undefined}>{rateText(c)}</span> },
              { key: 'var', header: t('payroll.variability'), cell: (c) => t(`payroll.variabilities.${c.variability ?? 'fixed'}`) },
              { key: 'eff', header: t('ui.effectiveFrom'), cell: (c) => (c.effective_from || c.effective_to ? <span className="num">{f.date(c.effective_from) || '…'} – {f.date(c.effective_to) || '…'}</span> : <span className="muted">—</span>) },
              { key: 'flags', header: '', cell: (c) => <span className="inline-list">{c.taxable && <Badge>{t('payroll.taxable')}</Badge>} {c.insurable && <Badge>{t('payroll.insurable')}</Badge>} {!c.is_active && <Badge tone="warning">{t('common.inactive')}</Badge>}</span> },
            ]} />
        </Panel>
      ))}
      {editing && <ComponentDialog component={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); reload(); }} />}
    </div>
  );
}

function ComponentDialog({ component: c, onClose, onSaved }: { component?: SalaryComponent; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const [v, setV] = useState({
    code: c?.code ?? '', name: c?.name ?? '', name_en: c?.name_en ?? '', kind: c?.kind ?? ('earning' as SalaryComponent['kind']), category: c?.category ?? 'allowance',
    method: c?.method ?? 'fixed', rate: c?.rate ?? '', quantity_field: c?.quantity_field ?? '', exemption: c?.exemption ?? '0', ceiling: c?.ceiling ?? '0',
    taxable: c?.taxable ?? true, insurable: c?.insurable ?? true, account_id: c?.account_id ?? (null as number | null), seq: String(c?.seq ?? 100), is_active: c?.is_active ?? true,
    variability: c?.variability ?? ('fixed' as 'fixed' | 'variable'), formula: c?.formula ?? '', insurance_share: c?.insurance_share ?? '',
    effective_from: c?.effective_from ?? '', effective_to: c?.effective_to ?? '',
  });
  const rules = useQuery({ queryKey: ['payroll-rules'], queryFn: () => api.get<PayrollRules>('/payroll/rules'), staleTime: 60_000 });
  const [brackets, setBrackets] = useState<Bracket[]>(c?.brackets ?? []);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const set = <K extends keyof typeof v>(k: K, x: (typeof v)[K]) => setV((s) => ({ ...s, [k]: x }));
  const save = async () => {
    const body = { ...v, rate: v.rate || '0', exemption: v.exemption || '0', ceiling: v.ceiling || '0', seq: Number(v.seq) || 0,
      effective_from: v.effective_from || null, effective_to: v.effective_to || null,
      brackets: brackets.map((b) => ({ upto: b.upto || null, rate: b.rate || '0' })) };
    const ok = await run(() => (c ? api.put(`/payroll/components/${c.id}`, body) : api.post('/payroll/components', body)).then(() => true), t('common.saved'));
    if (ok) onSaved();
  };
  const earning = v.kind === 'earning';
  return (
    <Dialog size="wide" title={c ? t('payroll.editComponent') : t('payroll.newComponent')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('common.code')} required error={errors.code} help="a-z, 0-9, _"><Input value={v.code} onChange={(e) => set('code', e.target.value.toLowerCase())} dir="ltr" autoFocus /></Field>
          <Field label={t('common.name')} required error={errors.name}><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
          <Field label={t('common.nameEn')}><Input value={v.name_en} onChange={(e) => set('name_en', e.target.value)} dir="ltr" /></Field>
          <Field label={t('payroll.kind')} error={errors.kind}>
            <Select value={v.kind} onChange={(e) => set('kind', e.target.value as SalaryComponent['kind'])}>
              {(['earning', 'deduction', 'employer'] as const).map((k) => <option key={k} value={k}>{t(`payroll.kinds.${k}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('payroll.category')} error={errors.category}>
            <Select value={v.category} onChange={(e) => set('category', e.target.value)}>
              {categories.map((k) => <option key={k} value={k}>{t(`payroll.categories.${k}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('payroll.method')} error={errors.method}>
            <Select value={v.method} onChange={(e) => set('method', e.target.value)}>
              {methods.map((k) => <option key={k} value={k}>{t(`payroll.methods.${k}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('payroll.variability')} error={errors.variability}>
            <Select value={v.variability} onChange={(e) => set('variability', e.target.value as 'fixed' | 'variable')}>
              <option value="fixed">{t('payroll.variabilities.fixed')}</option>
              <option value="variable">{t('payroll.variabilities.variable')}</option>
            </Select>
          </Field>
          {!['manual', 'tax_brackets', 'tax_table', 'insurance_rule'].includes(v.method) && (
            <Field label={t('payroll.rate')} error={errors.rate} help={v.method === 'formula' ? 'rate' : undefined}>
              {['fixed', 'per_unit', 'per_child'].includes(v.method) ? <MoneyInput value={v.rate} onChange={(x) => set('rate', x)} /> : <NumericInput fraction={4} value={v.rate} onChange={(x) => set('rate', x)} />}
            </Field>
          )}
          {v.method === 'insurance_rule' && (
            <Field label={t('payroll.insuranceShare')} required error={errors.insurance_share}>
              <Select value={v.insurance_share} onChange={(e) => set('insurance_share', e.target.value as typeof v.insurance_share)}>
                <option value="">{t('common.select')}</option>
                {(['employee', 'employer', 'unemployment'] as const).map((k) => <option key={k} value={k}>{t(`payroll.insuranceShares.${k}`)}</option>)}
              </Select>
            </Field>
          )}
          {['daily_base', 'hourly_base', 'per_unit', 'base_percent'].includes(v.method) && (
            <Field label={t('payroll.quantityField')} error={errors.quantity_field}>
              <Select value={v.quantity_field} onChange={(e) => set('quantity_field', e.target.value)}>
                {qtyFields.map((k) => <option key={k} value={k}>{t(`payroll.quantityFields.${k}`)}</option>)}
              </Select>
            </Field>
          )}
          {v.method === 'percent_of_insurable' && <Field label={t('payroll.ceiling')} error={errors.ceiling}><MoneyInput value={v.ceiling} onChange={(x) => set('ceiling', x)} /></Field>}
          {v.method === 'tax_brackets' && <Field label={t('payroll.exemption')} error={errors.exemption}><MoneyInput value={v.exemption} onChange={(x) => set('exemption', x)} /></Field>}
          {v.method === 'formula' && (
            <Field label={t('payroll.formula')} required error={errors.formula} className="span-all"
              help={t('payroll.formulaHelp', { vars: (rules.data?.formula_variables ?? []).join(', ') })}>
              <TextArea rows={2} dir="ltr" value={v.formula} onChange={(e) => set('formula', e.target.value)} placeholder="base_salary * rate / 100" spellCheck={false} />
            </Field>
          )}
          <Field label={t('payroll.seq')}><NumericInput value={v.seq} onChange={(x) => set('seq', x)} /></Field>
          <Field label={t('ui.effectiveFrom')} help={t('ui.optional')} error={errors.effective_from}><DateField value={v.effective_from} onChange={(x) => set('effective_from', x)} /></Field>
          <Field label={t('ui.effectiveTo')} help={t('ui.optional')} error={errors.effective_to}><DateField value={v.effective_to} onChange={(x) => set('effective_to', x)} /></Field>
          <Field label={t('payroll.account')} help={t('payroll.accountHelp')} error={errors.account_id} className="span-2">
            <AccountPicker value={v.account_id} onChange={(x) => set('account_id', x)}
              filter={(a) => (v.kind === 'deduction' && v.category !== 'unpaid_leave' ? a.type === 'asset' || a.type === 'liability' : a.type === 'expense' || a.type === 'cost_of_sales')} />
          </Field>
          {earning && <div style={{ alignSelf: 'end' }}><Checkbox label={t('payroll.taxable')} checked={v.taxable} onChange={(x) => set('taxable', x)} /></div>}
          {earning && <div style={{ alignSelf: 'end' }}><Checkbox label={t('payroll.insurable')} checked={v.insurable} onChange={(x) => set('insurable', x)} /></div>}
          {c && <div style={{ alignSelf: 'end' }}><Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} /></div>}
        </div>
        {v.method === 'tax_brackets' && (
          <div className="stack-sm">
            <div className="form-section-title">{t('payroll.brackets')}</div>
            {errors.brackets && <Alert>{t('validation.required')}</Alert>}
            {brackets.map((b, i) => (
              <div key={i} className="row">
                <span className="muted" style={{ minWidth: 40 }}>{t('payroll.upto')}</span>
                <MoneyInput value={b.upto ?? ''} placeholder={t('payroll.andAbove')} onChange={(x) => setBrackets(brackets.map((y, j) => (j === i ? { ...y, upto: x || null } : y)))} aria-label={t('payroll.upto')} />
                <span style={{ width: 110 }}><PercentageInput value={b.rate} onChange={(x) => setBrackets(brackets.map((y, j) => (j === i ? { ...y, rate: x } : y)))} aria-label={t('payroll.bracketRate')} /></span>
                <IconButton label={t('common.remove')} icon={<Trash2 size={16} />} onClick={() => setBrackets(brackets.filter((_, j) => j !== i))} />
              </div>
            ))}
            <div><Button size="sm" icon={<Plus size={14} />} onClick={() => setBrackets([...brackets, { upto: '', rate: '' }])}>{t('payroll.addBracket')}</Button></div>
          </div>
        )}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
