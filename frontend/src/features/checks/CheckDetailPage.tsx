import { useState } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowRightLeft, FileText, Pencil, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { AuditEntry, Check, JournalEntry, Page } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/Dialog';
import { KV, Money, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { EmptyState, ErrorState, SkeletonPage } from '@/components/ui/Feedback';
import { Stepper, Timeline, type Step, type TimelineItem } from '@/components/ui/Steps';
import { Attachments } from '@/components/Attachments';
import { useSubmit } from '@/lib/useSubmit';
import { CheckDialog, DueCell, TransitionDialog, checkPermission, checkQueryKeys, negativeActions, useBankName } from './common';

const happyPath = { received: ['received', 'in_custody', 'deposited', 'cleared'], issued: ['draft', 'issued', 'delivered', 'cleared'] } as const;
const terminalBad = ['returned', 'cancelled', 'lost', 'replaced'];
const toneOf = (status: string): TimelineItem['tone'] =>
  status === 'cleared' ? 'success' : terminalBad.includes(status) ? 'danger' : status === 'transferred' ? 'info' : status === 'deposited' ? 'warning' : 'neutral';

/** /checks/:id — status, amount, party, bank, due date, accounting impact, invoice, lifecycle and audit history. */
export function CheckDetailPage() {
  const { id } = useParams();
  const { t, tryT, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [action, setAction] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const del = useSubmit();
  const bankName = useBankName();
  const q = useQuery({ queryKey: ['check', Number(id)], queryFn: () => api.get<Check>(`/checks/${id}`) });
  const c = q.data;
  const audit = useQuery({
    queryKey: ['check', Number(id), 'audit'],
    queryFn: () => api.get<Page<AuditEntry>>('/audit', { entity: 'check', entity_id: id, page_size: 50 }),
    enabled: can('audit.view'),
  });
  const entryIds = c ? Array.from(new Set(c.transitions?.flatMap((x) => [x.journal_entry_id, ...x.reversal_entry_ids]).filter((x): x is number => !!x))) : [];
  const entries = useQueries({
    queries: entryIds.map((eid) => ({ queryKey: ['journal-entry', eid], queryFn: () => api.get<JournalEntry>(`/journal-entries/${eid}`), enabled: can('journal.view') })),
  });
  const refresh = () => {
    for (const k of checkQueryKeys) void qc.invalidateQueries({ queryKey: k });
  };

  if (q.error) return <div className="page"><Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel></div>;
  if (!c) return <div className="page"><SkeletonPage kpis={0} /></div>;

  const received = c.check_type === 'received';
  const path = happyPath[c.check_type];
  const reached = new Set([...(c.transitions ?? []).map((x) => x.to_status)]);
  const terminal = !path.includes(c.status as never) && c.status !== 'transferred' ? c.status : null;
  const steps: Step[] = path.map((st) => {
    const done = reached.has(st) && st !== c.status;
    const tr = c.transitions?.find((x) => x.to_status === st);
    let state: Step['state'] = done ? 'done' : st === c.status ? (st === 'cleared' ? 'done' : 'current') : 'upcoming';
    if (terminal && !reached.has(st)) state = 'skipped';
    return { key: st, label: t(`checks.${received ? 'received_path' : 'issued_path'}.${st}` as 'checks.received_path.received'), meta: tr ? f.date(tr.date) : undefined, state };
  });
  if (terminal || c.status === 'transferred') {
    steps.push({ key: c.status, label: t(`checks.statuses.${c.status as 'draft'}`), meta: c.status_date ? f.date(c.status_date) : undefined, state: terminal ? 'failed' : 'done' });
  }
  const actions = c.allowed_actions.filter((a) => can(checkPermission(a)));
  const primary = actions.filter((a) => !negativeActions.includes(a));
  const negative = actions.filter((a) => negativeActions.includes(a));
  const timeline: TimelineItem[] = [...(c.transitions ?? [])].reverse().map((x) => ({
    key: x.id,
    tone: toneOf(x.to_status),
    title: <>{tryT(`checks.actions.${x.action}`) ?? x.action} <StatusBadge status={x.to_status} /></>,
    meta: <>{f.date(x.date)} · {x.user_name ?? '—'} · {f.dateTime(x.created_at)}</>,
    children: (x.treasury_account_name || x.party_name || x.reason || x.journal_entry_id || x.reversal_entry_ids.length > 0) && (
      <div className="stack-sm">
        {(x.treasury_account_name || x.party_name) && <span className="muted">{[x.treasury_account_name, x.party_name].filter(Boolean).join(' — ')}</span>}
        {x.reason && <span>«{x.reason}»</span>}
        <span className="inline-list">
          {x.journal_entry_id && <Link to={`/journal/${x.journal_entry_id}`} className="badge plain">{t('checks.entry')} {f.digits(String(x.journal_number ?? x.journal_entry_id))}</Link>}
          {x.reversal_entry_ids.map((r) => <Link key={r} to={`/journal/${r}`} className="badge plain badge-danger">{t('checks.reversal')} #{f.digits(String(r))}</Link>)}
        </span>
      </div>
    ),
  }));
  const typeLabel = t(`checks.types.${c.check_type}`);
  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('checks.listTitle'), to: '/checks' }]}
        title={<span className="row" style={{ gap: 12 }}>{t('checks.detailTitle', { type: typeLabel, number: f.digits(c.check_number) })} <StatusBadge status={c.status} /></span>}
        subtitle={`${received ? t('checks.drawer') : t('checks.payee')}: ${c.party_name}`}
        actions={<>
          {(c.status === 'draft' || c.live) && can('checks.edit') && <Button icon={<Pencil size={16} />} onClick={() => setEditing(true)}>{t('common.edit')}</Button>}
          {c.status === 'draft' && can('checks.delete') && <Button variant="quiet" icon={<Trash2 size={16} />} onClick={() => setDeleting(true)}>{t('common.delete')}</Button>}
        </>}
      />

      <Panel bodyless className="section-gap">
        <div className="hero">
          <div className="hero-amount">
            <span className="label">{t('checks.amount')}</span>
            <span className="value num">{f.money(c.amount)}<span className="cur">{f.currencyLabel}</span></span>
          </div>
          <dl className="hero-facts">
            <div><dt>{t('checks.dueDate')}</dt><dd><DueCell c={c} /></dd></div>
            <div><dt>{received ? t('checks.drawer') : t('checks.payee')}</dt><dd><Link to={`/parties/${c.party_id}`}>{c.party_name}</Link></dd></div>
            <div><dt>{t('checks.bank')}</dt><dd>{[bankName(c), c.branch].filter(Boolean).join(' — ') || '—'}</dd></div>
            <div><dt>{t('checks.checkNumber')}</dt><dd className="num" dir="ltr">{f.digits(c.check_number)}</dd></div>
          </dl>
        </div>
        <Stepper steps={steps} label={t('checks.lifecycle')} />
        <div className="next-step">
          <div className="ns-text">
            <div className="ns-title">{actions.length ? t('checks.availableActions') : t('checks.currentStatus')}</div>
            <div className="ns-body">{actions.length ? t('checks.lifecycleHelp') : terminal && c.status_date ? t('checks.terminal', { status: t(`checks.statuses.${c.status as 'draft'}`), date: f.date(c.status_date) }) : t('checks.noActions')}</div>
          </div>
          <div className="row">
            {primary.map((a, i) => <Button key={a} variant={i === 0 ? 'accent' : 'secondary'} onClick={() => setAction(a)}>{tryT(`checks.actions.${a}`) ?? a}</Button>)}
            {negative.map((a) => <Button key={a} variant="negative" onClick={() => setAction(a)}>{tryT(`checks.actions.${a}`) ?? a}</Button>)}
          </div>
        </div>
      </Panel>

      <div className="grid-main">
        <div className="stack">
          <Panel title={t('checks.summary')}>
            <KV items={[
              { label: t('checks.registerNumber'), value: <span className="num">{f.digits(String(c.number))}</span> },
              { label: t('checks.type'), value: typeLabel },
              { label: t('checks.sayadiId'), value: c.sayadi_id && <span className="num" dir="ltr">{f.digits(c.sayadi_id)}</span> },
              { label: t('checks.serialNumber'), value: c.serial_number && <span dir="ltr">{f.digits(c.serial_number)}</span> },
              { label: t('checks.accountNumber'), value: c.account_number && <span className="num" dir="ltr">{f.digits(c.account_number)}</span> },
              { label: t('checks.accountHolder'), value: c.account_holder },
              { label: t('checks.issueDate'), value: <span className="num">{f.date(c.issue_date)}</span> },
              { label: t('checks.receivedDate'), value: c.received_date && <span className="num">{f.date(c.received_date)}</span>, hidden: !received },
              { label: received ? t('checks.depositAccount') : t('checks.treasuryAccount'), value: c.treasury_account_name },
              { label: t('checks.holder'), value: c.holder_party_name, hidden: !c.holder_party_id },
              { label: t('checks.project'), value: c.project_name },
              { label: t('common.description'), value: c.description },
              { label: t('checks.notes'), value: c.notes },
              { label: t('common.createdAt'), value: <>{f.dateTime(c.created_at)} {c.created_by_name && <span className="muted">— {c.created_by_name}</span>}</> },
            ]} />
          </Panel>

          <Panel title={<>{t('ui.accountingImpact')} <span className="panel-sub">— {t('checks.impactHelp')}</span></>} bodyless>
            {entryIds.length === 0 ? (
              <EmptyState compact title={t('ui.noEntries')} icon={<FileText size={20} />} />
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <caption className="visually-hidden">{t('ui.accountingImpact')}</caption>
                  <thead>
                    <tr>
                      <th scope="col">{t('checks.entry')}</th>
                      <th scope="col">{t('common.date')}</th>
                      <th scope="col">{t('journal.account')}</th>
                      <th scope="col" className="align-end">{t('common.debit')}</th>
                      <th scope="col" className="align-end">{t('common.credit')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {entries.map((e, i) => {
                      const je = e.data;
                      if (!je) {
                        return (
                          <tr key={entryIds[i]}>
                            <td><Link to={`/journal/${entryIds[i]}`}>#{f.digits(String(entryIds[i]))}</Link></td>
                            <td colSpan={4} className="muted">{e.isLoading ? t('common.loading') : '—'}</td>
                          </tr>
                        );
                      }
                      return je.lines?.map((l, j) => (
                        <tr key={`${je.id}-${l.id}`} className={je.reversal_of_id ? 'muted-row' : undefined}>
                          {j === 0 && (
                            <td rowSpan={je.lines?.length} style={{ verticalAlign: 'top' }}>
                              <Link to={`/journal/${je.id}`} className="num">{f.digits(String(je.number))}</Link>
                              {je.reversal_of_id && <span className="badge badge-danger" style={{ marginInlineStart: 6 }}>{t('checks.reversal')}</span>}
                              {je.reversed_by_id && <div className="muted" style={{ fontSize: 'var(--font-size-xs)' }}>{t('ui.reversedBy', { number: f.digits(String(je.reversed_by_number ?? '')) })}</div>}
                            </td>
                          )}
                          {j === 0 && <td rowSpan={je.lines?.length} className="num" style={{ verticalAlign: 'top' }}>{f.date(je.date)}</td>}
                          <td>
                            <span className="muted num">{f.digits(l.account_code ?? '')}</span> {lang === 'en' && l.account_name_en ? l.account_name_en : l.account_name}
                            {l.party_name && <span className="muted"> — {l.party_name}</span>}
                          </td>
                          <td className="align-end"><Money value={l.debit} blankZero /></td>
                          <td className="align-end"><Money value={l.credit} blankZero /></td>
                        </tr>
                      ));
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>

          <Attachments entity="check" id={c.id} canEdit={can('checks.edit')} />
        </div>

        <div className="stack">
          <Panel title={t('checks.relatedInvoice')}>
            {c.invoice_id ? (
              <div className="stack-sm">
                <Link to={`/${received ? 'sales' : 'purchases'}/invoices/${c.invoice_id}`} className="row">
                  <FileText size={16} /> {t(`invoices.singular.${received ? 'sales' : 'purchase'}_invoice`)} {f.digits(String(c.invoice_number ?? ''))}
                </Link>
                <div className="dl-grid">
                  <dt>{t('checks.settled')}</dt><dd><Money value={c.allocated} /></dd>
                </div>
              </div>
            ) : (
              <div className="stack-sm">
                <span className="muted">{t('checks.noInvoice')}</span>
                {Number(c.allocated) > 0 && <div className="dl-grid"><dt>{t('checks.settled')}</dt><dd><Money value={c.allocated} /></dd></div>}
              </div>
            )}
          </Panel>
          <Panel title={t('checks.history')}>
            {timeline.length ? <Timeline items={timeline} label={t('checks.history')} /> : <span className="muted">—</span>}
          </Panel>
          {can('audit.view') && (
            <Panel title={t('ui.auditTrail')}>
              {audit.error ? <ErrorState compact error={audit.error} onRetry={() => void audit.refetch()} /> : !audit.data ? <span className="muted">{t('common.loading')}</span> : audit.data.items.length === 0 ? (
                <span className="muted">{t('ui.noAudit')}</span>
              ) : (
                <Timeline label={t('ui.auditTrail')} items={audit.data.items.map((a) => ({
                  key: a.id,
                  title: tryT(`settings.auditActions.${a.action}`) ?? a.action,
                  meta: <>{f.dateTime(a.created_at)} · {a.user_name ?? '—'}{a.ip ? ` · ${a.ip}` : ''}</>,
                  tone: a.action === 'create' ? 'success' : negativeActions.includes(a.action) ? 'danger' : 'neutral',
                }))} />
              )}
            </Panel>
          )}
          <Link to="/checks" className="row muted"><ArrowRightLeft size={14} /> {t('checks.backToList')}</Link>
        </div>
      </div>

      {action && <TransitionDialog check={c} action={action} onClose={() => setAction(null)} onDone={() => { setAction(null); refresh(); }} />}
      {editing && <CheckDialog check={c} onClose={() => setEditing(false)} onSaved={() => { setEditing(false); refresh(); }} />}
      {deleting && (
        <ConfirmDialog title={t('common.delete')} body={t('common.confirmDelete')} confirmLabel={t('common.yesDelete')} negative pending={del.pending} error={del.error}
          onClose={() => setDeleting(false)}
          onConfirm={() => del.run(() => api.del(`/checks/${c.id}`).then(() => { refresh(); navigate('/checks'); }), t('common.deleted'))} />
      )}
    </div>
  );
}
