import { useState } from 'react';
import type { AuditEntry } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Field, Select } from '@/components/ui/Field';
import { PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { useList } from '@/lib/useList';
import { en } from '@/i18n/en';

const entities = ['journal_entry', 'invoice', 'treasury_transaction', 'expense', 'stock_document', 'account', 'party', 'product', 'user', 'role', 'company', 'fiscal_year', 'account_mappings', 'check', 'employee', 'payroll_period', 'salary_component', 'budget', 'budget_rule'];

export function AuditPage() {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const [open, setOpen] = useState<AuditEntry | null>(null);
  const list = useList<AuditEntry, { entity: string; action: string; from: string; to: string }>('audit', '/audit', { entity: '', action: '', from: '', to: '' }, { key: 'created_at', desc: true });
  return (
    <div className="page">
      <PageHeader title={t('settings.auditTitle')} />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('settings.entity')}>
            <Select value={list.filters.entity} onChange={(e) => list.setFilter('entity', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {entities.map((x) => <option key={x} value={x}>{tryT(`entities.${x}`) ?? x}</option>)}
            </Select>
          </Field>
          <Field label={t('settings.action')}>
            <Select value={list.filters.action} onChange={(e) => list.setFilter('action', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {Object.keys(en.settings.auditActions).map((x) => <option key={x} value={x}>{tryT(`settings.auditActions.${x}`)}</option>)}
            </Select>
          </Field>
          <DateRange from={list.filters.from} to={list.filters.to} onChange={(from, to) => list.patch({ from, to })} />
        </div>
        <DataTable rows={list.query.data?.items} loading={list.query.isLoading} rowKey={(a) => a.id} onRowClick={setOpen} sort={list.sort} onSort={list.setSort}
          columns={[
            { key: 'created_at', header: t('common.date'), sortable: true, cell: (a) => <span className="num">{f.dateTime(a.created_at)}</span>, width: 160 },
            { key: 'user', header: t('settings.user'), cell: (a) => a.user_name ?? '—' },
            { key: 'action', header: t('settings.action'), sortable: true, cell: (a) => tryT(`settings.auditActions.${a.action}`) ?? a.action },
            { key: 'entity', header: t('settings.entity'), sortable: true, cell: (a) => <>{tryT(`entities.${a.entity}`) ?? a.entity} {a.entity_id && <span className="num muted">#{f.int(a.entity_id)}</span>}</> },
            { key: 'ip', header: t('settings.ip'), cell: (a) => <span className="num" dir="ltr">{a.ip}</span> },
          ]} />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
      {open && (
        <Dialog wide title={`${tryT(`settings.auditActions.${open.action}`) ?? open.action} — ${tryT(`entities.${open.entity}`) ?? open.entity}`} onClose={() => setOpen(null)}>
          <div className="form-grid" style={{ gridTemplateColumns: '1fr 1fr' }}>
            {(['old_value', 'new_value'] as const).map((k) => (
              <div key={k}>
                <div className="field-label">{k === 'old_value' ? t('settings.before') : t('settings.after')}</div>
                <pre dir="ltr" style={{ background: 'var(--color-surface-sunken)', padding: 12, borderRadius: 6, overflow: 'auto', maxHeight: 420, fontSize: 12 }}>
                  {open[k] ? JSON.stringify(open[k], null, 2) : '—'}
                </pre>
              </div>
            ))}
          </div>
        </Dialog>
      )}
    </div>
  );
}
