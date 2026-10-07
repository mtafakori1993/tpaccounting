import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { api } from '@/api/client';
import type { Party } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button, LinkButton } from '@/components/ui/Button';
import { Alert, Badge, BalanceAmount, KV, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { DateRange, defaultYearRange } from '@/components/DateRange';
import { Attachments } from '@/components/Attachments';
import { LedgerView } from '@/features/reports/LedgerView';
import { PartyDialog } from './PartyDialog';

export function PartyPage() {
  const { id } = useParams();
  const { t } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const { can, me } = useAuth();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [range, setRange] = useState(defaultYearRange(me?.fiscal_year));
  const q = useQuery({ queryKey: ['party', Number(id)], queryFn: () => api.get<Party>(`/parties/${id}`) });
  const p = q.data;
  if (q.error) return <div className="page"><Alert>{err(q.error)}</Alert></div>;
  if (!p) return <LoadingBlock />;

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('parties.title'), to: '/parties' }]}
        title={<>{p.name} {!p.is_active && <Badge>{t('common.inactive')}</Badge>}</>}
        subtitle={<>{t('common.balance')}: <BalanceAmount value={p.balance} /></>}
        actions={
          <>
            {p.is_customer && can('sales.create') && <LinkButton to={`/sales/invoices/new?party=${p.id}`}>{t('parties.newInvoice')}</LinkButton>}
            {p.is_supplier && can('purchases.create') && !p.is_customer && <LinkButton to={`/purchases/invoices/new?party=${p.id}`}>{t('parties.newInvoice')}</LinkButton>}
            {can('treasury.create') && <LinkButton to={`/treasury/transactions?new=receipt&party=${p.id}`}>{t('parties.receive')}</LinkButton>}
            {can('treasury.create') && <LinkButton to={`/treasury/transactions?new=payment&party=${p.id}`}>{t('parties.pay')}</LinkButton>}
            {can('parties.edit') && <Button variant="accent" onClick={() => setEditing(true)}>{t('common.edit')}</Button>}
          </>
        }
      />
      <div className="side-layout">
        <Panel title={t('parties.statement')} bodyless>
          <div className="toolbar">
            <DateRange from={range.from} to={range.to} onChange={(from, to) => setRange({ from, to })} />
          </div>
          <LedgerView params={{ party_id: p.id, from: range.from, to: range.to }} exportName={`statement-${p.code}`} showParty={false} title={`${t('parties.statement')} — ${p.name}`} />
        </Panel>
        <div className="stack">
          <Panel title={t('common.details')}>
            <KV
              items={[
                { label: t('common.code'), value: <span className="num">{f.digits(p.code)}</span> },
                { label: t('parties.kind'), value: p.kind === 'person' ? t('parties.person') : t('parties.company') },
                { label: t('parties.nationalId'), value: f.digits(p.national_id) },
                { label: t('parties.economicCode'), value: f.digits(p.economic_code) },
                { label: t('parties.mobile'), value: <span dir="ltr">{f.digits(p.mobile)}</span> },
                { label: t('parties.phone'), value: <span dir="ltr">{f.digits(p.phone)}</span> },
                { label: t('parties.email'), value: p.email },
                { label: t('parties.address'), value: p.address },
                { label: t('parties.bankName'), value: p.bank_name, hidden: !p.bank_name },
                { label: t('parties.iban'), value: <span dir="ltr">{p.iban}</span>, hidden: !p.iban },
                { label: t('common.notes'), value: p.notes, hidden: !p.notes },
              ]}
            />
          </Panel>
          <Attachments entity="party" id={p.id} canEdit={can('parties.edit')} />
        </div>
      </div>
      {editing && (
        <PartyDialog
          party={p}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            void qc.invalidateQueries({ queryKey: ['party'] });
            void qc.invalidateQueries({ queryKey: ['parties'] });
          }}
        />
      )}
    </div>
  );
}
