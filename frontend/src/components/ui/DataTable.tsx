import { useRef, useState, type ReactNode } from 'react';
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, Columns3 } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useClickOutside, usePersistentState } from '@/lib/hooks';
import { EmptyState, ErrorState, SkeletonTable } from './Feedback';
import { Popover } from './Popover';

export interface Column<T> {
  key: string;
  header: ReactNode;
  cell: (row: T, index: number) => ReactNode;
  align?: 'start' | 'end' | 'center';
  sortable?: boolean;
  width?: number | string;
  footer?: ReactNode;
  className?: string;
  /** can be hidden from the column menu (when the table has `columnsKey`) */
  optional?: boolean;
  /** hidden until the user turns it on */
  defaultHidden?: boolean;
}

export interface Sort {
  key: string;
  desc: boolean;
}

interface Props<T> {
  columns: Column<T>[];
  rows: T[] | undefined;
  rowKey: (row: T, index: number) => string | number;
  loading?: boolean;
  /** a failed load: shown as an actionable error state */
  error?: unknown;
  onRetry?: () => void;
  onRowClick?: (row: T) => void;
  sort?: Sort;
  onSort?: (s: Sort) => void;
  empty?: ReactNode;
  showFooter?: boolean;
  rowClassName?: (row: T) => string | undefined;
  maxHeight?: string;
  /** accessible name of the table */
  caption?: string;
  /** enables the column-visibility menu, remembered per user under this key */
  columnsKey?: string;
  dense?: boolean;
}

/**
 * The single table used across the application: sticky header, server-side
 * sorting, skeleton while loading, explicit empty and error states, totals
 * footer and optional column visibility. Wide tables scroll inside their
 * container instead of stretching the page.
 */
export function DataTable<T>({ columns, rows, rowKey, loading, error, onRetry, onRowClick, sort, onSort, empty, showFooter, rowClassName, maxHeight, caption, columnsKey, dense }: Props<T>) {
  const { t } = useI18n();
  const [hidden, setHidden] = usePersistentState<Record<string, boolean>>(`columns.${columnsKey ?? 'none'}`,
    Object.fromEntries(columns.filter((c) => c.defaultHidden).map((c) => [c.key, true])));
  const visible = columnsKey ? columns.filter((c) => !hidden[c.key]) : columns;
  const alignClass = (a?: string) => (a === 'end' ? 'align-end' : a === 'center' ? 'align-center' : '');
  if (error && !rows?.length) return <ErrorState error={error} onRetry={onRetry} compact />;
  if (loading && !rows?.length) return <SkeletonTable columns={Math.min(Math.max(visible.length, 3), 8)} />;
  return (
    <>
      {columnsKey && (
        <div className="table-tools no-print">
          <ColumnMenu columns={columns} hidden={hidden} onChange={setHidden} />
        </div>
      )}
      <div className={`table-wrap ${loading ? 'is-refreshing' : ''}`} style={maxHeight ? { maxHeight } : undefined} aria-busy={loading || undefined}>
        <table className={`table ${dense ? 'dense' : ''}`}>
          {caption && <caption className="visually-hidden">{caption}</caption>}
          <thead>
            <tr>
              {visible.map((c) => {
                const sorted = sort?.key === c.key;
                const sortable = c.sortable && onSort;
                const toggle = () => onSort!({ key: c.key, desc: sorted ? !sort!.desc : false });
                return (
                  <th
                    key={c.key}
                    scope="col"
                    className={`${alignClass(c.align)} ${sortable ? 'sortable' : ''}`}
                    style={c.width ? { width: c.width } : undefined}
                    aria-sort={sorted ? (sort!.desc ? 'descending' : 'ascending') : sortable ? 'none' : undefined}
                  >
                    {sortable ? (
                      <button type="button" className="th-sort" onClick={toggle}>
                        {c.header}
                        <span className={`sort-ind ${sorted ? 'on' : ''}`} aria-hidden>
                          {sorted ? (sort!.desc ? <ArrowDown size={12} /> : <ArrowUp size={12} />) : <ArrowUpDown size={12} />}
                        </span>
                      </button>
                    ) : (
                      c.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {rows?.map((r, i) => (
              <tr
                key={rowKey(r, i)}
                className={`${onRowClick ? 'clickable' : ''} ${rowClassName?.(r) ?? ''}`}
                onClick={onRowClick ? () => onRowClick(r) : undefined}
                onKeyDown={onRowClick ? (e) => e.key === 'Enter' && onRowClick(r) : undefined}
                tabIndex={onRowClick ? 0 : undefined}
              >
                {visible.map((c) => (
                  <td key={c.key} className={`${alignClass(c.align)} ${c.className ?? ''}`}>
                    {c.cell(r, i)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
          {showFooter && rows && rows.length > 0 && (
            <tfoot>
              <tr>
                {visible.map((c) => (
                  <td key={c.key} className={alignClass(c.align)}>
                    {c.footer}
                  </td>
                ))}
              </tr>
            </tfoot>
          )}
        </table>
        {!loading && rows && rows.length === 0 && (empty ?? <EmptyState title={t('common.noResults')} body={t('common.noResultsHint')} filtered compact />)}
      </div>
    </>
  );
}

function ColumnMenu<T>({ columns, hidden, onChange }: { columns: Column<T>[]; hidden: Record<string, boolean>; onChange: (h: Record<string, boolean>) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const pop = useRef<HTMLDivElement>(null);
  const ref = useClickOutside<HTMLDivElement>(() => setOpen(false), open, pop);
  const optional = columns.filter((c) => c.optional);
  if (!optional.length) return null;
  return (
    <div className="menu" ref={ref}>
      <button ref={btn} type="button" className="btn btn-quiet btn-sm" aria-haspopup="true" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <Columns3 size={15} /> {t('ui.columns')}
      </button>
      {open && (
        <Popover anchor={btn} open popoverRef={pop} align="end" className="menu-panel" onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}>
          <div className="option-group">{t('ui.showColumns')}</div>
          {optional.map((c) => (
            <label key={c.key} className="menu-item checkbox">
              <input type="checkbox" checked={!hidden[c.key]} onChange={(e) => onChange({ ...hidden, [c.key]: !e.target.checked })} />
              <span>{c.header}</span>
            </label>
          ))}
        </Popover>
      )}
    </div>
  );
}

export function Pagination({ page, pageSize, total, onPage }: { page: number; pageSize: number; total: number; onPage: (p: number) => void }) {
  const { t } = useI18n();
  const f = useFormat();
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const to = Math.min(total, page * pageSize);
  return (
    <div className="table-footer">
      <span>{pages > 1 ? t('ui.rangeOf', { from: f.count(from), to: f.count(to), total: f.count(total) }) : t('common.rows', { count: f.count(total) })}</span>
      {pages > 1 && (
        <nav className="pagination" aria-label={t('ui.pagination')}>
          <button type="button" className="btn btn-quiet btn-icon btn-sm" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label={t('common.previous')}>
            <ChevronLeft size={16} className="icon-directional" />
          </button>
          <span aria-live="polite">{t('common.page', { page: f.count(page), pages: f.count(pages) })}</span>
          <button type="button" className="btn btn-quiet btn-icon btn-sm" disabled={page >= pages} onClick={() => onPage(page + 1)} aria-label={t('common.next')}>
            <ChevronRight size={16} className="icon-directional" />
          </button>
        </nav>
      )}
    </div>
  );
}
