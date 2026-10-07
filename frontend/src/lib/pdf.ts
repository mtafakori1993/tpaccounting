// The only way the frontend produces a PDF: describe the document as data and
// let the server's PDF engine render it inside the official letterhead.
import { useState } from 'react';
import { ApiError } from '@/api/client';
import { useErrorMessage } from '@/i18n/errors';
import { useI18n } from '@/i18n/I18nProvider';
import type { Formatter } from '@/i18n/format';
import { useToast } from '@/components/ui/Toast';
import type { ExportColumn } from '@/components/export';
import { isValidNumber } from './decimal';

export type Align = 'start' | 'end' | 'center';
export interface PdfColumn { label: string; align: Align }
export interface PdfRow { cells: string[]; style?: '' | 'group' | 'subtotal' | 'total' | 'muted' }
export interface PdfTable { title?: string; columns: PdfColumn[]; rows: PdfRow[]; footer?: string[] }
export interface PdfKV { label: string; value: string }

export interface PdfDocument {
  title: string;
  subtitle?: string;
  number?: string;
  date?: string;
  attachment?: string;
  lang?: 'fa' | 'en';
  orientation?: 'portrait' | 'landscape';
  purpose?: 'export' | 'print';
  meta?: PdfKV[];
  parties?: { title: string; name: string; lines: PdfKV[] }[];
  paragraphs?: string[];
  tables?: PdfTable[];
  totals?: { label: string; value: string; strong?: boolean }[];
  notes?: string;
  signatures?: string[];
}

function csrf() {
  const m = document.cookie.match(/(?:^|;\s*)tp_csrf=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : '';
}

/** POSTs to a PDF endpoint and returns the PDF blob. */
export async function fetchPdf(path: string, body: unknown): Promise<Blob> {
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, { code: 'network', message: 'network error' });
  }
  if (!res.ok) {
    const data = await res.json().catch(() => undefined);
    throw new ApiError(res.status, data?.error ?? { code: 'internal', message: res.statusText });
  }
  return res.blob();
}

/** Prints a PDF blob through a hidden frame (the browser's print dialog). */
export function printBlob(blob: Blob) {
  const url = URL.createObjectURL(blob);
  const frame = document.createElement('iframe');
  frame.style.cssText = 'position:fixed;width:0;height:0;border:0;right:0;bottom:0';
  frame.src = url;
  frame.onload = () => {
    setTimeout(() => {
      frame.contentWindow?.focus();
      frame.contentWindow?.print();
    }, 300);
    setTimeout(() => {
      frame.remove();
      URL.revokeObjectURL(url);
    }, 120_000);
  };
  document.body.appendChild(frame);
}

/** Opens a PDF blob in a window opened synchronously by the click (avoids popup blockers). */
function showBlob(win: Window | null, blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  if (win && !win.closed) {
    win.location.href = url;
  } else {
    const a = document.createElement('a');
    a.href = url;
    a.download = `${name}.pdf`;
    a.click();
  }
  setTimeout(() => URL.revokeObjectURL(url), 120_000);
}

/** Hook used by every "PDF" and "Print" action in the application. */
export function usePdf() {
  const [pending, setPending] = useState(false);
  const toast = useToast();
  const msg = useErrorMessage();
  const { lang } = useI18n();
  const run = async (build: () => PdfDocument | Promise<PdfDocument>, mode: 'view' | 'print') => {
    const win = mode === 'view' ? window.open('', '_blank') : null;
    setPending(true);
    try {
      const doc = await build();
      const blob = await fetchPdf('/pdf', { lang, ...doc, purpose: mode === 'print' ? 'print' : 'export' });
      if (mode === 'print') printBlob(blob);
      else showBlob(win, blob, doc.title || 'document');
    } catch (e) {
      win?.close();
      toast.error(msg(e));
    } finally {
      setPending(false);
    }
  };
  return {
    pending,
    view: (build: () => PdfDocument | Promise<PdfDocument>) => run(build, 'view'),
    print: (build: () => PdfDocument | Promise<PdfDocument>) => run(build, 'print'),
  };
}

/** Formats a raw export value for print: amounts grouped, digits localised. */
export function printValue(v: unknown, numeric: boolean | undefined, f: Formatter) {
  if (v === null || v === undefined) return '';
  const s = String(v);
  if (numeric && isValidNumber(s)) {
    // Amounts use the company's decimals; quantities and rates keep their own precision.
    const fraction = s.includes('.') ? s.split('.')[1].length : 0;
    return fraction > f.decimals ? f.number(s, Math.min(fraction, 4)) : f.money(s);
  }
  return f.digits(s);
}

/** Builds a table document from the same column definitions used for Excel/CSV export. */
export function tableDocument<T>(title: string, columns: ExportColumn<T>[], rows: T[], f: Formatter, extra: Partial<PdfDocument> = {}): PdfDocument {
  return {
    title,
    orientation: columns.length > 7 ? 'landscape' : 'portrait',
    ...extra,
    tables: [
      {
        columns: columns.map((c) => ({ label: c.header, align: c.numeric ? 'end' : 'start' })),
        rows: rows.map((r) => ({ cells: columns.map((c) => printValue(c.value(r), c.numeric, f)) })),
      },
      ...(extra.tables ?? []),
    ],
  };
}

