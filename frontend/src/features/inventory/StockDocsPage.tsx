import { useNavigate } from 'react-router-dom';
import type { StockDocument } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Field, Input, Select } from '@/components/ui/Field';
import { ActionMenu, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { useList } from '@/lib/useList';

export const stockKinds = ['receipt', 'issue', 'transfer', 'adjustment'] as const;

export function StockDocsPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const list = useList<StockDocument, { q: string; kind: string; status: string }>('stock-docs', '/inventory/documents', { q: '', kind: '', status: '' }, { key: 'date', desc: true });
  return (
    <div className="page">
      <PageHeader
        title={t('inventory.documentsTitle')}
        actions={can('inventory.create') && (
          <ActionMenu label={t('common.new')} items={stockKinds.map((k) => ({ label: t('inventory.newDocument', { kind: t(`inventory.kinds.${k}`) }), to: `/inventory/documents/new?kind=${k}` }))} />
        )}
      />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('common.type')}>
            <Select value={list.filters.kind} onChange={(e) => list.setFilter('kind', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {stockKinds.map((k) => <option key={k} value={k}>{t(`inventory.kinds.${k}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('common.status')}>
            <Select value={list.filters.status} onChange={(e) => list.setFilter('status', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {['draft', 'posted', 'cancelled'].map((s) => <option key={s} value={s}>{t(`status.${s as 'draft'}`)}</option>)}
            </Select>
          </Field>
        </div>
        <DataTable
          rows={list.query.data?.items}
          loading={list.query.isLoading}
          rowKey={(d) => d.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={(d) => navigate(`/inventory/documents/${d.id}`)}
          columns={[
            { key: 'kind', header: t('common.type'), sortable: true, cell: (d) => t(`inventory.kinds.${d.kind}`) },
            { key: 'number', header: t('common.number'), sortable: true, cell: (d) => <span className="num">{f.int(d.number)}</span> },
            { key: 'date', header: t('common.date'), sortable: true, cell: (d) => <span className="num">{f.date(d.date)}</span> },
            { key: 'wh', header: t('inventory.warehouse'), cell: (d) => (d.to_warehouse_name ? `${d.warehouse_name} ${t('treasury.toAccount')} ${d.to_warehouse_name}` : d.warehouse_name) },
            { key: 'desc', header: t('common.description'), cell: (d) => d.description },
            { key: 'status', header: t('common.status'), cell: (d) => <StatusBadge status={d.status} /> },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
    </div>
  );
}
