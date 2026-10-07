// Table export to CSV and Excel, done in the browser from the same data the
// user sees, so headers are already translated and filters already applied.

export type ExportValue = string | number | null | undefined;
export interface ExportColumn<T> {
  header: string;
  value: (row: T) => ExportValue;
  /** numbers are written as numeric cells in Excel */
  numeric?: boolean;
}

function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

const toNum = (v: ExportValue) => (v === null || v === undefined || v === '' ? null : Number(v));

export function exportCsv<T>(name: string, columns: ExportColumn<T>[], rows: T[]) {
  const esc = (v: ExportValue) => {
    const s = v === null || v === undefined ? '' : String(v);
    return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  const lines = [columns.map((c) => esc(c.header)).join(','), ...rows.map((r) => columns.map((c) => esc(c.value(r))).join(','))];
  // BOM so Excel opens UTF-8 (Persian) text correctly.
  download(new Blob(['﻿' + lines.join('\r\n')], { type: 'text/csv;charset=utf-8' }), `${name}.csv`);
}

export async function exportXlsx<T>(name: string, columns: ExportColumn<T>[], rows: T[], rtl: boolean) {
  const { default: writeXlsxFile } = await import('write-excel-file/browser');
  const header = columns.map((c) => ({ value: c.header, fontWeight: 'bold' as const }));
  const body = rows.map((r) =>
    columns.map((c) => {
      const v = c.value(r);
      if (c.numeric) {
        const n = toNum(v);
        return n === null || Number.isNaN(n) ? null : { value: n, type: Number, format: '#,##0.##' };
      }
      return v === null || v === undefined ? null : { value: String(v), type: String };
    }),
  );
  const blob = await writeXlsxFile([header, ...body], {
    rightToLeft: rtl,
    columns: columns.map((c) => ({ width: c.numeric ? 16 : 28 })),
    stickyRowsCount: 1,
  }).toBlob();
  download(blob, `${name}.xlsx`);
}
