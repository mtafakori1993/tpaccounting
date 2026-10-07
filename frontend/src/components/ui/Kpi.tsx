import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { SkeletonKpis } from './Feedback';

export type Tone = 'neutral' | 'primary' | 'success' | 'warning' | 'danger' | 'info';

/**
 * A key figure: label, value, an optional secondary line and a tone. With
 * `to` the whole card links to the detail behind the number.
 */
export function Kpi({ label, value, sub, tone = 'neutral', icon, to, footer }: {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  tone?: Tone;
  icon?: ReactNode;
  to?: string;
  footer?: ReactNode;
}) {
  const body = (
    <>
      <span className="kpi-label">
        {icon && <span className="kpi-icon" aria-hidden>{icon}</span>}
        {label}
      </span>
      <span className="kpi-value num">{value}</span>
      {sub && <span className="kpi-sub">{sub}</span>}
      {footer}
    </>
  );
  return to ? (
    <Link to={to} className={`kpi-card tone-${tone} clickable`}>{body}</Link>
  ) : (
    <div className={`kpi-card tone-${tone}`}>{body}</div>
  );
}

/** Responsive row of KPI cards, with a skeleton while loading. */
export function KpiGrid({ children, loading, count = 4 }: { children?: ReactNode; loading?: boolean; count?: number }) {
  if (loading) return <SkeletonKpis count={count} />;
  return <div className="kpi-grid">{children}</div>;
}
