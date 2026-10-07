import { useRef, useState } from 'react';
import { Download, FileSpreadsheet, FileText, FileType2, Printer } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useAuth } from '@/auth/AuthProvider';
import { useClickOutside } from '@/lib/hooks';
import { tableDocument, usePdf, type PdfDocument } from '@/lib/pdf';
import { exportCsv, exportXlsx, type ExportColumn } from './export';
import { Popover } from './ui/Popover';

/**
 * Export (Excel/CSV) and PDF/print actions for a table or report. PDF and print
 * always go through the server's letterhead engine; `pdf` lets a page supply a
 * richer document than the plain table (e.g. grouped financial statements).
 */
export function ExportMenu<T>({ name, columns, rows, permission = 'reports.export', title, subtitle, pdf }: {
  name: string;
  columns: ExportColumn<T>[];
  rows: T[] | undefined;
  permission?: string;
  title?: string;
  subtitle?: string;
  pdf?: () => PdfDocument;
}) {
  const { t, dir } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const printer = usePdf();
  const [open, setOpen] = useState(false);
  const pop = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const ref = useClickOutside<HTMLDivElement>(() => setOpen(false), open, pop);
  const allowed = can(permission);
  const docTitle = () => title ?? document.querySelector('.page-title')?.textContent?.trim() ?? name;
  const build = (): PdfDocument => pdf?.() ?? tableDocument(docTitle(), columns, rows ?? [], f, { subtitle });
  return (
    <div className="menu" ref={ref}>
      <button ref={btn} type="button" className="btn btn-secondary" aria-haspopup="menu" aria-expanded={open} aria-busy={printer.pending || undefined} onClick={() => setOpen((o) => !o)}>
        <Download size={16} />
        {t('common.export')}
      </button>
      {open && (
        <Popover anchor={btn} open popoverRef={pop} align="end" role="menu" className="menu-panel">
          {allowed && (
            <>
              <button type="button" role="menuitem" className="menu-item" disabled={!rows?.length} onClick={() => { setOpen(false); void exportXlsx(name, columns, rows ?? [], dir === 'rtl'); }}>
                <FileSpreadsheet size={16} /> {t('common.exportXlsx')}
              </button>
              <button type="button" role="menuitem" className="menu-item" disabled={!rows?.length} onClick={() => { setOpen(false); exportCsv(name, columns, rows ?? []); }}>
                <FileText size={16} /> {t('common.exportCsv')}
              </button>
              <button type="button" role="menuitem" className="menu-item" disabled={!rows?.length} onClick={() => { setOpen(false); void printer.view(build); }}>
                <FileType2 size={16} /> {t('common.exportPdf')}
              </button>
            </>
          )}
          <button type="button" role="menuitem" className="menu-item" disabled={!rows?.length} onClick={() => { setOpen(false); void printer.print(build); }}>
            <Printer size={16} /> {t('common.printDoc')}
          </button>
        </Popover>
      )}
    </div>
  );
}
