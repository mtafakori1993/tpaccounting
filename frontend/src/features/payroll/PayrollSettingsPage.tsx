import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { Bracket, InsuranceRule, PayrollRules, PayrollSettings, TaxTable } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, NumericInput, PercentageInput, Select, TextArea } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Badge, Money, PageHeader, Panel } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { EmptyState, ErrorState, SkeletonPage } from '@/components/ui/Feedback';
import { useSubmit } from '@/lib/useSubmit';
import { todayIso } from '@/lib/dates';
import { componentName, useSalaryComponents } from './common';

const inForce = (r: { effective_from: string; effective_to: string | null; is_active: boolean }) => {
  const d = todayIso();
  return r.is_active && r.effective_from <= d && (!r.effective_to || r.effective_to >= d);
};

/** /payroll/settings — statutory rules (with effective dates) and the month-length method. */
export function PayrollSettingsPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['payroll-rules'], queryFn: () => api.get<PayrollRules>('/payroll/rules') });
  const [tax, setTax] = useState<TaxTable | 'new' | null>(null);
  const [ins, setIns] = useState<InsuranceRule | 'new' | null>(null);
  const [deleting, setDeleting] = useState<{ kind: 'tax-tables' | 'insurance-rules'; id: number } | null>(null);
  const del = useSubmit();
  const editable = can('payroll.settings');
  const reload = () => void qc.invalidateQueries({ queryKey: ['payroll-rules'] });
  if (q.error) return <div className="page"><Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel></div>;
  const d = q.data;
  const status = (r: { effective_from: string; effective_to: string | null; is_active: boolean }) =>
    !r.is_active ? <Badge>{t('common.inactive')}</Badge> : inForce(r) ? <Badge tone="success">{t('ui.inForce')}</Badge> : <Badge tone="info">{t('ui.notInForce')}</Badge>;
  const period = (r: { effective_from: string; effective_to: string | null }) => <span className="num">{f.date(r.effective_from)} – {r.effective_to ? f.date(r.effective_to) : t('ui.openEnded')}</span>;
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('payroll.overviewTitle'), to: '/payroll' }]} title={t('payroll.settingsTitle')} subtitle={t('payroll.settingsSubtitle')} />
      <div className="section-gap"><Alert kind="warning">{t('payroll.rulesNotice')}</Alert></div>
      {!d ? <SkeletonPage kpis={0} /> : (
        <div className="stack">
          <DayBasisPanel settings={d.settings} editable={editable} onSaved={reload} />
          <Panel bodyless title={t('payroll.taxTables')} actions={editable && <Button size="sm" icon={<Plus size={15} />} onClick={() => setTax('new')}>{t('payroll.newTaxTable')}</Button>}>
            <DataTable rows={d.tax_tables} rowKey={(r) => r.id} onRowClick={editable ? (r) => setTax(r) : undefined} caption={t('payroll.taxTables')}
              empty={<EmptyState compact title={t('payroll.warnings.no_tax_table')} action={editable && <Button variant="accent" onClick={() => setTax('new')}>{t('payroll.newTaxTable')}</Button>} />}
              columns={[
                { key: 'name', header: t('ui.name'), cell: (r) => <strong>{r.name}</strong> },
                { key: 'eff', header: t('ui.effectiveFrom'), cell: period },
                { key: 'mode', header: t('payroll.mode'), cell: (r) => t(`payroll.modes.${r.mode}`) },
                { key: 'ex', header: t('payroll.exemptionLabel'), align: 'end', cell: (r) => <Money value={r.exemption} blankZero /> },
                { key: 'br', header: t('payroll.brackets'), cell: (r) => <span className="num">{r.brackets.map((b) => f.percent(b.rate)).join(' / ')}</span> },
                { key: 'st', header: t('common.status'), cell: status },
                { key: 'x', header: '', align: 'end', cell: (r) => editable && <IconButton size="sm" label={t('common.delete')} icon={<Trash2 size={15} />} onClick={(e) => { e.stopPropagation(); setDeleting({ kind: 'tax-tables', id: r.id }); }} /> },
              ]} />
          </Panel>
          <Panel bodyless title={t('payroll.insuranceRules')} actions={editable && <Button size="sm" icon={<Plus size={15} />} onClick={() => setIns('new')}>{t('payroll.newInsuranceRule')}</Button>}>
            <DataTable rows={d.insurance_rules} rowKey={(r) => r.id} onRowClick={editable ? (r) => setIns(r) : undefined} caption={t('payroll.insuranceRules')}
              empty={<EmptyState compact title={t('payroll.warnings.no_insurance_rule')} action={editable && <Button variant="accent" onClick={() => setIns('new')}>{t('payroll.newInsuranceRule')}</Button>} />}
              columns={[
                { key: 'name', header: t('ui.name'), cell: (r) => <strong>{r.name}</strong> },
                { key: 'eff', header: t('ui.effectiveFrom'), cell: period },
                { key: 'e', header: t('payroll.employeeRate'), align: 'end', cell: (r) => <span className="num">{f.percent(r.employee_rate)}</span> },
                { key: 'r', header: t('payroll.employerRate'), align: 'end', cell: (r) => <span className="num">{f.percent(r.employer_rate)}</span> },
                { key: 'u', header: t('payroll.unemploymentRate'), align: 'end', cell: (r) => <span className="num">{f.percent(r.unemployment_rate)}</span> },
                { key: 'c', header: t('payroll.ceilingLabel'), align: 'end', cell: (r) => <Money value={r.ceiling} blankZero /> },
                { key: 'st', header: t('common.status'), cell: status },
                { key: 'x', header: '', align: 'end', cell: (r) => editable && <IconButton size="sm" label={t('common.delete')} icon={<Trash2 size={15} />} onClick={(e) => { e.stopPropagation(); setDeleting({ kind: 'insurance-rules', id: r.id }); }} /> },
              ]} />
          </Panel>
        </div>
      )}
      {tax && <TaxTableDialog table={tax === 'new' ? undefined : tax} onClose={() => setTax(null)} onSaved={() => { setTax(null); reload(); }} />}
      {ins && <InsuranceDialog rule={ins === 'new' ? undefined : ins} onClose={() => setIns(null)} onSaved={() => { setIns(null); reload(); }} />}
      {deleting && (
        <ConfirmDialog title={t('common.delete')} body={t('ui.deleteConfirm')} confirmLabel={t('common.yesDelete')} negative pending={del.pending} error={del.error}
          onClose={() => setDeleting(null)} onConfirm={() => del.run(() => api.del(`/payroll/${deleting.kind}/${deleting.id}`).then(() => { setDeleting(null); reload(); }), t('common.deleted'))} />
      )}
    </div>
  );
}

function DayBasisPanel({ settings, editable, onSaved }: { settings: PayrollSettings; editable: boolean; onSaved: () => void }) {
  const { t } = useI18n();
  const [v, setV] = useState({ period_days: settings.period_days, divisor: String(settings.divisor ?? '30'), monthly_hours: String(settings.monthly_hours) });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  useEffect(() => setV({ period_days: settings.period_days, divisor: String(settings.divisor ?? '30'), monthly_hours: String(settings.monthly_hours) }), [settings]);
  const dirty = v.period_days !== settings.period_days || v.divisor !== String(settings.divisor) || v.monthly_hours !== String(settings.monthly_hours);
  return (
    <Panel title={t('payroll.dayBasisTitle')} actions={editable && <Button size="sm" variant="accent" disabled={!dirty} pending={pending} onClick={() => run(() => api.put('/payroll/settings', v).then(onSaved), t('common.saved'))}>{t('common.save')}</Button>}>
      <div className="stack">
        <p className="muted">{t('payroll.dayBasisHelp')}</p>
        {error && <Alert>{error}</Alert>}
        <div className="choice-cards" role="radiogroup" aria-label={t('payroll.dayBasisTitle')}>
          {(['30', 'actual', 'fixed'] as const).map((k) => (
            <label key={k} className={`choice-card ${v.period_days === k ? 'selected' : ''}`}>
              <input type="radio" name="period_days" value={k} checked={v.period_days === k} disabled={!editable} onChange={() => setV({ ...v, period_days: k })} />
              <span>{t(`payroll.dayBasisOptions.${k}`)}</span>
            </label>
          ))}
        </div>
        <div className="form-grid">
          {v.period_days === 'fixed' && <Field label={t('payroll.divisor')} required error={errors.divisor}><NumericInput fraction={2} value={v.divisor} disabled={!editable} onChange={(x) => setV({ ...v, divisor: x })} /></Field>}
          <Field label={t('payroll.monthlyHours')} error={errors.monthly_hours}><NumericInput fraction={2} value={v.monthly_hours} disabled={!editable} onChange={(x) => setV({ ...v, monthly_hours: x })} /></Field>
        </div>
      </div>
    </Panel>
  );
}

function TaxTableDialog({ table, onClose, onSaved }: { table?: TaxTable; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const [v, setV] = useState({
    name: table?.name ?? '', effective_from: table?.effective_from ?? todayIso(), effective_to: table?.effective_to ?? '', mode: table?.mode ?? ('monthly' as 'monthly' | 'annual'),
    exemption: table?.exemption ?? '0', notes: table?.notes ?? '', is_active: table?.is_active ?? true,
  });
  const [brackets, setBrackets] = useState<Bracket[]>(table?.brackets?.length ? table.brackets : [{ upto: '', rate: '0' }, { upto: null, rate: '' }]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const body = { ...v, effective_to: v.effective_to || null, exemption: v.exemption || '0', brackets: brackets.map((b, i) => ({ upto: i === brackets.length - 1 ? null : b.upto || null, rate: b.rate || '0' })) };
    const ok = await run(() => (table ? api.put(`/payroll/tax-tables/${table.id}`, body) : api.post('/payroll/tax-tables', body)).then(() => true), t('common.saved'));
    if (ok) onSaved();
  };
  return (
    <Dialog size="wide" title={table ? t('payroll.editTaxTable') : t('payroll.newTaxTable')} description={t('payroll.modeHelp')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('ui.name')} required error={errors.name} className="span-2"><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} autoFocus /></Field>
          <Field label={t('payroll.mode')} error={errors.mode}>
            <Select value={v.mode} onChange={(e) => setV({ ...v, mode: e.target.value as 'monthly' | 'annual' })}>
              <option value="monthly">{t('payroll.modes.monthly')}</option>
              <option value="annual">{t('payroll.modes.annual')}</option>
            </Select>
          </Field>
          <Field label={t('ui.effectiveFrom')} required error={errors.effective_from}><DateField value={v.effective_from} onChange={(x) => setV({ ...v, effective_from: x })} /></Field>
          <Field label={t('ui.effectiveTo')} help={t('ui.openEnded')} error={errors.effective_to}><DateField value={v.effective_to} onChange={(x) => setV({ ...v, effective_to: x })} /></Field>
          <Field label={t('payroll.exemptionLabel')} error={errors.exemption}><MoneyInput value={v.exemption} onChange={(x) => setV({ ...v, exemption: x })} /></Field>
        </div>
        <fieldset className="form-section">
          <legend>{t('payroll.brackets')}</legend>
          {errors.brackets && <Alert>{t('validation.required')}</Alert>}
          <div className="table-wrap">
            <table className="table dense">
              <thead><tr><th scope="col">#</th><th scope="col">{t('payroll.upto')}</th><th scope="col">{t('payroll.bracketRate')}</th><th /></tr></thead>
              <tbody>
                {brackets.map((b, i) => {
                  const last = i === brackets.length - 1;
                  return (
                    <tr key={i}>
                      <td className="num muted">{i + 1}</td>
                      <td>{last ? <span className="muted">{t('payroll.andAbove')}</span> : (
                        <MoneyInput value={b.upto ?? ''} onChange={(x) => setBrackets(brackets.map((y, j) => (j === i ? { ...y, upto: x } : y)))} aria-label={t('payroll.upto')} />
                      )}{errors[`brackets.${i}.upto`] && <span className="field-error">{t('validation.invalid')}</span>}</td>
                      <td style={{ width: 160 }}><PercentageInput value={b.rate} onChange={(x) => setBrackets(brackets.map((y, j) => (j === i ? { ...y, rate: x } : y)))} aria-label={t('payroll.bracketRate')} /></td>
                      <td style={{ width: 48 }}>{brackets.length > 1 && <IconButton size="sm" label={t('common.remove')} icon={<Trash2 size={15} />} onClick={() => setBrackets(brackets.filter((_, j) => j !== i))} />}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div style={{ marginTop: 8 }}><Button size="sm" icon={<Plus size={14} />} onClick={() => setBrackets([...brackets.slice(0, -1), { upto: '', rate: '' }, brackets[brackets.length - 1]])}>{t('payroll.addBracket')}</Button></div>
        </fieldset>
        <Field label={t('ui.notes')}><TextArea rows={2} value={v.notes} onChange={(e) => setV({ ...v, notes: e.target.value })} /></Field>
        {table && <Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => setV({ ...v, is_active: x })} />}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function InsuranceDialog({ rule, onClose, onSaved }: { rule?: InsuranceRule; onClose: () => void; onSaved: () => void }) {
  const { t, lang } = useI18n();
  const comps = useSalaryComponents();
  const [v, setV] = useState({
    name: rule?.name ?? '', effective_from: rule?.effective_from ?? todayIso(), effective_to: rule?.effective_to ?? '', employee_rate: rule?.employee_rate ?? '',
    employer_rate: rule?.employer_rate ?? '', unemployment_rate: rule?.unemployment_rate ?? '', ceiling: rule?.ceiling ?? '0', eligible_codes: rule?.eligible_codes ?? [],
    notes: rule?.notes ?? '', is_active: rule?.is_active ?? true,
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const body = { ...v, effective_to: v.effective_to || null, employee_rate: v.employee_rate || '0', employer_rate: v.employer_rate || '0',
      unemployment_rate: v.unemployment_rate || '0', ceiling: v.ceiling || '0' };
    const ok = await run(() => (rule ? api.put(`/payroll/insurance-rules/${rule.id}`, body) : api.post('/payroll/insurance-rules', body)).then(() => true), t('common.saved'));
    if (ok) onSaved();
  };
  const earnings = comps.data?.filter((c) => c.kind === 'earning') ?? [];
  const toggle = (code: string, on: boolean) => setV({ ...v, eligible_codes: on ? [...v.eligible_codes, code] : v.eligible_codes.filter((x) => x !== code) });
  return (
    <Dialog size="wide" title={rule ? t('payroll.editInsuranceRule') : t('payroll.newInsuranceRule')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('ui.name')} required error={errors.name} className="span-2"><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} autoFocus /></Field>
          <Field label={t('ui.effectiveFrom')} required error={errors.effective_from}><DateField value={v.effective_from} onChange={(x) => setV({ ...v, effective_from: x })} /></Field>
          <Field label={t('ui.effectiveTo')} help={t('ui.openEnded')} error={errors.effective_to}><DateField value={v.effective_to} onChange={(x) => setV({ ...v, effective_to: x })} /></Field>
          <Field label={t('payroll.employeeRate')} required error={errors.employee_rate}><PercentageInput value={v.employee_rate} onChange={(x) => setV({ ...v, employee_rate: x })} /></Field>
          <Field label={t('payroll.employerRate')} required error={errors.employer_rate}><PercentageInput value={v.employer_rate} onChange={(x) => setV({ ...v, employer_rate: x })} /></Field>
          <Field label={t('payroll.unemploymentRate')} error={errors.unemployment_rate}><PercentageInput value={v.unemployment_rate} onChange={(x) => setV({ ...v, unemployment_rate: x })} /></Field>
          <Field label={t('payroll.ceilingLabel')} error={errors.ceiling}><MoneyInput value={v.ceiling} onChange={(x) => setV({ ...v, ceiling: x })} /></Field>
        </div>
        <fieldset className="form-section">
          <legend>{t('payroll.eligible')}</legend>
          <p className="muted" style={{ marginBottom: 8 }}>{t('payroll.eligibleHelp')}</p>
          {errors.eligible_codes && <Alert>{t('validation.invalid')}</Alert>}
          <div className="check-grid">
            {earnings.map((c) => <Checkbox key={c.code} aiKey={`payroll.eligible.${c.code}`} label={`${componentName(c, lang)}${c.insurable ? ' ✓' : ''}`} checked={v.eligible_codes.includes(c.code)} onChange={(x) => toggle(c.code, x)} />)}
          </div>
        </fieldset>
        <Field label={t('ui.notes')}><TextArea rows={2} value={v.notes} onChange={(e) => setV({ ...v, notes: e.target.value })} /></Field>
        {rule && <Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => setV({ ...v, is_active: x })} />}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
