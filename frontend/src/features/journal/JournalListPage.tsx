import { useNavigate } from 'react-router-dom';
import { Plus } from 'lucide-react';
import type { JournalEntry } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { LinkButton } from '@/components/ui/Button';
import { Field, Input, Select } from '@/components/ui/Field';
import { Amount, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import { useList } from '@/lib/useList';
import { en } from '@/i18n/en';

const sources = Object.keys(en.journal.sources);

export function JournalListPage() {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const list = useList<JournalEntry, { q: string; status: string; source_type: string; from: string; to: string }>(
    'journal-list', '/journal-entries', { q: '', status: '', source_type: '', from: '', to: '' }, { key: 'date', desc: true },
  );
  const rows = list.query.data?.items;
  const src = (e: JournalEntry) => tryT(`journal.sources.${e.source_type}`) ?? e.source_type;
  return (
    <div className="page">
      <PageHeader
        title={t('journal.title')}
        actions={
          <>
            <ExportMenu
              name="journal-entries"
              permission="journal.export"
              rows={rows}
              columns={[
                { header: t('common.number'), value: (e) => e.number, numeric: true },
                { header: t('common.date'), value: (e) => f.date(e.date) },
                { header: t('journal.source'), value: src },
                { header: t('common.description'), value: (e) => e.description },
                { header: t('common.reference'), value: (e) => e.reference },
                { header: t('common.amount'), value: (e) => e.total_debit, numeric: true },
                { header: t('common.status'), value: (e) => t(`status.${e.status}`) },
              ]}
            />
            {can('journal.create') && <LinkButton to="/journal/new" variant="accent" icon={<Plus size={16} />}>{t('journal.newEntry')}</LinkButton>}
          </>
        }
      />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('common.status')}>
            <Select value={list.filters.status} onChange={(e) => list.setFilter('status', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {['draft', 'posted', 'cancelled'].map((s) => <option key={s} value={s}>{t(`status.${s as 'draft'}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('journal.source')}>
            <Select value={list.filters.source_type} onChange={(e) => list.setFilter('source_type', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {sources.map((s) => <option key={s} value={s}>{tryT(`journal.sources.${s}`)}</option>)}
            </Select>
          </Field>
          <DateRange from={list.filters.from} to={list.filters.to} onChange={(from, to) => list.patch({ from, to })} />
        </div>
        <DataTable
          rows={rows}
          loading={list.query.isLoading}
          rowKey={(e) => e.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={(e) => navigate(`/journal/${e.id}`)}
          columns={[
            { key: 'number', header: t('common.number'), sortable: true, cell: (e) => <span className="num">{f.int(e.number)}</span>, width: 80 },
            { key: 'date', header: t('common.date'), sortable: true, cell: (e) => <span className="num">{f.date(e.date)}</span>, width: 110 },
            { key: 'source', header: t('journal.source'), cell: src, width: 150 },
            { key: 'desc', header: t('common.description'), cell: (e) => e.description || <span className="muted">{e.reference}</span> },
            { key: 'total', header: t('common.amount'), sortable: true, align: 'end', cell: (e) => <Amount value={e.total_debit} /> },
            { key: 'status', header: t('common.status'), sortable: true, cell: (e) => <StatusBadge status={e.status} />, width: 110 },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
    </div>
  );
}
