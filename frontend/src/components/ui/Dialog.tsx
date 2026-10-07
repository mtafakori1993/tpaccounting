import { useEffect, useId, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from './Button';
import { aiKey } from './aiTags';

interface Props {
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
  /** md (560px), wide (860px) or xwide (1120px); full screen on phones */
  size?: 'md' | 'wide' | 'xwide';
  /** short line under the title */
  description?: ReactNode;
}

/** Modal dialog with focus containment and Esc to close. */
export function Dialog({ title, onClose, children, footer, wide, size, description }: Props) {
  const { t } = useI18n();
  const titleId = useId();
  const ref = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  // Runs once per dialog: focusing on every parent re-render made stacked dialogs fight over focus.
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const first = ref.current?.querySelector<HTMLElement>('input, select, textarea, button:not(.dialog-close)');
    first?.focus();
    const onKey = (e: KeyboardEvent) => {
      // Only the top-most dialog handles keys.
      const all = document.querySelectorAll('.dialog');
      if (all[all.length - 1] !== ref.current) return;
      if (e.key === 'Escape') closeRef.current();
      if (e.key === 'Tab' && ref.current) {
        const focusables = ref.current.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])');
        if (!focusables.length) return;
        const firstEl = focusables[0];
        const lastEl = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === firstEl) {
          e.preventDefault();
          lastEl.focus();
        } else if (!e.shiftKey && document.activeElement === lastEl) {
          e.preventDefault();
          firstEl.focus();
        }
      }
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      if (prev && document.contains(prev)) prev.focus();
    };
  }, []);
  return createPortal(
    <div className="dialog-backdrop" onMouseDown={(e) => e.target === e.currentTarget && closeRef.current()}>
      <div className={`dialog ${size ?? (wide ? 'wide' : '')}`} role="dialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={description ? `${titleId}-d` : undefined} ref={ref} data-ai-dialog={aiKey(title)}>
        <div className="dialog-head">
          <div>
            <h2 className="dialog-title" id={titleId}>{title}</h2>
            {description && <div className="dialog-desc" id={`${titleId}-d`}>{description}</div>}
          </div>
          <button type="button" className="btn btn-quiet btn-icon dialog-close" aria-label={t('common.close')} data-ai-action="common.close" onClick={onClose}>
            <X size={18} />
          </button>
        </div>
        <div className="dialog-body">{children}</div>
        {footer && <div className="dialog-foot">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

/** Confirmation for irreversible or significant actions only. */
export function ConfirmDialog({ title, body, confirmLabel, onConfirm, onClose, pending, negative, error }: {
  title: ReactNode;
  body: ReactNode;
  confirmLabel: string;
  onConfirm: () => void;
  onClose: () => void;
  pending?: boolean;
  negative?: boolean;
  error?: string | null;
}) {
  const { t } = useI18n();
  return (
    <Dialog
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant={negative ? 'negative' : 'accent'} onClick={onConfirm} pending={pending}>{confirmLabel}</Button>
        </>
      }
    >
      <div className="stack">
        <p>{body}</p>
        {error && <div className="alert alert-danger">{error}</div>}
      </div>
    </Dialog>
  );
}
