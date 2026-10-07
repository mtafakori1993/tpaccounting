import { useRef, useState } from 'react';
import { Download, FileSpreadsheet, FileText, FileType2, Printer } from 'lucide-react';
import type { Query } from '@/api/client';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useErrorMessage } from '@/i18n/errors';
import { useClickOutside } from '@/lib/hooks';
import { downloadFile } from '@/lib/download';
import { useToast } from './ui/Toast';
import { Popover } from './ui/Popover';

/**
 * Export of a report generated on the server from the full filtered dataset
 * (never only the visible page): Excel, CSV, PDF with the letterhead, and print.
 */
export function ServerExport({ path, query, name, permission, formats = ['xlsx', 'csv', 'pdf'], disabled }: {
  path: string;
  query: Query;
  name: string;
  permission: string;
  formats?: ('xlsx' | 'csv' | 'pdf')[];
  disabled?: boolean;
}) {
  const { t, lang } = useI18n();
  const { can } = useAuth();
  const toast = useToast();
  const msg = useErrorMessage();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const ref = useClickOutside<HTMLDivElement>(() => setOpen(false), open, pop);
  if (!can(permission)) return null;
  const run = async (format: string, mode: 'save' | 'view' | 'print') => {
    setOpen(false);
    setPending(true);
    try {
      await downloadFile(path, { ...query, format, lang }, `${name}.${format}`, mode);
    } catch (e) {
      toast.error(`${t('ui.exportFailed')} ${msg(e)}`);
    } finally {
      setPending(false);
    }
  };
  return (
    <div className="menu" ref={ref}>
      <button ref={btn} type="button" className="btn btn-secondary" aria-haspopup="menu" aria-expanded={open} aria-busy={pending || undefined}
        disabled={disabled || pending} onClick={() => setOpen((o) => !o)} title={t('ui.exportAll')}>
        {pending ? <span className="spinner" style={{ width: 14, height: 14 }} /> : <Download size={16} />}
        {pending ? t('ui.exporting') : t('common.export')}
      </button>
      {open && (
        <Popover anchor={btn} open popoverRef={pop} align="end" role="menu" className="menu-panel" onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}>
          {formats.includes('xlsx') && (
            <button type="button" role="menuitem" className="menu-item" onClick={() => run('xlsx', 'save')}><FileSpreadsheet size={16} /> {t('common.exportXlsx')}</button>
          )}
          {formats.includes('csv') && (
            <button type="button" role="menuitem" className="menu-item" onClick={() => run('csv', 'save')}><FileText size={16} /> {t('common.exportCsv')}</button>
          )}
          {formats.includes('pdf') && (
            <>
              <button type="button" role="menuitem" className="menu-item" onClick={() => run('pdf', 'view')}><FileType2 size={16} /> {t('common.exportPdf')}</button>
              <button type="button" role="menuitem" className="menu-item" onClick={() => run('pdf', 'print')}><Printer size={16} /> {t('common.printDoc')}</button>
            </>
          )}
          <div className="menu-sep" />
          <div className="option-empty" style={{ maxWidth: 260, whiteSpace: 'normal' }}>{t('ui.exportAll')}</div>
        </Popover>
      )}
    </div>
  );
}
