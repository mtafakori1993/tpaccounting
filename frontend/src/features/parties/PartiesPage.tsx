import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Plus } from 'lucide-react';
import type { Party } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Field, Input } from '@/components/ui/Field';
import { BalanceAmount, Badge, PageHeader, Panel, Tabs } from '@/components/ui/Misc';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { useList } from '@/lib/useList';
import { PartyDialog } from './PartyDialog';

export function PartiesPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const list = useList<Party, { q: string; role: string }>('parties', '/parties', { q: '', role: '' }, { key: 'name', desc: false });
  const rows = list.query.data?.items;
  const roles = (p: Party) => [p.is_customer && t('parties.customer'), p.is_supplier && t('parties.supplier'), p.is_other && t('parties.other')].filter(Boolean).join('، ');

  return (
    <div className="page">
      <PageHeader
        title={t('parties.title')}
        actions={
          <>
            <ExportMenu
              name="parties"
              permission="parties.export"
              rows={rows}
              columns={[
                { header: t('common.code'), value: (p) => p.code },
                { header: t('common.name'), value: (p) => p.name },
                { header: t('parties.roles'), value: roles },
                { header: t('parties.nationalId'), value: (p) => p.national_id },
                { header: t('parties.economicCode'), value: (p) => p.economic_code },
                { header: t('parties.mobile'), value: (p) => p.mobile },
                { header: t('parties.phone'), value: (p) => p.phone },
                { header: t('common.balance'), value: (p) => p.balance, numeric: true },
              ]}
            />
            {can('parties.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('parties.newParty')}</Button>}
          </>
        }
      />
      <Panel bodyless>
        <Tabs
          value={list.filters.role}
          onChange={(r) => list.setFilter('role', r)}
          tabs={[
            { key: '', label: t('common.all') },
            { key: 'customer', label: t('parties.customer') },
            { key: 'supplier', label: t('parties.supplier') },
            { key: 'other', label: t('parties.other') },
          ]}
        />
        <div className="toolbar">
          <Field label={t('common.search')} className="search">
            <Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} />
          </Field>
        </div>
        <DataTable
          rows={rows}
          loading={list.query.isLoading}
          rowKey={(p) => p.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={(p) => navigate(`/parties/${p.id}`)}
          rowClassName={(p) => (p.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'code', header: t('common.code'), sortable: true, cell: (p) => <span className="num">{f.digits(p.code)}</span>, width: 100 },
            { key: 'name', header: t('common.name'), sortable: true, cell: (p) => <>{p.name} {!p.is_active && <Badge>{t('common.inactive')}</Badge>}</> },
            { key: 'role', header: t('parties.roles'), cell: roles },
            { key: 'mobile', header: t('parties.mobile'), cell: (p) => <span className="num">{f.digits(p.mobile || p.phone)}</span> },
            { key: 'balance', header: t('common.balance'), sortable: true, align: 'end', cell: (p) => <BalanceAmount value={p.balance} /> },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
      {creating && <PartyDialog onClose={() => setCreating(false)} onSaved={(p) => { setCreating(false); navigate(`/parties/${p.id}`); }} />}
    </div>
  );
}
