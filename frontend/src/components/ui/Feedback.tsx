import type { ReactNode } from 'react';
import { AlertTriangle, Inbox, RotateCcw, SearchX } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from './Button';

/** A shimmering placeholder block. */
export function Skeleton({ width = '100%', height = 14, radius, className = '' }: { width?: number | string; height?: number | string; radius?: number; className?: string }) {
  return <span className={`skeleton ${className}`} style={{ width, height, borderRadius: radius }} aria-hidden />;
}

/** Placeholder for a row of KPI cards. */
export function SkeletonKpis({ count = 4 }: { count?: number }) {
  const { t } = useI18n();
  return (
    <div className="kpi-grid" role="status" aria-label={t('common.loading')}>
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="kpi-card">
          <Skeleton width="55%" height={12} />
          <Skeleton width="80%" height={26} />
          <Skeleton width="35%" height={10} />
        </div>
      ))}
    </div>
  );
}

/** Placeholder for a table while its first page loads. */
export function SkeletonTable({ rows = 6, columns = 6 }: { rows?: number; columns?: number }) {
  const { t } = useI18n();
  return (
    <div className="skeleton-table" role="status" aria-label={t('common.loading')}>
      <div className="skeleton-row head">{Array.from({ length: columns }, (_, i) => <Skeleton key={i} height={10} width={`${50 + ((i * 17) % 40)}%`} />)}</div>
      {Array.from({ length: rows }, (_, r) => (
        <div key={r} className="skeleton-row">
          {Array.from({ length: columns }, (_, i) => <Skeleton key={i} height={12} width={`${40 + (((r + 1) * (i + 3) * 13) % 55)}%`} />)}
        </div>
      ))}
    </div>
  );
}

/** Placeholder for a whole report/detail page body. */
export function SkeletonPage({ kpis = 4 }: { kpis?: number }) {
  return (
    <div className="stack">
      {kpis > 0 && <SkeletonKpis count={kpis} />}
      <div className="panel"><SkeletonTable /></div>
    </div>
  );
}

/**
 * Explains why there is nothing to show and what to do next.
 * Use `filtered` when filters hide everything (offers clearing them instead).
 */
export function EmptyState({ title, body, action, icon, filtered, compact }: {
  title: ReactNode;
  body?: ReactNode;
  action?: ReactNode;
  icon?: ReactNode;
  filtered?: boolean;
  compact?: boolean;
}) {
  return (
    <div className={`state ${compact ? 'compact' : ''}`}>
      <span className="state-icon" aria-hidden>{icon ?? (filtered ? <SearchX size={22} /> : <Inbox size={22} />)}</span>
      <div className="state-title">{title}</div>
      {body && <div className="state-body">{body}</div>}
      {action && <div className="state-action">{action}</div>}
    </div>
  );
}

/** An actionable error: what failed, the translated reason, and a retry. */
export function ErrorState({ error, title, onRetry, compact }: { error: unknown; title?: ReactNode; onRetry?: () => void; compact?: boolean }) {
  const { t } = useI18n();
  const msg = useErrorMessage();
  return (
    <div className={`state error ${compact ? 'compact' : ''}`} role="alert">
      <span className="state-icon" aria-hidden><AlertTriangle size={22} /></span>
      <div className="state-title">{title ?? t('ui.loadFailed')}</div>
      <div className="state-body">{msg(error)}</div>
      {onRetry && (
        <div className="state-action">
          <Button icon={<RotateCcw size={16} />} onClick={onRetry}>{t('common.retry')}</Button>
        </div>
      )}
    </div>
  );
}
