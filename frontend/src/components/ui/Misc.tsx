import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { AlertCircle, ChevronRight, MoreHorizontal } from 'lucide-react';
import { useI18n, type TKey } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useClickOutside } from '@/lib/hooks';
import { Popover } from './Popover';
import { toBig } from '@/lib/decimal';

export function PageHeader({ title, subtitle, crumbs, actions, meta }: { title: ReactNode; subtitle?: ReactNode; crumbs?: { label: string; to?: string }[]; actions?: ReactNode; meta?: ReactNode }) {
  return (
    <div className="page-header">
      <div>
        {crumbs && crumbs.length > 0 && (
          <nav className="breadcrumbs" aria-label="breadcrumb">
            {crumbs.map((c, i) => (
              <span key={i} className="row" style={{ gap: 4 }}>
                {c.to ? <Link to={c.to}>{c.label}</Link> : <span>{c.label}</span>}
                <ChevronRight size={14} className="icon-directional" aria-hidden />
              </span>
            ))}
          </nav>
        )}
        <h1 className="page-title">{title}</h1>
        {subtitle && <div className="page-subtitle">{subtitle}</div>}
        {meta && <div className="page-meta">{meta}</div>}
      </div>
      {actions && <div className="row no-print">{actions}</div>}
    </div>
  );
}

export function Panel({ title, actions, children, className = '', bodyless }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string; bodyless?: boolean }) {
  return (
    <section className={`panel ${className}`}>
      {(title || actions) && (
        <div className="panel-head">
          {title ? <h2 className="panel-title">{title}</h2> : <span />}
          {actions && <div className="row no-print">{actions}</div>}
        </div>
      )}
      {bodyless ? children : <div className="panel-body">{children}</div>}
    </section>
  );
}

export function Spinner() {
  return <span className="spinner" role="progressbar" aria-busy />;
}

export function LoadingBlock() {
  const { t } = useI18n();
  return (
    <div className="loading-block">
      <Spinner /> {t('common.loading')}
    </div>
  );
}

export function Alert({ kind = 'danger', children }: { kind?: 'danger' | 'warning' | 'info' | 'success'; children: ReactNode }) {
  return (
    <div className={`alert alert-${kind}`} role={kind === 'danger' ? 'alert' : 'status'}>
      {kind === 'danger' && <AlertCircle size={18} style={{ flexShrink: 0, marginTop: 2 }} />}
      <div>{children}</div>
    </div>
  );
}

const statusTone: Record<string, string> = {
  draft: '', posted: 'success', cancelled: 'danger', open: 'info', closed: '', paid: 'success', unpaid: 'warning', partial: 'info', overdue: 'danger',
  received: 'info', in_custody: 'info', deposited: 'warning', cleared: 'success', transferred: '', returned: 'danger', lost: 'danger', issued: 'info',
  delivered: 'info', replaced: '', calculated: 'info', approved: 'warning', submitted: 'warning', active: 'success', archived: '',
};

export function StatusBadge({ status }: { status: string }) {
  const { tryT } = useI18n();
  const tone = statusTone[status] ?? '';
  return <span className={`badge ${tone ? `badge-${tone}` : ''}`}>{tryT(`status.${status}`) ?? status}</span>;
}

export function Badge({ children, tone }: { children: ReactNode; tone?: 'success' | 'warning' | 'danger' | 'info' }) {
  return <span className={`badge plain ${tone ? `badge-${tone}` : ''}`}>{children}</span>;
}

/**
 * A monetary amount (end-aligned by numeric table cells). Pass `currency` to
 * append the company currency — for KPIs and totals, not every table cell.
 */
export function Money({ value, blankZero, signed, currency }: { value: string | number | null | undefined; blankZero?: boolean; signed?: boolean; currency?: boolean }) {
  const f = useFormat();
  if (value === null || value === undefined) return null;
  const n = toBig(value);
  if (blankZero && n.eq(0)) return <span className="num muted">—</span>;
  return <span className={`num ${signed && n.lt(0) ? 'amount-neg' : ''}`}>{currency ? f.moneyWithCurrency(value) : f.money(value)}</span>;
}

/** Kept for existing call sites; same as Money. */
export const Amount = Money;

/** A grouped quantity or count (not an identifier — use f.digits for those). */
export function FormattedNumber({ value, fraction = 0 }: { value: string | number | null | undefined; fraction?: number }) {
  const f = useFormat();
  if (value === null || value === undefined || value === '') return <span className="num muted">—</span>;
  return <span className="num">{f.number(value, fraction)}</span>;
}

/** A value that is already a percentage: 72.5 → 72.5%. */
export function Percentage({ value }: { value: string | number | null | undefined }) {
  const f = useFormat();
  return <span className="num">{f.percent(value)}</span>;
}

/** A debit/credit balance: absolute value with Dr/Cr marker. */
export function BalanceAmount({ value }: { value: string | number | null | undefined }) {
  const f = useFormat();
  const { t } = useI18n();
  const n = toBig(value ?? 0);
  if (n.eq(0)) return <span className="num muted">{f.money(0)}</span>;
  return (
    <span className={`num ${n.lt(0) ? 'amount-cr' : ''}`}>
      {f.money(n.abs().toString())}
      <span className="side-tag">{n.gt(0) ? t('common.dr') : t('common.cr')}</span>
    </span>
  );
}

export function Tabs<K extends string>({ tabs, value, onChange, label }: { tabs: { key: K; label: ReactNode; count?: ReactNode; tone?: 'danger' | 'warning' }[]; value: K; onChange: (k: K) => void; label?: string }) {
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    // Arrow keys move between tabs (direction-aware), as in the WAI-ARIA tabs pattern.
    const i = tabs.findIndex((x) => x.key === value);
    const rtl = document.documentElement.dir === 'rtl';
    const next = e.key === (rtl ? 'ArrowLeft' : 'ArrowRight') ? i + 1 : e.key === (rtl ? 'ArrowRight' : 'ArrowLeft') ? i - 1 : null;
    if (next === null) return;
    e.preventDefault();
    const k = tabs[(next + tabs.length) % tabs.length].key;
    onChange(k);
    (e.currentTarget.querySelector(`[data-key="${k}"]`) as HTMLElement | null)?.focus();
  };
  return (
    <div className="tabs" role="tablist" aria-label={label} onKeyDown={onKey}>
      {tabs.map((tb) => (
        <button key={tb.key} data-key={tb.key} role="tab" type="button" className="tab" aria-selected={value === tb.key} tabIndex={value === tb.key ? 0 : -1} onClick={() => onChange(tb.key)}>
          {tb.label}
          {tb.count !== undefined && tb.count !== null && <span className={`tab-count ${tb.tone ?? ''}`}>{tb.count}</span>}
        </button>
      ))}
    </div>
  );
}

export interface MenuItem {
  label: ReactNode;
  icon?: ReactNode;
  onClick?: () => void;
  to?: string;
  danger?: boolean;
  hidden?: boolean;
  separator?: boolean;
}

export function ActionMenu({ items, label }: { items: MenuItem[]; label?: string }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const pop = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const ref = useClickOutside<HTMLDivElement>(() => setOpen(false), open, pop);
  const visible = items.filter((i) => !i.hidden);
  if (!visible.length) return null;
  return (
    <div className="menu" ref={ref}>
      <button ref={btn} type="button" className="btn btn-secondary btn-icon" aria-haspopup="menu" aria-expanded={open} aria-label={label ?? t('common.more')} title={label ?? t('common.more')} onClick={() => setOpen((o) => !o)}>
        <MoreHorizontal size={18} />
      </button>
      {open && (
        <Popover anchor={btn} open popoverRef={pop} align="end" role="menu" className="menu-panel" onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}>
          {visible.map((it, i) =>
            it.separator ? (
              <div key={i} className="menu-sep" />
            ) : it.to ? (
              <Link key={i} to={it.to} className={`menu-item ${it.danger ? 'danger' : ''}`} role="menuitem" onClick={() => setOpen(false)}>
                {it.icon}
                {it.label}
              </Link>
            ) : (
              <button key={i} type="button" role="menuitem" className={`menu-item ${it.danger ? 'danger' : ''}`} onClick={() => { setOpen(false); it.onClick?.(); }}>
                {it.icon}
                {it.label}
              </button>
            ),
          )}
        </Popover>
      )}
    </div>
  );
}

export { EmptyState } from './Feedback';

export function KV({ items }: { items: { label: ReactNode; value: ReactNode; hidden?: boolean }[] }) {
  return (
    <dl className="kv">
      {items.filter((i) => !i.hidden).map((i, idx) => (
        <div key={idx}>
          <dt>{i.label}</dt>
          <dd>{i.value === '' || i.value === null || i.value === undefined ? '—' : i.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Translate helper for components that receive a key. */
export function T({ k }: { k: TKey }) {
  const { t } = useI18n();
  return <>{t(k)}</>;
}
