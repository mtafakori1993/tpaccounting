import { useEffect, useRef, useState, type KeyboardEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { CheckCircle2, Plus, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { JournalEntry } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { AmountInput, Field, Input } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { AccountPicker, CostCenterSelect, PartyPicker } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { add, cmp, isZero, sub, toBig } from '@/lib/decimal';
import { todayIso } from '@/lib/dates';
import { focusNext } from '@/lib/gridNav';

interface Line {
  key: number;
  account_id: number | null;
  party_id: number | null;
  cost_center_id: number | null;
  description: string;
  debit: string;
  credit: string;
}

let lineSeq = 0;
const newLine = (): Line => ({ key: ++lineSeq, account_id: null, party_id: null, cost_center_id: null, description: '', debit: '', credit: '' });

function fromEntry(e: JournalEntry): Line[] {
  return (e.lines ?? []).map((l) => ({
    key: ++lineSeq,
    account_id: l.account_id,
    party_id: l.party_id,
    cost_center_id: l.cost_center_id,
    description: l.description,
    debit: isZero(l.debit) ? '' : l.debit,
    credit: isZero(l.credit) ? '' : l.credit,
  }));
}

export function JournalFormPage() {
  const { id } = useParams();
  const [search] = useSearchParams();
  const copyId = search.get('copy');
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const gridRef = useRef<HTMLDivElement>(null);

  const sourceId = id ?? copyId;
  const existing = useQuery({ queryKey: ['journal', Number(sourceId)], queryFn: () => api.get<JournalEntry>(`/journal-entries/${sourceId}`), enabled: !!sourceId });

  const [header, setHeader] = useState({ number: '', date: sessionStorage.getItem('tp.lastJournalDate') ?? todayIso(), reference: '', description: '' });
  const [lines, setLines] = useState<Line[]>(() => [newLine(), newLine()]);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [loaded, setLoaded] = useState(!sourceId);
  const { run, pending, error } = useSubmit(setFieldErrors);

  useEffect(() => {
    const e = existing.data;
    if (!e || loaded) return;
    setLines(fromEntry(e));
    setHeader({
      number: id ? String(e.number) : '',
      date: id ? e.date : header.date,
      reference: e.reference,
      description: e.description,
    });
    setLoaded(true);
  }, [existing.data, loaded, id, header.date]);

  const totalDebit = add(...lines.map((l) => l.debit || '0'));
  const totalCredit = add(...lines.map((l) => l.credit || '0'));
  const diff = sub(totalDebit, totalCredit);
  const balanced = isZero(diff) && !isZero(totalDebit);

  const update = (key: number, patch: Partial<Line>) => setLines((ls) => ls.map((l) => (l.key === key ? { ...l, ...patch } : l)));

  const setAmount = (l: Line, side: 'debit' | 'credit', raw: string) => {
    if (raw === '=') {
      // Fill with whatever balances the entry on this side.
      const others = lines.filter((x) => x.key !== l.key);
      const d = sub(add(...others.map((x) => x.debit || '0')), add(...others.map((x) => x.credit || '0')));
      const need = side === 'debit' ? toBig(d).times(-1) : toBig(d);
      update(l.key, need.gt(0) ? { [side]: need.toString(), [side === 'debit' ? 'credit' : 'debit']: '' } : { [side]: '' });
      return;
    }
    update(l.key, { [side]: raw, ...(raw && !isZero(raw) ? { [side === 'debit' ? 'credit' : 'debit']: '' } : {}) });
  };

  const addLine = (focus = true) => {
    const nl = newLine();
    setLines((ls) => [...ls, nl]);
    if (focus) setTimeout(() => gridRef.current?.querySelector<HTMLInputElement>(`[data-line="${nl.key}"] input`)?.focus(), 30);
  };

  const save = async (post: boolean) => {
    const body = {
      number: header.number ? Number(header.number) : 0,
      date: header.date,
      reference: header.reference,
      description: header.description,
      post,
      lines: lines
        .filter((l) => l.account_id || l.debit || l.credit)
        .map((l) => ({ account_id: l.account_id ?? 0, party_id: l.party_id, cost_center_id: l.cost_center_id, description: l.description, debit: l.debit || '0', credit: l.credit || '0' })),
    };
    const saved = await run(
      () => (id ? api.put<JournalEntry>(`/journal-entries/${id}`, body) : api.post<JournalEntry>('/journal-entries', body)),
      post ? t('common.postedToast') : t('common.saved'),
    );
    if (saved) {
      sessionStorage.setItem('tp.lastJournalDate', header.date);
      void qc.invalidateQueries({ queryKey: ['journal'] });
      void qc.invalidateQueries({ queryKey: ['dashboard'] });
      navigate(`/journal/${saved.id}`);
    }
  };

  const onGridKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const target = e.target as HTMLElement;
    if (e.key === 'Enter' && !e.ctrlKey && !e.metaKey && !e.shiftKey && target.tagName !== 'BUTTON') {
      e.preventDefault();
      if (!focusNext(target, gridRef.current!)) addLine();
    }
  };

  const onPageKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      void save(can('journal.post'));
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      void save(false);
    } else if (e.altKey && e.key.toLowerCase() === 'n') {
      e.preventDefault();
      addLine();
    }
  };

  const lineErr = (i: number, field: string) => fieldErrors[`lines.${i}.${field}`];

  if (sourceId && !loaded) return <LoadingBlock />;
  if (id && existing.data && existing.data.status !== 'draft') {
    return <div className="page"><Alert>{t('errors.not_draft')}</Alert></div>;
  }

  const drMax = toBig(totalDebit);
  const crMax = toBig(totalCredit);
  const total = drMax.plus(crMax);
  const pct = (v: typeof drMax) => (total.gt(0) ? Number(v.div(total).times(100).toFixed(1)) : 50);

  return (
    <div className="page" onKeyDown={onPageKey}>
      <PageHeader
        crumbs={[{ label: t('journal.title'), to: '/journal' }]}
        title={id ? t('journal.editEntry') : t('journal.newEntry')}
        subtitle={<span className="muted" style={{ fontSize: 12 }}>{t('journal.shortcuts')}</span>}
        actions={
          <>
            <Button onClick={() => navigate(-1)}>{t('common.cancel')}</Button>
            <Button onClick={() => save(false)} pending={pending}>{t('common.saveDraft')}</Button>
            {can('journal.post') && <Button variant="accent" onClick={() => save(true)} pending={pending} disabled={!balanced}>{t('common.saveAndPost')}</Button>}
          </>
        }
      />
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        <Panel>
          <div className="form-grid">
            <Field label={t('common.number')} error={fieldErrors.number} help={!id ? t('journal.autoNumber') : undefined}>
              <Input value={header.number} onChange={(e) => setHeader({ ...header, number: e.target.value.replace(/\D/g, '') })} className="num" inputMode="numeric" />
            </Field>
            <Field label={t('common.date')} required error={fieldErrors.date}>
              <DateField value={header.date} onChange={(v) => setHeader({ ...header, date: v })} />
            </Field>
            <Field label={t('common.reference')} error={fieldErrors.reference}>
              <Input value={header.reference} onChange={(e) => setHeader({ ...header, reference: e.target.value })} />
            </Field>
            <Field label={t('common.description')} className="span-all" error={fieldErrors.description}>
              <Input value={header.description} onChange={(e) => setHeader({ ...header, description: e.target.value })} />
            </Field>
          </div>
        </Panel>
        <section className="panel entry-grid">
          <div className="panel-head">
            <h2 className="panel-title">{t('journal.lines')}</h2>
            <Button size="sm" icon={<Plus size={14} />} onClick={() => addLine()}>{t('journal.addLine')} <kbd>Alt+N</kbd></Button>
          </div>
          {fieldErrors.lines && <div style={{ padding: '8px 20px' }}><Alert>{t('journal.emptyLines')}</Alert></div>}
          <div className="table-wrap" ref={gridRef} onKeyDown={onGridKey}>
            <table className="table">
              <thead>
                <tr>
                  <th className="align-center">#</th>
                  <th style={{ minWidth: 240 }}>{t('journal.account')}</th>
                  <th style={{ minWidth: 180 }}>{t('journal.party')}</th>
                  <th style={{ minWidth: 130 }}>{t('journal.costCenter')}</th>
                  <th style={{ minWidth: 180 }}>{t('common.description')}</th>
                  <th className="align-end" style={{ width: 150 }}>{t('common.debit')}</th>
                  <th className="align-end" style={{ width: 150 }}>{t('common.credit')}</th>
                  <th style={{ width: 40 }} />
                </tr>
              </thead>
              <tbody>
                {lines.map((l, i) => (
                  <tr key={l.key} data-line={l.key}>
                    <td className="line-no num">{f.int(i + 1)}</td>
                    <td data-invalid={!!lineErr(i, 'account_id')} title={lineErr(i, 'account_id') ? t(`validation.${lineErr(i, 'account_id')}` as never) : undefined}>
                      <AccountPicker value={l.account_id} onChange={(id) => update(l.key, { account_id: id })} aria-invalid={!!lineErr(i, 'account_id')} />
                    </td>
                    <td><PartyPicker value={l.party_id} onChange={(id) => update(l.key, { party_id: id })} /></td>
                    <td><CostCenterSelect value={l.cost_center_id} onChange={(id) => update(l.key, { cost_center_id: id })} /></td>
                    <td><Input value={l.description} onChange={(e) => update(l.key, { description: e.target.value })} placeholder={header.description} /></td>
                    <td data-invalid={!!lineErr(i, 'debit')}><AmountInput value={l.debit} onChange={(v) => setAmount(l, 'debit', v)} /></td>
                    <td><AmountInput value={l.credit} onChange={(v) => setAmount(l, 'credit', v)} /></td>
                    <td>
                      <IconButton
                        size="sm"
                        tabIndex={-1}
                        label={t('journal.removeLine')}
                        icon={<Trash2 size={14} />}
                        disabled={lines.length <= 1}
                        onClick={() => setLines((ls) => ls.filter((x) => x.key !== l.key))}
                      />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className={`balance-bar ${balanced ? 'ok' : ''}`} aria-live="polite">
            <div className="sum">
              <span className="label">{t('common.debit')}</span>
              <span className="value num">{f.money(totalDebit)}</span>
            </div>
            <div className="balance-meter" aria-hidden>
              <span className="dr" style={{ width: `${pct(drMax)}%` }} />
              <span className="cr" style={{ width: `${pct(crMax)}%` }} />
            </div>
            <div className="sum">
              <span className="label">{t('common.credit')}</span>
              <span className="value num">{f.money(totalCredit)}</span>
            </div>
            {balanced ? (
              <span className="balance-state ok"><CheckCircle2 size={16} /> {t('journal.balanced')}</span>
            ) : (
              <span className="balance-state off num">
                {t('journal.unbalanced', { amount: f.money(toBig(diff).abs().toString()) })} {cmp(diff, 0) > 0 ? t('common.dr') : cmp(diff, 0) < 0 ? t('common.cr') : ''}
              </span>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}
