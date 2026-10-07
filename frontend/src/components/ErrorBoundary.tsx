import { Component, type ErrorInfo, type ReactNode } from 'react';
import { useI18n } from '@/i18n/I18nProvider';

const RELOAD_KEY = 'tp.reloadedForUpdate';

/** True when a lazily loaded page chunk could not be fetched (usually after a new deployment). */
function isChunkError(err: unknown) {
  const msg = err instanceof Error ? err.message : String(err);
  return /dynamically imported module|Importing a module script failed|Failed to fetch|error loading dynamically|preload/i.test(msg);
}

/** Reloads once to pick up a newly deployed version; avoids reload loops. */
export function reloadForUpdate() {
  try {
    if (sessionStorage.getItem(RELOAD_KEY)) return false;
    sessionStorage.setItem(RELOAD_KEY, '1');
  } catch {
    /* storage unavailable: still reload once */
  }
  window.location.reload();
  return true;
}

export function clearReloadFlag() {
  try {
    sessionStorage.removeItem(RELOAD_KEY);
  } catch {
    /* ignore */
  }
}

function Fallback({ onRetry }: { onRetry: () => void }) {
  const { t } = useI18n();
  return (
    <div className="page">
      <div className="alert alert-danger" role="alert">
        <div className="stack-sm">
          <div>{t('errors.internal')}</div>
          <div className="row">
            <button type="button" className="btn btn-accent btn-sm" onClick={() => window.location.reload()}>{t('common.refresh')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={onRetry}>{t('common.retry')}</button>
          </div>
        </div>
      </div>
    </div>
  );
}

/** Keeps a failing page from blanking the whole app. Reset by changing `resetKey` (the route). */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: unknown }> {
  state = { error: null as unknown };

  static getDerivedStateFromError(error: unknown) {
    return { error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    if (isChunkError(error) && reloadForUpdate()) return;
    console.error('page error', error, info.componentStack);
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  render() {
    if (this.state.error) return <Fallback onRetry={() => this.setState({ error: null })} />;
    return this.props.children;
  }
}
