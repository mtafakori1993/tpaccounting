import { useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Download, FileUp } from 'lucide-react';
import { ApiError, api } from '@/api/client';
import type { ImportIssue, ImportLog, ImportResult, PayrollPeriod } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { Alert } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { useToast } from '@/components/ui/Toast';
import { exportCsv } from '@/components/export';
import { downloadFile } from '@/lib/download';

function csrf() {
  const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : '';
}

async function upload(periodId: number, file: File, commit: boolean): Promise<ImportResult> {
  const body = new FormData();
  body.append('file', file);
  if (commit) body.append('commit', 'true');
  let res: Response;
  try {
    res = await fetch(`/api/v1/payroll/periods/${periodId}/attendance/import`, { method: 'POST', body, credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() } });
  } catch {
    throw new ApiError(0, { code: 'network', message: 'network error' });
  }
  const data = await res.json().catch(() => undefined);
  if (!res.ok) throw new ApiError(res.status, data?.error ?? { code: 'internal', message: res.statusText });
  return data as ImportResult;
}

/** Upload → parse → preview → validate → show errors → confirm → store. Nothing is stored unless every row is valid. */
export function AttendanceImportDialog({ period, onClose, onImported }: { period: PayrollPeriod; onClose: () => void; onImported: () => void }) {
  const { t, tryT, lang } = useI18n();
  const f = useFormat();
  const msg = useErrorMessage();
  const toast = useToast();
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [drag, setDrag] = useState(false);
  const [res, setRes] = useState<ImportResult | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const history = useQuery({ queryKey: ['payroll-period', period.id, 'imports'], queryFn: () => api.get<ImportLog[]>(`/payroll/periods/${period.id}/attendance/imports`) });
  const issue = (i: ImportIssue) => tryT(`payroll.importIssues.${i.code}`, { value: i.value ?? '' }) ?? i.code;
  const choose = async (fl: File | null) => {
    setFile(fl);
    setRes(null);
    setError(null);
    if (!fl) return;
    setPending(true);
    try {
      setRes(await upload(period.id, fl, false));
    } catch (e) {
      setError(msg(e));
    } finally {
      setPending(false);
    }
  };
  const commit = async () => {
    if (!file) return;
    setPending(true);
    setError(null);
    try {
      const r = await upload(period.id, file, true);
      setRes(r);
      toast.success(t('payroll.importDone', { rows: f.count(r.summary.imported_rows), employees: f.count(r.summary.employees) }));
      onImported();
      onClose();
    } catch (e) {
      setError(msg(e));
    } finally {
      setPending(false);
    }
  };
  const errorReport = () => res && exportCsv(`attendance-errors-${period.year}-${period.month}`, [
    { header: t('payroll.row'), value: (i: ImportIssue) => i.row || '' },
    { header: t('payroll.column'), value: (i: ImportIssue) => i.column ?? '' },
    { header: t('payroll.value'), value: (i: ImportIssue) => i.value ?? '' },
    { header: t('payroll.problem'), value: issue },
  ], [...res.errors, ...res.warnings]);
  const s = res?.summary;
  return (
    <Dialog size="xwide" title={t('payroll.importTitle')} description={t('payroll.importHelp')} onClose={onClose}
      footer={<>
        <Button icon={<Download size={16} />} onClick={() => void downloadFile('/payroll/attendance/template', { lang }, 'attendance-template.xlsx').catch((e) => setError(msg(e)))}>{t('ui.downloadTemplate')}</Button>
        <span className="grow" />
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <Button variant="accent" pending={pending} disabled={!res?.can_import} onClick={commit}>{t('payroll.confirmImport', { count: f.count(s?.valid_rows ?? 0) })}</Button>
      </>}>
      <div className="stack">
        <label className={`dropzone ${drag ? 'drag' : ''}`}
          onDragOver={(e) => { e.preventDefault(); setDrag(true); }} onDragLeave={() => setDrag(false)}
          onDrop={(e) => { e.preventDefault(); setDrag(false); void choose(e.dataTransfer.files[0] ?? null); }}>
          <FileUp size={26} aria-hidden />
          <strong>{file ? file.name : t('ui.chooseFile')}</strong>
          <span className="muted">{t('ui.dropHint')}</span>
          <input ref={input} type="file" accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" className="visually-hidden"
            onChange={(e) => void choose(e.target.files?.[0] ?? null)} />
        </label>
        <p className="muted" style={{ fontSize: 'var(--font-size-sm)' }}>{t('payroll.importColumns')}</p>
        {pending && !res && <div className="loading-block"><span className="spinner" /> {t('payroll.validate')}…</div>}
        {error && <Alert>{error}</Alert>}
        {s && (
          <>
            <div className="summary-tiles" aria-live="polite">
              {(['total_rows', 'valid_rows', 'invalid_rows', 'warnings', 'employees'] as const).map((k) => (
                <div key={k} className={`summary-tile ${k === 'invalid_rows' && s[k] > 0 ? 'bad' : k === 'warnings' && s[k] > 0 ? 'warn' : k === 'valid_rows' && s.invalid_rows === 0 ? 'good' : ''}`}>
                  <div className="label">{t(`payroll.importSummary.${k}`)}</div>
                  <div className="value num">{f.count(s[k])}</div>
                </div>
              ))}
            </div>
            {res!.errors.length > 0 && (
              <div className="stack-sm">
                <div className="row-between">
                  <strong className="danger-text">{t('payroll.importErrorsTitle')}</strong>
                  <Button size="sm" icon={<Download size={14} />} onClick={errorReport}>{t('payroll.downloadErrors')}</Button>
                </div>
                <DataTable rows={res!.errors} rowKey={(_, i) => i} dense maxHeight="240px" caption={t('payroll.importErrorsTitle')}
                  columns={[
                    { key: 'row', header: t('payroll.row'), cell: (i) => <span className="num">{i.row ? f.count(i.row) : '—'}</span>, width: 70 },
                    { key: 'col', header: t('payroll.column'), cell: (i) => i.column ?? '' },
                    { key: 'val', header: t('payroll.value'), cell: (i) => <span dir="auto">{i.value ?? ''}</span> },
                    { key: 'p', header: t('payroll.problem'), cell: issue },
                  ]} />
              </div>
            )}
            {res!.warnings.length > 0 && (
              <details>
                <summary className="row"><strong>{t('payroll.importWarningsTitle')}</strong> <span className="tab-count warning">{f.count(res!.warnings.length)}</span></summary>
                <ul className="stack-sm" style={{ marginTop: 8 }}>
                  {res!.warnings.map((w, i) => <li key={i}>{w.row ? `${t('payroll.row')} ${f.count(w.row)}: ` : ''}{w.employee ? `${w.employee} — ` : ''}{issue(w)}</li>)}
                </ul>
              </details>
            )}
            {res!.employees.length > 0 && (
              <DataTable rows={res!.employees} rowKey={(e) => e.employee_id} dense maxHeight="260px" caption={t('payroll.preview')}
                columns={[
                  { key: 'e', header: t('payroll.employee'), cell: (e) => <>{e.employee_name} <span className="muted num">{f.digits(e.employee_code)}</span></> },
                  { key: 'd', header: t('payroll.days'), align: 'end', cell: (e) => <span className="num">{f.count(e.days)}</span> },
                  { key: 'h', header: t('payroll.workingHours'), align: 'end', cell: (e) => <span className="num">{f.qty(e.working_hours)}</span> },
                  { key: 'o', header: t('payroll.overtimeHours'), align: 'end', cell: (e) => <span className="num">{f.qty(e.overtime_hours)}</span> },
                  { key: 'a', header: t('payroll.absenceDays'), align: 'end', cell: (e) => <span className="num">{f.qty(e.absence_days)}</span> },
                  { key: 'l', header: t('payroll.paidLeaveDays'), align: 'end', cell: (e) => <span className="num">{f.qty(e.paid_leave_days)}</span> },
                  { key: 'u', header: t('payroll.unpaidLeaveDays'), align: 'end', cell: (e) => <span className="num">{f.qty(e.unpaid_leave_days)}</span> },
                  { key: 'm', header: t('payroll.missionDays'), align: 'end', cell: (e) => <span className="num">{f.qty(e.mission_days)}</span> },
                ]} />
            )}
          </>
        )}
        {history.data && history.data.length > 0 && (
          <details>
            <summary><strong>{t('payroll.importHistory')}</strong></summary>
            <DataTable rows={history.data} rowKey={(h) => h.id} dense
              columns={[
                { key: 'd', header: t('common.date'), cell: (h) => <span className="num">{f.dateTime(h.created_at)}</span> },
                { key: 'f', header: t('ui.chooseFile'), cell: (h) => h.file_name },
                { key: 'r', header: t('payroll.importSummary.imported_rows'), align: 'end', cell: (h) => <span className="num">{f.count(h.imported_rows)}</span> },
                { key: 'e', header: t('payroll.importSummary.employees'), align: 'end', cell: (h) => <span className="num">{f.count(h.employee_count)}</span> },
                { key: 'u', header: t('settings.user'), cell: (h) => h.user_name ?? '' },
              ]} />
          </details>
        )}
      </div>
    </Dialog>
  );
}
